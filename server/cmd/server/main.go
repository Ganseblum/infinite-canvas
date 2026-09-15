package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/crypto"
	"github.com/infinite-canvas/server/internal/db"
	"github.com/infinite-canvas/server/internal/handler"
	"github.com/infinite-canvas/server/internal/mail"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/moderation"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("配置加载失败", "err", err)
		os.Exit(1)
	}

	level := new(slog.LevelVar)
	switch cfg.LogLevel {
	case "debug":
		level.Set(slog.LevelDebug)
	case "warn":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	default:
		level.Set(slog.LevelInfo)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	gormDB, err := db.Connect(cfg.DatabaseURL)
	if err != nil {
		slog.Error("数据库连接失败", "err", err)
		os.Exit(1)
	}
	if err := db.Migrate(gormDB); err != nil {
		slog.Error("AutoMigrate 失败", "err", err)
		os.Exit(1)
	}
	if err := db.SeedPlans(gormDB); err != nil {
		slog.Error("写入默认档位失败", "err", err)
		os.Exit(1)
	}
	if err := db.SeedBilling(gormDB); err != nil {
		slog.Error("写入默认档位与模型目录失败", "err", err)
		os.Exit(1)
	}
	if err := db.EnsureAdmin(gormDB, cfg.AdminEmail, cfg.AdminPassword); err != nil {
		slog.Error("初始化管理员失败", "err", err)
		os.Exit(1)
	}
	if cfg.SeedTestData {
		// 测试数据随代码入库，开关一开就能把固定账号与示例内容导入任何环境（幂等）。
		slog.Warn("SEED_TEST_DATA 已开启，将写入公开密码的测试账号；正式环境务必保持关闭")
		if err := db.SeedTestData(gormDB, cfg.SeedTestDataEmail, cfg.SeedTestDataPassword); err != nil {
			slog.Error("写入测试数据失败", "err", err)
			os.Exit(1)
		}
	}

	mailer := mail.New(mail.Config{
		Driver:     cfg.MailDriver,
		Host:       cfg.SMTPHost,
		Port:       cfg.SMTPPort,
		Username:   cfg.SMTPUsername,
		Password:   cfg.SMTPPassword,
		From:       cfg.SMTPFrom,
		Security:   cfg.SMTPSecurity,
		AppBaseURL: cfg.AppBaseURL,
	})

	// 媒体存储驱动：local 落 MEDIA_ROOT 磁盘卷，s3 走 S3 兼容对象存储。
	var mediaStorage storage.Storage
	if cfg.StorageDriver == "s3" {
		s3Storage, err := storage.NewS3(storage.S3Config{
			Endpoint:        cfg.S3Endpoint,
			Region:          cfg.S3Region,
			Bucket:          cfg.S3Bucket,
			AccessKeyID:     cfg.S3AccessKeyID,
			SecretAccessKey: cfg.S3SecretAccessKey,
			ForcePathStyle:  cfg.S3ForcePathStyle,
			PresignAlign:    cfg.S3PresignAlign,
			PresignTTL:      cfg.S3PresignTTL,
		})
		if err != nil {
			slog.Error("初始化 S3 存储驱动失败", "err", err)
			os.Exit(1)
		}
		mediaStorage = s3Storage
	} else {
		mediaStorage = storage.NewLocal(cfg.MediaRoot)
	}

	// 第四期：平台渠道加密、报价与 AI 转发。
	credentialCipher, err := crypto.New(cfg.CredentialKey)
	if err != nil {
		slog.Error("初始化凭据加密器失败", "err", err)
		os.Exit(1)
	}

	siteSettings := service.NewSiteSettingService(gormDB)
	if err := siteSettings.Load(context.Background()); err != nil {
		slog.Error("读取站点设置失败", "err", err)
		os.Exit(1)
	}
	authHandler := handler.NewAuthHandler(gormDB, cfg, mailer)
	authHandler.SetSettings(siteSettings)
	grantService := service.NewFreeGrantService(gormDB)
	accountHandler := handler.NewAccountHandler(gormDB, cfg, grantService, authHandler)
	canvasHandler := handler.NewCanvasHandler(gormDB)
	assetHandler := handler.NewAssetHandler(gormDB)
	generationHandler := handler.NewGenerationHandler(gormDB)

	// 第五期：内容审核。审核链路的开关、阈值与 fail mode 全部来自环境变量，
	// fail mode 未配置时启动即失败，不在代码里静默默认放行。
	var moderationProvider moderation.Provider
	if cfg.ModerationEnabled {
		switch cfg.ModerationProvider {
		case "fake":
			// 本地开发的可控实现：命中 MODERATION_FAKE_REJECT_TEXTS 的文本被拒。
			fake := moderation.NewFake()
			if raw := cfg.ModerationFakeRejectTexts; raw != "" {
				fake.RejectTexts = strings.Split(raw, ",")
			}
			moderationProvider = fake
		case "nsfwjs+detoxify":
			nsfwjs := moderation.NewNSFWJS(cfg.ModerationNSFWJSEndpoint, cfg.ModerationImageThreshold, cfg.ModerationTimeout)
			detoxify := moderation.NewDetoxify(cfg.ModerationDetoxifyEndpoint, cfg.ModerationTextThreshold, cfg.ModerationTimeout)
			moderationProvider = moderation.NewCombined(nsfwjs, detoxify)
		}
	}
	quarantineService := service.NewQuarantineService(gormDB, mediaStorage, credentialCipher, cfg.ModerationQuarantineTTL)
	moderationService := service.NewModerationService(gormDB, moderationProvider, quarantineService, service.ModerationConfig{
		Enabled:  cfg.ModerationEnabled,
		FailMode: cfg.ModerationFailMode,
		Policy:   cfg.ModerationPolicyVersion,
		Timeout:  cfg.ModerationTimeout,
		CacheTTL: cfg.ModerationCacheTTL,
	})

	mediaHandler := handler.NewMediaHandler(gormDB, mediaStorage, moderationService)
	paymentRegistry, err := service.NewPaymentRegistry(cfg)
	if err != nil {
		slog.Error("支付渠道配置无效", "err", err)
		os.Exit(1)
	}
	creditHandler := handler.NewCreditHandler(gormDB, paymentRegistry)
	orderHandler := handler.NewOrderHandler(gormDB, paymentRegistry)
	paymentHandler := handler.NewPaymentHandler(gormDB, paymentRegistry)
	catalogHandler := handler.NewModelHandler(gormDB, func() bool { return cfg.PromotionEnabled })

	timeouts := service.DefaultUpstreamTimeouts()
	if cfg.AIImageTimeout > 0 {
		timeouts.ImageTotal = cfg.AIImageTimeout
	}
	if cfg.AIAudioTimeout > 0 {
		timeouts.SpeechTotal = cfg.AIAudioTimeout
	}
	if cfg.AIStreamTimeout > 0 {
		timeouts.StreamTotal = cfg.AIStreamTimeout
	}
	if cfg.AIStreamIdleTimeout > 0 {
		timeouts.StreamIdle = cfg.AIStreamIdleTimeout
	}
	if cfg.AIVideoTaskTimeout > 0 {
		timeouts.VideoTask = cfg.AIVideoTaskTimeout
	}
	upstreamService := service.NewUpstreamService(gormDB, credentialCipher, timeouts)
	upstreamService.SetAllowPrivate(cfg.AIAllowPrivateUpstream)
	catalogService := service.NewCatalogService(gormDB, func() bool { return cfg.PromotionEnabled })
	quotaService := service.NewQuotaService(gormDB)
	quoteService := service.NewQuoteService(catalogService, quotaService, cfg.JWTSecret)
	aiHandler := handler.NewAIHandler(gormDB, catalogService, quoteService, upstreamService, mediaStorage, cfg.AppBaseURL, moderationService)
	aiTaskService := service.NewAITaskService(gormDB, upstreamService, service.NewMediaWriteService(gormDB, mediaStorage))
	aiTaskService.SetModeration(moderationService)
	requestService := service.NewAIRequestService(gormDB)

	adminHandler := handler.NewAdminHandlerWithUpstream(gormDB, cfg, mediaStorage, upstreamService)
	adminHandler.SetModeration(moderationService)
	adminHandler.SetSettings(siteSettings)
	communityHandler := handler.NewCommunityHandler(gormDB, siteSettings)
	activityHandler := handler.NewActivityHandler(gormDB, siteSettings)

	router := gin.New()
	router.Use(middleware.RequestID(), middleware.Logger(level.Level()), middleware.Recovery())
	// 仅信任 TRUSTED_PROXIES 配置的代理网段解析 X-Forwarded-For / X-Real-IP；
	// 默认为空表示不信任任何代理头，ClientIP 直接使用 RemoteAddr。
	if err := router.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		slog.Error("TRUSTED_PROXIES 配置无效", "err", err)
		os.Exit(1)
	}

	// 健康检查（不在 /api 分组下，不鉴权，只在 compose 网络内被探测）
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/readyz", func(c *gin.Context) {
		sqlDB, err := gormDB.DB()
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		if err := sqlDB.PingContext(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	secret := []byte(cfg.JWTSecret)

	// 限流器
	regLimiter := middleware.NewLimiter(time.Hour, 10)
	loginLimiter := middleware.NewLimiter(10*time.Minute, 20)
	mailLimiter := middleware.NewLimiter(time.Hour, 10)
	orderLimiter := middleware.NewLimiter(time.Hour, 10)

	api := router.Group("/api")

	authGroup := api.Group("/auth")
	{
		authGroup.POST("/register", middleware.RateLimit(regLimiter, func(c *gin.Context) string { return "reg:" + middleware.ClientIP(c) }), authHandler.Register)
		authGroup.POST("/login", middleware.RateLimit(loginLimiter, func(c *gin.Context) string { return "login:" + middleware.ClientIP(c) }), authHandler.Login)
		authGroup.POST("/refresh", authHandler.Refresh)
		authGroup.POST("/logout", authHandler.Logout)
		authGroup.POST("/verify-email/send", middleware.Auth(secret), middleware.RateLimit(mailLimiter, func(c *gin.Context) string { return "mail:" + middleware.ClientIP(c) }), authHandler.VerifyEmailSend)
		authGroup.POST("/verify-email", authHandler.VerifyEmail)
		authGroup.POST("/password/forgot", middleware.RateLimit(mailLimiter, func(c *gin.Context) string { return "mail:" + middleware.ClientIP(c) }), authHandler.ForgotPassword)
		authGroup.POST("/password/reset", authHandler.ResetPassword)
	}

	// 全站业务接口要求登录且账号未封禁；注销冷静期的账号可以浏览，但生成与下单被拦截。
	active := middleware.RequireActiveUser(gormDB)

	me := api.Group("/me", middleware.Auth(secret), active)
	{
		me.GET("", accountHandler.GetMe)
		me.PATCH("", accountHandler.UpdateMe)
		me.POST("/password", accountHandler.ChangePassword)
		me.POST("/free-grant/claim", accountHandler.ClaimFreeGrant)
		me.POST("/deletion", accountHandler.RequestDeletion)
		me.POST("/deletion/cancel", accountHandler.CancelDeletion)
	}

	canvases := api.Group("/canvases", middleware.Auth(secret), active)
	{
		canvases.GET("", canvasHandler.List)
		canvases.POST("", canvasHandler.Create)
		canvases.GET("/:id", canvasHandler.Get)
		canvases.PUT("/:id", canvasHandler.Update)
		canvases.PATCH("/:id", canvasHandler.Patch)
		canvases.DELETE("/:id", canvasHandler.Delete)
	}

	assets := api.Group("/assets", middleware.Auth(secret), active)
	{
		assets.GET("", assetHandler.List)
		assets.POST("", assetHandler.Create)
		assets.GET("/:id", assetHandler.Get)
		assets.PATCH("/:id", assetHandler.Patch)
		assets.DELETE("/:id", assetHandler.Delete)
	}

	generations := api.Group("/generations", middleware.Auth(secret), active)
	{
		generations.GET("", generationHandler.List)
		generations.GET("/:id", generationHandler.Get)
		generations.DELETE("/:id", generationHandler.Delete)
	}

	// 社区：作品复用用户素材，浏览无需额外权限，发布与互动需登录。
	community := api.Group("/community", middleware.Auth(secret), active)
	{
		community.GET("/works", communityHandler.List)
		community.GET("/works/mine", communityHandler.MyWorks)
		community.POST("/works", communityHandler.Publish)
		community.GET("/works/:id", communityHandler.Get)
		community.DELETE("/works/:id", communityHandler.Delete)
		community.POST("/works/:id/like", communityHandler.Like)
		community.POST("/works/:id/unlike", communityHandler.Like)
		community.POST("/works/:id/report", communityHandler.Report)
		community.GET("/users/:id", communityHandler.UserProfile)
	}

	// 运营活动：签到与邀请返利只进赠送桶。
	activity := api.Group("/activity", middleware.Auth(secret), active)
	{
		activity.GET("/checkin", activityHandler.CheckinStatus)
		activity.POST("/checkin", activityHandler.Checkin)
		activity.GET("/invite", activityHandler.InviteInfo)
		activity.POST("/invite/bind", activityHandler.BindInvite)
	}

	// 站点公开设置：公告、维护状态与功能开关。
	api.GET("/settings/public", adminHandler.PublicSettings)

	// AI 报价与生成：全部走平台目录与服务端托管的渠道。
	ai := api.Group("/ai", middleware.Auth(secret), active)
	{
		ai.POST("/quote", aiHandler.Quote)
		ai.POST("/images/generations", aiHandler.Images)
		ai.POST("/videos/generations", aiHandler.CreateVideo)
		ai.GET("/videos/tasks/:id", aiHandler.VideoTask)
		ai.POST("/audio/speech", aiHandler.Speech)
		ai.POST("/chat/completions", aiHandler.Chat)
	}

	// 媒体读路径额外认 ic_media cookie（GET/HEAD），写路径只认 Bearer。
	media := api.Group("/media", middleware.MediaAuth(secret), active)
	{
		media.HEAD("/:storageKey", mediaHandler.Head)
		media.GET("/:storageKey", mediaHandler.Get)
		media.PUT("/:storageKey", mediaHandler.Put)
		media.DELETE("/:storageKey", mediaHandler.Delete)
	}

	// 点数、档位与模型目录
	billing := api.Group("", middleware.Auth(secret), active)
	{
		billing.GET("/credits", creditHandler.GetBalance)
		billing.GET("/credits/transactions", creditHandler.ListTransactions)
		billing.GET("/credit-packages", creditHandler.ListPackages)
		billing.GET("/plans", creditHandler.ListPlans)
		billing.GET("/models", catalogHandler.List)
	}

	// 订单：下单额外要求不在注销冷静期，并按用户限流防刷垃圾待支付订单。
	orders := api.Group("/orders", middleware.Auth(secret), active)
	{
		orders.POST("", middleware.RequireNotPendingDeletion(),
			middleware.RateLimit(orderLimiter, func(c *gin.Context) string { return "order:" + c.GetString("user_id") }),
			orderHandler.Create)
		orders.GET("", orderHandler.List)
		orders.GET("/:id", orderHandler.Get)
		orders.POST("/:id/cancel", orderHandler.Cancel)
	}

	// 支付回调是唯一的公开写接口：不鉴权但必须验签。
	api.POST("/payments/webhook/:provider", paymentHandler.Webhook)

	// 管理后台：非管理员返回 403，前端隐藏入口只是体验优化。
	admin := api.Group("/admin", middleware.Auth(secret), active, middleware.AdminOnly())
	{
		admin.GET("/stats", adminHandler.Stats)
		admin.GET("/users", adminHandler.ListUsers)
		admin.GET("/users/:id", adminHandler.GetUser)
		admin.PATCH("/users/:id", adminHandler.PatchUser)
		admin.POST("/users/:id/password", adminHandler.ResetPassword)
		admin.POST("/users/:id/credits", adminHandler.AdjustCredits)
		admin.POST("/users/:id/usage/recalculate", adminHandler.RecalculateUsage)
		admin.POST("/users/:id/media/reclaim", adminHandler.ReclaimMedia)
		admin.GET("/models", adminHandler.ListModels)
		admin.POST("/models", adminHandler.CreateModel)
		admin.PATCH("/models/:id", adminHandler.UpdateModel)
		admin.DELETE("/models/:id", adminHandler.DeleteModel)
		admin.GET("/model-promotions", adminHandler.ListPromotions)
		admin.POST("/model-promotions", adminHandler.CreatePromotion)
		admin.PATCH("/model-promotions/:id", adminHandler.UpdatePromotion)
		admin.GET("/credit-packages", adminHandler.ListPackages)
		admin.POST("/credit-packages", adminHandler.CreatePackage)
		admin.PATCH("/credit-packages/:id", adminHandler.UpdatePackage)
		admin.GET("/orders", adminHandler.ListOrders)
		admin.GET("/channels", adminHandler.ListChannels)
		admin.POST("/channels", adminHandler.CreateChannel)
		admin.PATCH("/channels/:id", adminHandler.UpdateChannel)
		admin.DELETE("/channels/:id", adminHandler.DeleteChannel)
		admin.POST("/requests/refunds/retry", adminHandler.RetryRefunds)
		admin.GET("/moderation/records", adminHandler.ListModerationRecords)
		admin.GET("/moderation/records/:id", adminHandler.GetModerationRecord)
		admin.GET("/moderation/records/:id/preview", adminHandler.PreviewModerationArtifact)
		admin.PATCH("/moderation/records/:id", adminHandler.ReviewModerationRecord)
		admin.POST("/moderation/records/:id/compensate", adminHandler.CompensateModeration)
		admin.GET("/moderation/stats", adminHandler.ModerationStats)
		admin.GET("/settings", adminHandler.GetSettings)
		admin.PATCH("/settings", adminHandler.UpdateSettings)
		admin.GET("/admins", adminHandler.ListAdmins)
		admin.POST("/admins", adminHandler.AddAdmin)
		admin.DELETE("/admins/:id", adminHandler.RemoveAdmin)
		admin.GET("/audit-logs", adminHandler.ListAuditLogs)
		admin.GET("/community/works", adminHandler.ListCommunityWorks)
		admin.PATCH("/community/works/:id", adminHandler.PatchCommunityWork)
		admin.GET("/community/reports", adminHandler.ListCommunityReports)
		admin.PATCH("/community/reports/:id", adminHandler.PatchCommunityReport)
		admin.GET("/stats/revenue", adminHandler.RevenueStats)
	}

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 后台定时任务：超时订单扫描与注销冷静期到期匿名化。
	orderService := service.NewOrderService(gormDB, paymentRegistry)
	deletionService := service.NewDeletionService(gormDB)
	go runScheduled(ctx, "订单超时扫描", 5*time.Minute, func() {
		if n, err := orderService.ExpirePendingOrders(ctx, time.Now(), 30*time.Minute); err != nil {
			slog.Error("订单超时扫描失败", "err", err)
		} else if n > 0 {
			slog.Info("已关闭超时订单", "count", n)
		}
	})
	// 第四期：启动时收敛滞留的生成请求，并恢复视频任务轮询。
	if count, err := requestService.ConvergeStaleRunning(ctx, upstreamService.TimeoutFor, time.Now()); err != nil {
		slog.Error("收敛滞留生成请求失败", "err", err)
	} else if count > 0 {
		slog.Info("已收敛重启前的生成请求", "count", count)
	}
	go aiTaskService.StartTaskPoller(ctx, 10*time.Second, func(count int) {
		slog.Info("恢复视频任务轮询", "count", count)
	})
	go runScheduled(ctx, "隔离区过期清理", time.Hour, func() {
		if count, err := quarantineService.CleanupExpired(ctx, time.Now()); err != nil {
			slog.Error("隔离区清理失败", "err", err)
		} else if count > 0 {
			slog.Info("已清理过期隔离原件", "count", count)
		}
	})
	go runScheduled(ctx, "注销到期匿名化", time.Hour, func() {
		if n, err := deletionService.AnonymizeExpired(ctx, time.Now()); err != nil {
			slog.Error("账号匿名化任务失败", "err", err)
		} else if n > 0 {
			slog.Info("已匿名化到期的注销账号", "count", n)
		}
	})

	go func() {
		slog.Info("后端服务启动", "port", cfg.Port, "mail_driver", cfg.MailDriver)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP 服务异常退出", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("正在优雅关闭…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("优雅关闭失败", "err", err)
	}
}

// runScheduled 立即执行一次后按固定间隔重复执行，ctx 取消即停止。
func runScheduled(ctx context.Context, name string, interval time.Duration, task func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	task()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			task()
		}
	}
}
