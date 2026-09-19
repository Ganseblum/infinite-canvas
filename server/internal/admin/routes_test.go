package admin

import (
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/ai"
	"github.com/infinite-canvas/server/internal/canvas"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/platform/identity"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/storage"
	"github.com/infinite-canvas/server/internal/testutil"
	"github.com/infinite-canvas/server/internal/watermark"
)

// newResourceRouterWithModeration 注册画布、素材、生成记录与媒体四组路由并注入审核服务，
// 供管理面审核链路测试使用。与 canvas/ai 包的同名夹具各自独立，避免测试包互相依赖。
func newResourceRouterWithModeration(t *testing.T, g *gorm.DB, cfg *config.Config, stor storage.Storage, moderationService *service.ModerationService) *gin.Engine {
	t.Helper()
	secret := []byte(cfg.JWTSecret)
	canvasH := canvas.NewCanvasHandler(g)
	assetH := canvas.NewAssetHandler(g)
	genH := canvas.NewGenerationHandler(g)
	mediaH := ai.NewMediaHandler(g, stor, moderationService, secret, watermark.NewService("", ""))

	r := testutil.NewRouter(t, func(r *gin.Engine) {
		api := r.Group("/api/v1")
		canvases := api.Group("/canvases", middleware.Auth(secret))
		canvas.MountCanvasRoutes(canvases, canvasH)

		assets := api.Group("/assets", middleware.Auth(secret))
		canvas.MountAssetRoutes(assets, assetH)

		generations := api.Group("/generations", middleware.Auth(secret))
		canvas.MountGenerationRoutes(generations, genH)

		media := api.Group("/media", middleware.MediaAuth(secret, identity.NewService(g)))
		ai.MountMediaRoutes(media, mediaH)
		media.POST("/:storageKey/download", mediaH.RequestDownload)

		download := api.Group("/media-download", middleware.MediaAuth(secret, identity.NewService(g)))
		ai.MountMediaDownloadRoutes(download, mediaH)
	})
	return r
}
