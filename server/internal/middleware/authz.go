package middleware

import (
	"log/slog"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/platform/identity"
)

// ctxAdminAccess 是 LoadAdminAccess 写入请求上下文的权限快照。
const ctxAdminAccess = "admin_access"

// AdminAccess 是当前请求的后台权限快照：角色 + 已授予的权限集合。
type AdminAccess struct {
	RoleKey     string
	RoleName    string
	IsSystem    bool
	Permissions []string // 已排序；系统角色为全部注册权限
}

// AdminAccessFrom 读取当前请求的后台权限快照，第二个返回值表示快照是否存在。
// 快照不存在说明该路由漏挂了 LoadAdminAccess，调用方必须按拒绝处理。
func AdminAccessFrom(c *gin.Context) (AdminAccess, bool) {
	value, ok := c.Get(ctxAdminAccess)
	if !ok {
		return AdminAccess{}, false
	}
	access, ok := value.(AdminAccess)
	return access, ok
}

// Has 判断快照是否包含某个权限点。系统角色隐式拥有全部权限（短路由，不查 role_permissions）。
func (a AdminAccess) Has(key string) bool {
	if a.IsSystem {
		return true
	}
	for _, granted := range a.Permissions {
		if granted == key {
			return true
		}
	}
	return false
}

// LoadAdminAccess 是管理后台的组级中间件，必须紧跟 Auth 与 RequireActiveUser。
//
// 权限每请求从库里读取，不读 JWT 里的 role claim：access token 里的角色最长 15 分钟陈旧，
// 不能作为授权依据，降权必须立即生效。管理流量低，初期不加缓存。
//
// 失败关闭：没有后台角色、角色已不存在、或角色分配了代码注册表之外的权限点，一律拒绝/忽略。
func LoadAdminAccess(idn *identity.Service, db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := uuid.Parse(c.GetString("user_id"))
		if err != nil {
			errs.Abort(c, errs.ErrUnauthorized)
			return
		}
		user, err := idn.GetByID(c.Request.Context(), userID)
		if err != nil {
			slog.Error("读取用户角色失败", "err", err, "user_id", userID)
			errs.Abort(c, errs.ErrInternal)
			return
		}
		if user.RoleKey == nil || *user.RoleKey == "" {
			// 没有后台角色：/admin/me 之外的管理接口由 RequirePermission 再拦一层。
			errs.Abort(c, errs.ErrForbidden)
			return
		}
		roleKey := *user.RoleKey
		var rows []struct {
			Name          string
			IsSystem      bool
			PermissionKey *string
		}
		if err := db.Table("roles").
			Select("roles.name AS name, roles.is_system AS is_system, role_permissions.permission_key AS permission_key").
			Joins("LEFT JOIN role_permissions ON role_permissions.role_key = roles.role_key").
			Where("roles.role_key = ?", roleKey).
			Order("role_permissions.permission_key ASC").
			Scan(&rows).Error; err != nil {
			slog.Error("读取角色权限失败", "err", err, "role", roleKey)
			errs.Abort(c, errs.ErrInternal)
			return
		}
		if len(rows) == 0 {
			slog.Warn("用户引用了不存在的角色，已按失败关闭拒绝", "user_id", userID, "role", roleKey)
			errs.Abort(c, errs.ErrForbidden)
			return
		}
		access := AdminAccess{RoleKey: roleKey, RoleName: rows[0].Name, IsSystem: rows[0].IsSystem}
		if access.IsSystem {
			access.Permissions = authz.Keys()
		} else {
			granted := make([]string, 0, len(rows))
			seen := make(map[string]struct{}, len(rows))
			for _, row := range rows {
				if row.PermissionKey == nil {
					continue
				}
				key := *row.PermissionKey
				if !authz.IsKnown(key) {
					// typo 只会让谁都进不去，不会让谁都能进。
					slog.Warn("角色分配了代码注册表之外的权限点，已忽略", "role", roleKey, "permission", key)
					continue
				}
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				granted = append(granted, key)
			}
			sort.Strings(granted)
			access.Permissions = granted
		}
		c.Set(ctxAdminAccess, access)
		c.Next()
	}
}

// RequirePermission 是管理路由的权限中间件，只读上下文里的权限快照。
//
// 失败关闭：key 不在代码注册表、请求没有权限快照（漏挂 LoadAdminAccess）、
// 快照里没有该 key，三种情况都拒绝。响应体不回传缺失的权限点，避免把执行点当探针接口。
func RequirePermission(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !authz.IsKnown(key) {
			slog.Warn("权限点不在代码注册表中，已按失败关闭拒绝", "permission", key, "path", c.FullPath())
			errs.Abort(c, errs.ErrForbidden)
			return
		}
		access, ok := AdminAccessFrom(c)
		if !ok {
			slog.Warn("管理路由缺少 LoadAdminAccess，已按失败关闭拒绝", "permission", key, "path", c.FullPath())
			errs.Abort(c, errs.ErrForbidden)
			return
		}
		if !access.Has(key) {
			errs.Abort(c, errs.ErrForbidden)
			return
		}
		c.Next()
	}
}
