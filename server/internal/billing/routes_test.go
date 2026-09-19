package billing

import (
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/infinite-canvas/server/internal/account"
	"github.com/infinite-canvas/server/internal/config"
	"github.com/infinite-canvas/server/internal/testutil"
)

// newAuthRouter 组装认证路由的测试引擎，路由表与生产共用 MountAuthRoutes。
func newAuthRouter(t *testing.T, cfg *config.Config, h *account.AuthHandler) *gin.Engine {
	return testutil.NewRouter(t, func(r *gin.Engine) {
		account.MountAuthRoutes(r.Group("/api/v1/auth"), h, []byte(cfg.JWTSecret))
	})
}
