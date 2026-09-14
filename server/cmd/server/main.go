package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/db"
	"github.com/infinite-canvas/server/internal/handler"
	"github.com/infinite-canvas/server/internal/mail"
	"github.com/infinite-canvas/server/internal/middleware"
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

	authHandler := handler.NewAuthHandler(gormDB, cfg, mailer)
	grantService := service.NewFreeGrantService(gormDB)
	accountHandler := handler.NewAccountHandler(gormDB, cfg, grantService, authHandler)
	canvasHandler := handler.NewCanvasHandler(gormDB)
	assetHandler := handler.NewAssetHandler(gormDB)
	generationHandler := handler.NewGenerationHandler(gormDB)
	mediaHandler := handler.NewMediaHandler(gormDB, mediaStorage)

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

	me := api.Group("/me", middleware.Auth(secret))
	{
		me.GET("", accountHandler.GetMe)
		me.PATCH("", accountHandler.UpdateMe)
		me.POST("/password", accountHandler.ChangePassword)
		me.POST("/free-grant/claim", accountHandler.ClaimFreeGrant)
	}

	canvases := api.Group("/canvases", middleware.Auth(secret))
	{
		canvases.GET("", canvasHandler.List)
		canvases.POST("", canvasHandler.Create)
		canvases.GET("/:id", canvasHandler.Get)
		canvases.PUT("/:id", canvasHandler.Update)
		canvases.PATCH("/:id", canvasHandler.Patch)
		canvases.DELETE("/:id", canvasHandler.Delete)
	}

	assets := api.Group("/assets", middleware.Auth(secret))
	{
		assets.GET("", assetHandler.List)
		assets.POST("", assetHandler.Create)
		assets.GET("/:id", assetHandler.Get)
		assets.PATCH("/:id", assetHandler.Patch)
		assets.DELETE("/:id", assetHandler.Delete)
	}

	generations := api.Group("/generations", middleware.Auth(secret))
	{
		generations.GET("", generationHandler.List)
		generations.GET("/:id", generationHandler.Get)
		generations.DELETE("/:id", generationHandler.Delete)
	}

	// 媒体读路径额外认 ic_media cookie（GET/HEAD），写路径只认 Bearer。
	media := api.Group("/media", middleware.MediaAuth(secret))
	{
		media.HEAD("/:storageKey", mediaHandler.Head)
		media.GET("/:storageKey", mediaHandler.Get)
		media.PUT("/:storageKey", mediaHandler.Put)
		media.DELETE("/:storageKey", mediaHandler.Delete)
	}

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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
