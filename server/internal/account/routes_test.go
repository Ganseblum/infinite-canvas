package account

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/service"
	"github.com/infinite-canvas/server/internal/testutil"
)

// registerUser 保持域内测试的旧签名，refresh cookie 名由本包契约常量补齐。
func registerUser(t *testing.T, r http.Handler, email, username, password string) (*httptest.ResponseRecorder, testutil.SessionResp, *http.Cookie) {
	return testutil.RegisterUser(t, r, email, username, password, RefreshCookieName)
}

// newAuthRouter 组装认证路由的测试引擎，路由表与生产共用 MountAuthRoutes。
func newAuthRouter(t *testing.T, cfg *config.Config, h *AuthHandler) *gin.Engine {
	return testutil.NewRouter(t, func(r *gin.Engine) {
		MountAuthRoutes(r.Group("/api/v1/auth"), h, []byte(cfg.JWTSecret))
	})
}

// newAccountRouter 组装免费领取测试路由：只挂 /api/me/free-grant/claim。
func newAccountRouter(t *testing.T, g *gorm.DB, cfg *config.Config) *gin.Engine {
	return testutil.NewRouter(t, func(r *gin.Engine) {
		acct := NewAccountHandler(g, cfg, service.NewFreeGrantService(g), NewAuthHandler(g, cfg, testutil.TestMailer()))
		me := r.Group("/api/v1/me", middleware.Auth([]byte(cfg.JWTSecret)))
		me.POST("/free-grant/claim", acct.ClaimFreeGrant)
	})
}
