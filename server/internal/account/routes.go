package account

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/infinite-canvas/server/internal/middleware"
)

// MountAuthRoutes 注册认证八端点，生产 main.go 与测试夹具共用同一张路由表。
// 限流器属于认证域的防刷策略，随路由一并注册；分组级中间件由调用方挂。
func MountAuthRoutes(g *gin.RouterGroup, h *AuthHandler, secret []byte) {
	regLim := middleware.NewLimiter(time.Hour, 10)
	loginLim := middleware.NewLimiter(10*time.Minute, 20)
	mailLim := middleware.NewLimiter(time.Hour, 10)
	ipKey := func(prefix string) func(*gin.Context) string {
		return func(c *gin.Context) string { return prefix + middleware.ClientIP(c) }
	}
	g.POST("/register", middleware.RateLimit(regLim, ipKey("reg:")), h.Register)
	g.POST("/login", middleware.RateLimit(loginLim, ipKey("login:")), h.Login)
	g.POST("/refresh", h.Refresh)
	g.POST("/logout", h.Logout)
	g.POST("/verify-email/send", middleware.Auth(secret), middleware.RateLimit(mailLim, ipKey("mail:")), h.VerifyEmailSend)
	g.POST("/verify-email", h.VerifyEmail)
	g.POST("/password/forgot", middleware.RateLimit(mailLim, ipKey("mail:")), h.ForgotPassword)
	g.POST("/password/reset", h.ResetPassword)
}
