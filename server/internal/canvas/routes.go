package canvas

import "github.com/gin-gonic/gin"

// MountCanvasRoutes 注册画布 CRUD 六条路由，鉴权等中间件由调用方挂在分组上。
func MountCanvasRoutes(g *gin.RouterGroup, h *CanvasHandler) {
	g.GET("", h.List)
	g.POST("", h.Create)
	g.GET("/:id", h.Get)
	g.PUT("/:id", h.Update)
	g.PATCH("/:id", h.Patch)
	g.DELETE("/:id", h.Delete)
}

// MountAssetRoutes 注册素材五条路由，鉴权等中间件由调用方挂在分组上。
func MountAssetRoutes(g *gin.RouterGroup, h *AssetHandler) {
	g.GET("", h.List)
	g.POST("", h.Create)
	g.GET("/:id", h.Get)
	g.PATCH("/:id", h.Patch)
	g.DELETE("/:id", h.Delete)
}

// MountGenerationRoutes 注册生成记录三条路由与生成反馈三条路由，鉴权等中间件由调用方挂在分组上。
func MountGenerationRoutes(g *gin.RouterGroup, h *GenerationHandler) {
	g.GET("", h.List)
	g.GET("/:id", h.Get)
	g.DELETE("/:id", h.Delete)
	// 生成结果点赞点踩：同一 (用户, 生成记录) 一条反馈，重复提交覆盖。
	g.PUT("/:id/feedback", h.SetGenerationFeedback)
	g.GET("/:id/feedback", h.GetGenerationFeedback)
	g.DELETE("/:id/feedback", h.DeleteGenerationFeedback)
}
