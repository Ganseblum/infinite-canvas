package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/account"
	"github.com/infinite-canvas/server/internal/admin"
	"github.com/infinite-canvas/server/internal/ai"
	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/billing"
	"github.com/infinite-canvas/server/internal/blog"
	"github.com/infinite-canvas/server/internal/canvas"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/crypto"
	"github.com/infinite-canvas/server/internal/db"
	"github.com/infinite-canvas/server/internal/envload"
	"github.com/infinite-canvas/server/internal/mail"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/moderation"
	"github.com/infinite-canvas/server/internal/office"
	platformbilling "github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/platform/identity"
	platformstorage "github.com/infinite-canvas/server/internal/platform/storage"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/storage"
	"github.com/infinite-canvas/server/internal/watermark"
)

// main 是装配入口：先初始化基础设施（日志、数据库、存储），再按依赖顺序构建
// 平台层与各产品域的处理器，最后注册路由并启动 HTTP 服务；任何一步失败即退出。
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

	// 日志统一 JSON 输出到 stdout，级别取 LOG_LEVEL（非法值已在 config.Load 拦下）
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
	// 第六期 M1：office 域五张表的迁移由域包自带（db 包 import 产品域会违反依赖方向）。
	if err := office.Migrate(gormDB); err != nil {
		slog.Error("office AutoMigrate 失败", "err", err)
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

	// 邮件服务：smtp 直发或 log 打印（MAIL_DRIVER），验证码与告警等出站邮件统一走它
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

	// 站点设置单例：启动时读库，admin / 社区 / 活动域共享同一实例
	siteSettings := service.NewSiteSettingService(gormDB)
	if err := siteSettings.Load(context.Background()); err != nil {
		slog.Error("读取站点设置失败", "err", err)
		os.Exit(1)
	}
	// 平台身份域：认证、授权与管理端的身份读写统一入口。
	idn := identity.NewService(gormDB)
	// 处理器装配模式：构造函数只收核心依赖，可选依赖（设置、审核、水印等）用 Set* 后置注入
	authHandler := account.NewAuthHandler(gormDB, cfg, mailer)
	authHandler.SetSettings(siteSettings)
	grantService := service.NewFreeGrantService(gormDB)
	accountHandler := account.NewAccountHandler(gormDB, cfg, grantService, authHandler)
	canvasHandler := canvas.NewCanvasHandler(gormDB)
	assetHandler := canvas.NewAssetHandler(gormDB)
	generationHandler := canvas.NewGenerationHandler(gormDB)

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
	mediaHandler := ai.NewMediaHandler(gormDB, mediaStorage, moderationService, []byte(cfg.JWTSecret), wmService)
	// 支付渠道注册表：下单与回调验签共用同一份渠道配置，配置无效启动即退出
	paymentRegistry, err := service.NewPaymentRegistry(cfg)
	if err != nil {
		slog.Error("支付渠道配置无效", "err", err)
		os.Exit(1)
	}
	creditHandler := billing.NewCreditHandler(gormDB, paymentRegistry)
	orderHandler := billing.NewOrderHandler(gormDB, paymentRegistry)
	paymentHandler := billing.NewPaymentHandler(gormDB, paymentRegistry)
	catalogHandler := ai.NewModelHandler(gormDB, func() bool { return cfg.PromotionEnabled })

	// 上游超时集合：默认值可被 AI_*_TIMEOUT 系列环境变量逐项覆盖
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
	billingService := platformbilling.NewService(gormDB, model.ProductCanvas)
	storageService := platformstorage.NewService(gormDB, model.ProductCanvas)
	quoteService := service.NewQuoteService(catalogService, billingService, cfg.JWTSecret)
	aiHandler := ai.NewAIHandler(gormDB, catalogService, quoteService, upstreamService, mediaStorage, cfg.AppBaseURL, moderationService)
	aiTaskService := service.NewAITaskService(gormDB, upstreamService, service.NewMediaWriteService(gormDB, mediaStorage))
	aiTaskService.SetModeration(moderationService)
	// T5 生成落盘水印挂钩：WATERMARK_ENABLED 只控制生成时是否烧录（下发闸门与下载
	// 端点永远在线，不随 flag 下线）。开关关闭时挂钩不生效，生成链路行为与现状一致。
	aiHandler.SetWatermark(wmService, watermark.Enabled)
	aiTaskService.SetWatermark(wmService, watermark.Enabled)
	requestService := service.NewAIRequestService(gormDB)

	adminHandler := admin.NewAdminHandlerWithUpstream(gormDB, cfg, mediaStorage, upstreamService)
	adminHandler.SetModeration(moderationService)
	adminHandler.SetSettings(siteSettings)
	// 人工释放隔离件时按物主档位烧录水印（评审 E-4），与生成链路共用同一服务与开关。
	adminHandler.SetWatermark(wmService, watermark.Enabled)
	communityHandler := canvas.NewCommunityHandler(gormDB, siteSettings)
	activityHandler := canvas.NewActivityHandler(gormDB, siteSettings, grantService, cfg)

	// 第六期 M1：AI 办公助理编排域。权限点 office.read/write 的接入是 M3 任务，
	// 当前契约一只挂登录中间件；点数读写复用同一 platform/billing 服务实例。
	officeService := office.NewService(gormDB, billingService, office.RuntimeConfig{
		BaseURL: cfg.OfficeAgentURL,
		Token:   cfg.OfficeInternalToken,
	})
	officeHandler := office.NewOfficeHandler(officeService)

	// 博客产品域：公共内容读与互动在 /api/v1/blog，管理面经 admin 包注册（blog.read/write）。
	blogRevalidator := blog.NewRevalidator(cfg.BlogRevalidateURL, cfg.BlogRevalidateSecret)
	blogPublic := blog.NewPublicHandler(gormDB)
	if moderationService.Enabled() {
		blogPublic.SetTextChecker(func(ctx context.Context, userID uuid.UUID, text string) (bool, error) {
			verdict, err := moderationService.Check(ctx, userID, moderation.StagePrompt, moderation.ContentText, text, nil, "")
			if err != nil {
				return false, err
			}
			return verdict.Decision == moderation.DecisionPassed, nil
		})
	}
	blogAdmin := blog.NewAdminHandler(gormDB, blogRevalidator)
	adminHandler.SetBlog(blogAdmin)

	// M3 OIDC Provider（PLAN T09）：签名密钥从 OIDC_JWKS_PRIVATE_KEY（PEM PKCS#8）读取，
	// 配置了但解析失败直接退出（fail-fast）；未配置时生成临时密钥并告警——重启后旧 token 失效，
	// 生产环境必须显式配置。
	oidcHandler, err := account.NewOIDCHandler(gormDB, cfg, idn)
	if err != nil {
		slog.Error("初始化 OIDC Provider 失败", "err", err)
		os.Exit(1)
	}

	// gin.New 不带任何默认中间件：RequestID/日志/Recovery/CORS 全部显式挂载，注册顺序即执行顺序。
	router := gin.New()
	router.Use(middleware.RequestID(), middleware.Logger(level.Level()), middleware.Recovery())
	// 管理后台独立部署时跨源直连 API；CORS_ALLOWED_ORIGINS 为空则完全不启用。
	// 必须挂在引擎级：group handlers 在路由注册时固化，挂在 /api/admin 会漏掉 /api/v1/auth/login 等路径。
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
	// 就绪探针：真正 ping 一次数据库，连接池耗尽或库不可达时返回 503
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

	// 平台会话凭据：HS256，/api/v1 与 /api/admin 的 middleware.Auth 共用这一份；
	// OIDC 签发验签走独立 RS256 密钥，两套凭据互不通用（见下方 /api/v1/oidc 注释）。
	secret := []byte(cfg.JWTSecret)

	// 限流器（认证域的三个限流器随 account.MountAuthRoutes 注册）
	orderLimiter := middleware.NewLimiter(time.Hour, 10)
	// 社区发布限流：与 handler 内的每日上限双保险（差异清单 #38）。
	publishLimiter := middleware.NewLimiter(time.Hour, 12)
	// 申请下载限流：签发端无状态，按用户限流防刷短时效链接（60 次/小时）。
	downloadLimiter := middleware.NewLimiter(time.Hour, 60)
	// OIDC 授权端点限流：authorize/token 对齐登录端点口径（20 次/10 分钟/IP），
	// token 无会话只能按 IP 键；authorize 浏览器直跳场景同样按 IP 拦截刷码。
	oidcAuthLimiter := middleware.NewLimiter(10*time.Minute, 20)
	oidcTokenLimiter := middleware.NewLimiter(10*time.Minute, 20)
	// 反馈工单提交限流：10 次/小时/用户，与社区发布同量级，防刷工单队列。
	feedbackLimiter := middleware.NewLimiter(time.Hour, 10)

	api := router.Group("/api/v1")

	// 维护模式写拦截（差异清单 #117）：开启后非管理员的写请求 503，
	// 读操作、认证、管理端与支付回调放行。
	api.Use(middleware.MaintenanceGate(siteSettings, idn, secret))

	// 强制改密：must_change_password 的账号只能登出、刷新或改密，其余接口一律 403。
	// 挂到每个需要登录的路由上，新增受保护分组必须一并挂上。
	passwordGate := middleware.RequirePasswordChanged(idn)

	// 认证八端点的路由表在 account 包内维护，main 与测试夹具共用。
	account.MountAuthRoutes(api.Group("/auth"), authHandler, secret)

	// 全站业务接口要求登录且账号未封禁；注销冷静期的账号可以浏览，但生成与下单被拦截。
	active := middleware.RequireActiveUser(idn)

	// OIDC Provider 四端点（PLAN T09）：authorize 要求登录（Auth + RequireActiveUser），
	// token/jwks 公开；userinfo 在 handler 内验 OIDC 签发的 RS256 access token，
	// 与平台 HS256 会话是两套凭据，不走 middleware.Auth。/api/v1/oidc/ 也在维护模式豁免前缀内
	// （token 是 POST，不能被维护模式拦断）。T09 不含 admin 管理端点（T10 再挂 sso.read/write）。
	oidc := api.Group("/oidc")
	oidc.GET("/authorize", middleware.RateLimit(oidcAuthLimiter, func(c *gin.Context) string { return "oidc-auth:" + middleware.ClientIP(c) }), middleware.Auth(secret), active, oidcHandler.Authorize)
	oidc.POST("/token", middleware.RateLimit(oidcTokenLimiter, func(c *gin.Context) string { return "oidc-token:" + middleware.ClientIP(c) }), oidcHandler.Token)
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
	canvas.MountCanvasRoutes(canvases, canvasHandler)

	assets := api.Group("/assets", middleware.Auth(secret), active, passwordGate)
	canvas.MountAssetRoutes(assets, assetHandler)

	generations := api.Group("/generations", middleware.Auth(secret), active, passwordGate)
	canvas.MountGenerationRoutes(generations, generationHandler)

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

	// 反馈工单：登录用户提交与查看自己的工单；处理端在 /api/admin/feedback。
	feedbackHandler := canvas.NewFeedbackHandler(gormDB)
	feedback := api.Group("/feedback", middleware.Auth(secret), active, passwordGate)
	{
		feedback.POST("", middleware.RateLimit(feedbackLimiter, func(c *gin.Context) string { return "feedback:" + c.GetString("user_id") }), feedbackHandler.Create)
		feedback.GET("", feedbackHandler.List)
		feedback.GET("/:id", feedbackHandler.Get)
		feedback.POST("/:id/replies", feedbackHandler.Reply)
		feedback.POST("/:id/close", feedbackHandler.Close)
	}

	// 博客：内容读与评论列表公开（前台 Next 服务端内网拉取 + 游客），
	// 发评/点赞/收藏需登录。发评限流 2 次/分钟 ≈ 30 秒一条（口径待确认）。
	commentLimiter := middleware.NewLimiter(time.Minute, 2)
	blogPublic.SetCommentLimiter(commentLimiter)
	blog.MountContentRoutes(api.Group("/blog"), blogPublic)
	blog.MountInteractionRoutes(api.Group("/blog", middleware.Auth(secret), active, passwordGate), blogPublic)

	// AI 办公助理：契约一 A1–A9 挂 /api/v1/office，登录即可用（权限点接入是 M3）。
	office.MountOfficeRoutes(api.Group("/office", middleware.Auth(secret), active, passwordGate), officeHandler)

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
	aiGroup := api.Group("/ai", middleware.Auth(secret), active, passwordGate, middleware.RequireNotPendingDeletion())
	{
		aiGroup.POST("/quote", aiHandler.Quote)
		aiGroup.POST("/images/generations", aiHandler.Images)
		aiGroup.POST("/videos/generations", aiHandler.CreateVideo)
		aiGroup.GET("/videos/tasks/:id", aiHandler.VideoTask)
		aiGroup.POST("/audio/speech", aiHandler.Speech)
		aiGroup.POST("/chat/completions", aiHandler.Chat)
	}

	// 媒体读路径额外认 ic_media cookie（GET/HEAD），写路径只认 Bearer。
	media := api.Group("/media", middleware.MediaAuth(secret, idn), active, passwordGate)
	ai.MountMediaRoutes(media, mediaHandler)
	// 申请下载：严格归属校验后签发短期取件链接（POST 认 Bearer，与写路径同口径）。
	media.POST("/:storageKey/download",
		middleware.RateLimit(downloadLimiter, func(c *gin.Context) string { return "download:" + c.GetString("user_id") }),
		mediaHandler.RequestDownload)

	// 干净原件取件：签名 URL 必须同时过 MediaAuth（ic_media cookie 或 Bearer），
	// 这是防盗链的第二道闸；签名、过期与归属校验在 handler 内完成，任一失败一律 404。
	mediaDownload := api.Group("/media-download", middleware.MediaAuth(secret, idn), active, passwordGate)
	ai.MountMediaDownloadRoutes(mediaDownload, mediaHandler)

	// 点数、档位与模型目录
	billingGroup := api.Group("", middleware.Auth(secret), active, passwordGate)
	{
		billingGroup.GET("/credits", creditHandler.GetBalance)
		billingGroup.GET("/credits/transactions", creditHandler.ListTransactions)
		billingGroup.GET("/credit-packages", creditHandler.ListPackages)
		billingGroup.GET("/plans", creditHandler.ListPlans)
		billingGroup.GET("/models", catalogHandler.List)
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

	// 管理后台：每请求按库里的角色判定权限（不读 JWT 里的 role claim，避免 15 分钟陈旧授权）。
	// 管理面挂顶层 /api/admin，不进 /api/v1 公开版本契约；路由表在 admin 包内维护。
	adminGroup := router.Group("/api/admin", middleware.Auth(secret), active, passwordGate, middleware.LoadAdminAccess(idn, gormDB))
	admin.RegisterRoutes(adminGroup, adminHandler)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	// SIGINT/SIGTERM 触发 ctx 取消：后台定时任务随之停止，主流程进入优雅关闭。
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
	// 第六期 E13：office 孤儿 run 回收扫描（启动即跑一次，之后每 60s）。
	go officeService.StartOrphanScan(ctx)
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
	// 不平走 storage.Recalculate 以事实源重算收敛并落结构化日志；发现漂移同时邮件告警全部
	// admin 角色账号（D9），发送走异步 Mailer，失败仅记日志不阻塞对账。
	reconcileService := service.NewReconcileService(gormDB, storageService)
	go runScheduled(ctx, "存储记账对账", 24*time.Hour, func() {
		if checked, fixed, err := reconcileService.ReconcileStorage(ctx, time.Now()); err != nil {
			slog.Error("存储记账对账失败", "err", err)
		} else if fixed > 0 {
			slog.Warn("storage_reconcile_drift", "checked", checked, "fixed", fixed)
			notifyAdmins(gormDB, mailer, "存储记账对账发现漂移并已自动收敛",
				fmt.Sprintf("夜间对账检查了 %d 个账号，发现并已按事实源自动收敛 %d 处存储记账漂移。\n逐账号明细见 api 容器日志中的 storage_reconcile_drift 记录。\n", checked, fixed))
		}
	})

	go func() {
		slog.Info("后端服务启动", "port", cfg.Port, "mail_driver", cfg.MailDriver)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP 服务异常退出", "err", err)
			stop()
		}
	}()

	// 阻塞等待退出信号，最多再给在途请求 10 秒排空
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

// notifyAdmins 向全部 admin 角色账号邮箱投递告警邮件（D9 对账告警通道）。
// 没有管理员或发送失败都不影响主流程；Mailer 自身异步且失败仅记日志。
func notifyAdmins(db *gorm.DB, mailer *mail.Mailer, subject, body string) {
	if mailer == nil {
		return
	}
	var emails []string
	if err := db.Model(&model.PlatformUser{}).Where("role_key = ?", authz.SystemRoleKey).Order("created_at").Pluck("email", &emails).Error; err != nil {
		slog.Error("查询告警管理员邮箱失败", "err", err)
		return
	}
	for _, to := range emails {
		mailer.Send(mail.Message{To: to, Subject: subject, Body: body})
	}
}
