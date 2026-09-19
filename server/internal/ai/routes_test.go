package ai

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/account"
	"github.com/infinite-canvas/server/internal/canvas"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/platform/identity"
	"github.com/infinite-canvas/server/internal/storage"
	"github.com/infinite-canvas/server/internal/testutil"
	"github.com/infinite-canvas/server/internal/watermark"
)

// registerUser 保持域内测试的旧签名，refresh cookie 名取自 account 包契约常量。
func registerUser(t *testing.T, r http.Handler, email, username, password string) (*httptest.ResponseRecorder, testutil.SessionResp, *http.Cookie) {
	return testutil.RegisterUser(t, r, email, username, password, account.RefreshCookieName)
}

// newAuthRouter 组装认证路由的测试引擎，路由表与生产共用 MountAuthRoutes。
func newAuthRouter(t *testing.T, cfg *config.Config, h *account.AuthHandler) *gin.Engine {
	return testutil.NewRouter(t, func(r *gin.Engine) {
		account.MountAuthRoutes(r.Group("/api/v1/auth"), h, []byte(cfg.JWTSecret))
	})
}

// newResourceRouter 注册画布、素材、生成记录与媒体四组路由，媒体驱动可注入。
// 与 canvas/admin 包的同名夹具各自独立，避免测试包互相依赖。
func newResourceRouter(t *testing.T, g *gorm.DB, cfg *config.Config, stor storage.Storage) *gin.Engine {
	t.Helper()
	secret := []byte(cfg.JWTSecret)
	canvasH := canvas.NewCanvasHandler(g)
	assetH := canvas.NewAssetHandler(g)
	genH := canvas.NewGenerationHandler(g)
	mediaH := NewMediaHandler(g, stor, nil, secret, watermark.NewService("", ""))

	r := testutil.NewRouter(t, func(r *gin.Engine) {
		api := r.Group("/api/v1")
		canvases := api.Group("/canvases", middleware.Auth(secret))
		canvas.MountCanvasRoutes(canvases, canvasH)

		assets := api.Group("/assets", middleware.Auth(secret))
		canvas.MountAssetRoutes(assets, assetH)

		generations := api.Group("/generations", middleware.Auth(secret))
		canvas.MountGenerationRoutes(generations, genH)

		media := api.Group("/media", middleware.MediaAuth(secret, identity.NewService(g)))
		MountMediaRoutes(media, mediaH)
		media.POST("/:storageKey/download", mediaH.RequestDownload)

		download := api.Group("/media-download", middleware.MediaAuth(secret, identity.NewService(g)))
		MountMediaDownloadRoutes(download, mediaH)
	})
	return r
}
