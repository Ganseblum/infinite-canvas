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

	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/crypto"
	"github.com/infinite-canvas/server/internal/db"
	"github.com/infinite-canvas/server/internal/envload"
	"github.com/infinite-canvas/server/internal/handler"
	"github.com/infinite-canvas/server/internal/mail"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/moderation"
	"github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/platform/identity"
	platformstorage "github.com/infinite-canvas/server/internal/platform/storage"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/storage"
	"github.com/infinite-canvas/server/internal/watermark"
)

func main() {
	// 本地 go run 直启时加载仓库根目录的 .env（已存在的环境变量不被覆盖）；
	// 容器内该文件不存在，静默跳过（差异清单 #69）。
	if err := envload.Load(".env"); err != nil {
		slog.Warn("加载 .env 失败，忽略", "err", err)
	}
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
	// 结构版本留痕：AutoMigrate 无回滚，先记录「哪个版本建出了当前结构」。
	if err := db.RecordSchemaVersion(gormDB); err != nil {
		slog.Warn("记录 schema 版本失败（不阻断启动）", "err", err)
	}
	if err := db.SeedMembershipPlans(gormDB); err != nil {
		slog.Error("写入默认档位失败", "err", err)
		os.Exit(1)
	}
	if err := db.SeedBilling(gormDB); err != nil {
		slog.Error("写入默认档位与模型目录失败", "err", err)
		os.Exit(1)
	}
	// RBAC 目录同步：系统角色、权限点投影、改名别名迁移与老数据回填，幂等。
	if err := authz.Sync(gormDB); err != nil {
		slog.Error("同步角色与权限目录失败", "err", err)
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
	// 平台身份域：认证、授权与管理端的身份读写统一入口。
	idn := identity.NewService(gormDB)
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

	// 媒体水印服务：WATERMARK_ENABLED 只控制生成链路是否烧录（生成挂钩消费），
	// 下发闸门与下载端点不读该开关、永远在线（计划红线）。
	wmService := watermark.NewService(cfg.WatermarkFontPath, cfg.WatermarkText)
	mediaHandler := handler.NewMediaHandler(gormDB, mediaStorage, moderationService, []byte(cfg.JWTSecret), wmService)
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
	// 平台权益三域：报价的免费试用判定走 billing 域，媒体记账与档位派生走 storage/membership 域。
	catalogService := service.NewCatalogService(gormDB, func() bool { return cfg.PromotionEnabled })
	billingService := billing.NewService(gormDB, model.ProductCanvas)
	storageService := platformstorage.NewService(gormDB, model.ProductCanvas)
	quoteService := service.NewQuoteService(catalogService, billingService, cfg.JWTSecret)
	aiHandler := handler.NewAIHandler(gormDB, catalogService, quoteService, upstreamService, mediaStorage, cfg.AppBaseURL, moderationService)
	aiTaskService := service.NewAITaskService(gormDB, upstreamService, service.NewMediaWriteService(gormDB, mediaStorage))
	aiTaskService.SetModeration(moderationService)
	// T5 生成落盘水印挂钩：WATERMARK_ENABLED 只控制生成时是否烧录（下发闸门与下载
	// 端点永远在线，不随 flag 下线）。开关关闭时挂钩不生效，生成链路行为与现状一致。
	aiHandler.SetWatermark(wmService, watermark.Enabled)
	aiTaskService.SetWatermark(wmService, watermark.Enabled)
	requestService := service.NewAIRequestService(gormDB)

	adminHandler := handler.NewAdminHandlerWithUpstream(gormDB, cfg, mediaStorage, upstreamService)
	adminHandler.SetModeration(moderationService)
	adminHandler.SetSettings(siteSettings)
	communityHandler := handler.NewCommunityHandler(gormDB, siteSettings)
	activityHandler := handler.NewActivityHandler(gormDB, siteSettings, grantService, cfg)

	// M3 OIDC Provider（PLAN T09）：签名密钥从 OIDC_JWKS_PRIVATE_KEY（PEM PKCS#8）读取，
	// 配置了但解析失败直接退出（fail-fast）；未配置时生成临时密钥并告警——重启后旧 token 失效，
	// 生产环境必须显式配置。
	oidcHandler, err := handler.NewOIDCHandler(gormDB, cfg, idn)
	if err != nil {
		slog.Error("初始化 OIDC Provider 失败", "err", err)
		os.Exit(1)
	}

	router := gin.New()
	router.Use(middleware.RequestID(), middleware.Logger(level.Level()), middleware.Recovery())
	// 管理后台独立部署时跨源直连 API；CORS_ALLOWED_ORIGINS 为空则完全不启用。
	// 必须挂在引擎级：group handlers 在路由注册时固化，挂在 /api/admin 会漏掉 /api/auth/login 等路径。
	router.Use(middleware.CORS(cfg.CORSAllowedOrigins))
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
	// 社区发布限流：与 handler 内的每日上限双保险（差异清单 #38）。
	publishLimiter := middleware.NewLimiter(time.Hour, 12)
	// 申请下载限流：签发端无状态，按用户限流防刷短时效链接（60 次/小时）。
	downloadLimiter := middleware.NewLimiter(time.Hour, 60)

	api := router.Group("/api")

	// 维护模式写拦截（差异清单 #117）：开启后非管理员的写请求 503，
	// 读操作、认证、管理端与支付回调放行。
	api.Use(middleware.MaintenanceGate(siteSettings, idn, secret))

	// 强制改密：must_change_password 的账号只能登出、刷新或改密，其余接口一律 403。
	// 挂到每个需要登录的路由上，新增受保护分组必须一并挂上。
	passwordGate := middleware.RequirePasswordChanged(idn)

	authGroup := api.Group("/auth")
	{
		authGroup.POST("/register", middleware.RateLimit(regLimiter, func(c *gin.Context) string { return "reg:" + middleware.ClientIP(c) }), authHandler.Register)
		authGroup.POST("/login", middleware.RateLimit(loginLimiter, func(c *gin.Context) string { return "login:" + middleware.ClientIP(c) }), authHandler.Login)
		authGroup.POST("/refresh", authHandler.Refresh)
		authGroup.POST("/logout", authHandler.Logout)
		authGroup.POST("/verify-email/send", middleware.Auth(secret), passwordGate, middleware.RateLimit(mailLimiter, func(c *gin.Context) string { return "mail:" + middleware.ClientIP(c) }), authHandler.VerifyEmailSend)
		authGroup.POST("/verify-email", authHandler.VerifyEmail)
		authGroup.POST("/password/forgot", middleware.RateLimit(mailLimiter, func(c *gin.Context) string { return "mail:" + middleware.ClientIP(c) }), authHandler.ForgotPassword)
		authGroup.POST("/password/reset", authHandler.ResetPassword)
	}

	// 全站业务接口要求登录且账号未封禁；注销冷静期的账号可以浏览，但生成与下单被拦截。
	active := middleware.RequireActiveUser(idn)

	// OIDC Provider 四端点（PLAN T09）：authorize 要求登录（Auth + RequireActiveUser），
	// token/jwks 公开；userinfo 在 handler 内验 OIDC 签发的 RS256 access token，
	// 与平台 HS256 会话是两套凭据，不走 middleware.Auth。/api/oidc/ 也在维护模式豁免前缀内
	// （token 是 POST，不能被维护模式拦断）。T09 不含 admin 管理端点（T10 再挂 sso.read/write）。
	oidc := api.Group("/oidc")
	oidc.GET("/authorize", middleware.Auth(secret), active, oidcHandler.Authorize)
	oidc.POST("/token", oidcHandler.Token)
	oidc.GET("/userinfo", oidcHandler.Userinfo)
	oidc.GET("/jwks.json", oidcHandler.JWKS)

	me := api.Group("/me", middleware.Auth(secret), active, passwordGate)
	{
		me.GET("", accountHandler.GetMe)
		me.PATCH("", accountHandler.UpdateMe)
		me.POST("/password", accountHandler.ChangePassword)
		me.POST("/free-grant/claim", middleware.RequireNotPendingDeletion(), accountHandler.ClaimFreeGrant)
		me.POST("/deletion", accountHandler.RequestDeletion)
		me.POST("/deletion/cancel", accountHandler.CancelDeletion)
		// 个人数据导出（可携带权占位实现）：JSON 汇总，不含媒体二进制。
		me.GET("/export", accountHandler.ExportMe)
	}

	canvases := api.Group("/canvases", middleware.Auth(secret), active, passwordGate)
	{
		canvases.GET("", canvasHandler.List)
		canvases.POST("", canvasHandler.Create)
		canvases.GET("/:id", canvasHandler.Get)
		canvases.PUT("/:id", canvasHandler.Update)
		canvases.PATCH("/:id", canvasHandler.Patch)
		canvases.DELETE("/:id", canvasHandler.Delete)
	}

	assets := api.Group("/assets", middleware.Auth(secret), active, passwordGate)
	{
		assets.GET("", assetHandler.List)
		assets.POST("", assetHandler.Create)
		assets.GET("/:id", assetHandler.Get)
		assets.PATCH("/:id", assetHandler.Patch)
		assets.DELETE("/:id", assetHandler.Delete)
	}

	generations := api.Group("/generations", middleware.Auth(secret), active, passwordGate)
	{
		generations.GET("", generationHandler.List)
		generations.GET("/:id", generationHandler.Get)
		generations.DELETE("/:id", generationHandler.Delete)
	}

	// 社区：作品复用用户素材，浏览无需额外权限，发布与互动需登录。
	community := api.Group("/community", middleware.Auth(secret), active, passwordGate)
	{
		community.GET("/works", communityHandler.List)
		community.GET("/works/mine", communityHandler.MyWorks)
		community.POST("/works", middleware.RateLimit(publishLimiter, func(c *gin.Context) string { return "publish:" + c.GetString("user_id") }), communityHandler.Publish)
		community.GET("/works/:id", communityHandler.Get)
		community.DELETE("/works/:id", communityHandler.Delete)
		community.POST("/works/:id/like", communityHandler.Like)
		community.POST("/works/:id/unlike", communityHandler.Like)
		community.POST("/works/:id/report", communityHandler.Report)
		community.GET("/users/:id", communityHandler.UserProfile)
	}

	// 运营活动：签到与邀请返利只进赠送桶。
	activity := api.Group("/activity", middleware.Auth(secret), active, passwordGate)
	{
		activity.GET("/checkin", activityHandler.CheckinStatus)
		activity.POST("/checkin", activityHandler.Checkin)
		activity.GET("/invite", activityHandler.InviteInfo)
		activity.POST("/invite/bind", activityHandler.BindInvite)
	}

	// 站点公开设置：公告、维护状态与功能开关。
	api.GET("/settings/public", adminHandler.PublicSettings)

	// AI 报价与生成：全部走平台目录与服务端托管的渠道。
	// 注销冷静期允许浏览与导出，但生成属写操作，与下单同口径拦截。
	ai := api.Group("/ai", middleware.Auth(secret), active, passwordGate, middleware.RequireNotPendingDeletion())
	{
		ai.POST("/quote", aiHandler.Quote)
		ai.POST("/images/generations", aiHandler.Images)
		ai.POST("/videos/generations", aiHandler.CreateVideo)
		ai.GET("/videos/tasks/:id", aiHandler.VideoTask)
		ai.POST("/audio/speech", aiHandler.Speech)
		ai.POST("/chat/completions", aiHandler.Chat)
	}

	// 媒体读路径额外认 ic_media cookie（GET/HEAD），写路径只认 Bearer。
	media := api.Group("/media", middleware.MediaAuth(secret, idn), active, passwordGate)
	{
		media.HEAD("/:storageKey", mediaHandler.Head)
		media.GET("/:storageKey", mediaHandler.Get)
		media.PUT("/:storageKey", mediaHandler.Put)
		media.DELETE("/:storageKey", mediaHandler.Delete)
		// 申请下载：严格归属校验后签发短期取件链接（POST 认 Bearer，与写路径同口径）。
		media.POST("/:storageKey/download",
			middleware.RateLimit(downloadLimiter, func(c *gin.Context) string { return "download:" + c.GetString("user_id") }),
			mediaHandler.RequestDownload)
	}

	// 干净原件取件：签名 URL 必须同时过 MediaAuth（ic_media cookie 或 Bearer），
	// 这是防盗链的第二道闸；签名、过期与归属校验在 handler 内完成，任一失败一律 404。
	mediaDownload := api.Group("/media-download", middleware.MediaAuth(secret, idn), active, passwordGate)
	{
		mediaDownload.GET("/:token", mediaHandler.ServeDownload)
	}

	// 点数、档位与模型目录
	billing := api.Group("", middleware.Auth(secret), active, passwordGate)
	{
		billing.GET("/credits", creditHandler.GetBalance)
		billing.GET("/credits/transactions", creditHandler.ListTransactions)
		billing.GET("/credit-packages", creditHandler.ListPackages)
		billing.GET("/plans", creditHandler.ListPlans)
		billing.GET("/models", catalogHandler.List)
	}

	// 订单：下单额外要求不在注销冷静期，并按用户限流防刷垃圾待支付订单。
	orders := api.Group("/orders", middleware.Auth(secret), active, passwordGate)
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

	// 管理后台：每请求按库里的角色判定权限（不读 JWT 里的 role claim，避免 15 分钟陈旧授权），
	// 具体权限点由 registerAdminRoutes 逐条挂载。
	admin := api.Group("/admin", middleware.Auth(secret), active, passwordGate, middleware.LoadAdminAccess(idn, gormDB))
	registerAdminRoutes(admin, adminHandler)

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
	// T08：夜间存储记账对账——三层比对（media_files 聚合 / storage_usage / storage_accounts），
	// 不平走 storage.Recalculate 以事实源重算收敛并落结构化日志（告警通道 D9 拍板后接线）。
	reconcileService := service.NewReconcileService(gormDB, storageService)
	go runScheduled(ctx, "存储记账对账", 24*time.Hour, func() {
		if checked, fixed, err := reconcileService.ReconcileStorage(ctx, time.Now()); err != nil {
			slog.Error("存储记账对账失败", "err", err)
		} else if fixed > 0 {
			slog.Warn("storage_reconcile_drift", "checked", checked, "fixed", fixed)
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
