package ai

import "github.com/gin-gonic/gin"

// MountMediaRoutes 注册媒体读写四条路由，MediaAuth 等中间件由调用方挂在分组上。
// 申请下载 POST /:storageKey/download 带独立限流，由 main.go 单独挂。
func MountMediaRoutes(g *gin.RouterGroup, h *MediaHandler) {
	g.HEAD("/:storageKey", h.Head)
	g.GET("/:storageKey", h.Get)
	g.PUT("/:storageKey", h.Put)
	g.DELETE("/:storageKey", h.Delete)
}

// MountMediaDownloadRoutes 注册干净原件取件路由，中间件由调用方挂在分组上。
func MountMediaDownloadRoutes(g *gin.RouterGroup, h *MediaHandler) {
	g.GET("/:token", h.ServeDownload)
}
