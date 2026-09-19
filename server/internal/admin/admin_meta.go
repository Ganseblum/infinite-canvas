package admin

import (
	"github.com/gin-gonic/gin"

	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/db"
	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/middleware"
)

// AdminMeta 返回产品标识与按调用者权限过滤的模块清单。
// 这是多产品共用后台的引导接口：任何持有后台角色的账号都可调（与 /admin/me 同级，
// 不要求具体权限点），前端据此渲染产品名、版本与可用模块。
func (h *AdminHandler) AdminMeta(c *gin.Context) {
	access, ok := middleware.AdminAccessFrom(c)
	if !ok {
		// 组级中间件漏挂 LoadAdminAccess 时按拒绝处理，与权限中间件同口径。
		errs.Abort(c, errs.ErrForbidden)
		return
	}
	c.JSON(200, gin.H{
		"product": gin.H{
			"id":      "youc-canvas",
			"name":    "优刻画布",
			"version": db.CurrentVersion(),
		},
		"modules": authz.ModulesFor(access.Permissions),
	})
}
