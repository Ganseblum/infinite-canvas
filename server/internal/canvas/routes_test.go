package canvas

import (
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/ai"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/platform/identity"
	"github.com/infinite-canvas/server/internal/storage"
	"github.com/infinite-canvas/server/internal/testutil"
	"github.com/infinite-canvas/server/internal/watermark"
)

// newResourceRouter 注册画布、素材、生成记录与媒体四组路由，媒体驱动可注入。
// 路由表与生产共用各域的 Mount 函数；本包与 ai/admin 的同名夹具各自独立，避免测试包互相依赖。
func newResourceRouter(t *testing.T, g *gorm.DB, cfg *config.Config, stor storage.Storage) *gin.Engine {
	t.Helper()
	secret := []byte(cfg.JWTSecret)
	canvasH := NewCanvasHandler(g)
	assetH := NewAssetHandler(g)
	genH := NewGenerationHandler(g)
	mediaH := ai.NewMediaHandler(g, stor, nil, secret, watermark.NewService("", ""))

	r := testutil.NewRouter(t, func(r *gin.Engine) {
		api := r.Group("/api/v1")
		canvases := api.Group("/canvases", middleware.Auth(secret))
		MountCanvasRoutes(canvases, canvasH)

		assets := api.Group("/assets", middleware.Auth(secret))
		MountAssetRoutes(assets, assetH)

		generations := api.Group("/generations", middleware.Auth(secret))
		MountGenerationRoutes(generations, genH)

		media := api.Group("/media", middleware.MediaAuth(secret, identity.NewService(g)))
		ai.MountMediaRoutes(media, mediaH)
		media.POST("/:storageKey/download", mediaH.RequestDownload)

		download := api.Group("/media-download", middleware.MediaAuth(secret, identity.NewService(g)))
		ai.MountMediaDownloadRoutes(download, mediaH)
	})
	return r
}
