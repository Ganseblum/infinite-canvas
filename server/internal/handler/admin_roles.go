package handler

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/middleware"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/service"
)

// errLastSystemMember 表示操作会让系统角色失去最后一个 active 用户，属于防锁死拦截。
var errLastSystemMember = errors.New("系统角色必须保留至少一个 active 用户")

// roleKeyPattern 约束角色标识：小写字母开头，只含小写字母、数字、点、下划线与连字符。
var roleKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{1,63}$`)

// Me 返回当前后台用户的角色与权限集合，前端据此渲染菜单。
// 这是唯一不要求权限点的管理路由，但必须已挂 LoadAdminAccess（也就是必须有后台角色）。
func (h *AdminHandler) Me(c *gin.Context) {
	access, ok := middleware.AdminAccessFrom(c)
	if !ok {
		errs.Abort(c, errs.ErrForbidden)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"role": gin.H{
			"key":      access.RoleKey,
			"name":     access.RoleName,
			"isSystem": access.IsSystem,
		},
		"permissions": access.Permissions,
	})
}

// ListRoles 返回全部角色及其成员数、已分配权限。系统角色的权限按隐式全量返回。
func (h *AdminHandler) ListRoles(c *gin.Context) {
	var roles []model.Role
	if err := h.db.Order("is_system DESC").Order("role_key ASC").Find(&roles).Error; err != nil {
		slog.Error("读取角色列表失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	var memberRows []struct {
		RoleKey string
		Total   int64
	}
	if err := h.db.Model(&model.User{}).
		Select("role_key, COUNT(*) AS total").
		Where("role_key IS NOT NULL").
		Group("role_key").Scan(&memberRows).Error; err != nil {
		slog.Error("统计角色成员失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	members := make(map[string]int64, len(memberRows))
	for _, row := range memberRows {
		members[row.RoleKey] = row.Total
	}
	var permissionRows []struct {
		RoleKey       string
		PermissionKey string
	}
	if err := h.db.Table("role_permissions").
		Select("role_key, permission_key").
		Order("permission_key ASC").Scan(&permissionRows).Error; err != nil {
		slog.Error("读取角色权限失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	assigned := make(map[string][]string, len(roles))
	for _, row := range permissionRows {
		if !authz.IsKnown(row.PermissionKey) {
			continue
		}
		assigned[row.RoleKey] = append(assigned[row.RoleKey], row.PermissionKey)
	}
	items := make([]gin.H, 0, len(roles))
	for _, role := range roles {
		permissions := assigned[role.Key]
		if role.IsSystem {
			permissions = authz.Keys()
		}
		if permissions == nil {
			permissions = []string{}
		}
		items = append(items, rolePayload(role, permissions, members[role.Key]))
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// ListPermissions 返回代码注册表里的权限点清单，按模块分组，供角色编辑界面渲染。
// 权限点本身不能在界面上增删改，界面只能配置「角色 ↔ 权限」的分配。
func (h *AdminHandler) ListPermissions(c *gin.Context) {
	type permissionGroup struct {
		Module      string  `json:"module"`
		ModuleName  string  `json:"moduleName"`
		Permissions []gin.H `json:"permissions"`
	}
	groups := make([]permissionGroup, 0, 8)
	index := map[string]int{}
	for _, def := range authz.Permissions() {
		idx, ok := index[def.Module]
		if !ok {
			groups = append(groups, permissionGroup{
				Module:      def.Module,
				ModuleName:  authz.ModuleLabel(def.Module),
				Permissions: []gin.H{},
			})
			idx = len(groups) - 1
			index[def.Module] = idx
		}
		groups[idx].Permissions = append(groups[idx].Permissions, gin.H{
			"key":         def.Key,
			"name":        def.Name,
			"description": def.Description,
			"sort":        def.Sort,
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": groups})
}

type roleCreateReq struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// CreateRole 新建自定义角色。系统角色不通过接口创建，is_system 永远为 false。
func (h *AdminHandler) CreateRole(c *gin.Context) {
	var req roleCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	if fields := validateRoleFields(req.Key, req.Name, req.Description); len(fields) > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, fields))
		return
	}
	role := model.Role{Key: req.Key, Name: req.Name, Description: req.Description}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&role).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "role.create", "role", role.Key, c.GetString("request_id"), "",
			nil, roleAuditSummary(role, []string{}))
	})
	if err != nil {
		if service.IsDuplicateKey(err) {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"key": "角色标识已存在"}))
			return
		}
		slog.Error("创建角色失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusCreated, rolePayload(role, []string{}, 0))
}

type roleUpdateReq struct {
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	Permissions *[]string `json:"permissions"`
}

// UpdateRole 修改角色名与说明，并可按全量替换的方式重分配权限。
// 系统角色只允许改显示名与说明，权限不可编辑。
func (h *AdminHandler) UpdateRole(c *gin.Context) {
	roleKey := strings.TrimSpace(c.Param("key"))
	var req roleUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	fields := map[string]string{}
	var name *string
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" || len([]rune(trimmed)) > 64 {
			fields["name"] = "角色名需为 1-64 个字符"
		}
		name = &trimmed
	}
	var description *string
	if req.Description != nil {
		trimmed := strings.TrimSpace(*req.Description)
		if len([]rune(trimmed)) > 200 {
			fields["description"] = "角色说明不能超过 200 个字符"
		}
		description = &trimmed
	}
	var permissions []string
	if req.Permissions != nil {
		granted, unknown := normalizePermissionKeys(*req.Permissions)
		if len(unknown) > 0 {
			fields["permissions"] = "存在未注册的权限点: " + strings.Join(unknown, ", ")
		}
		permissions = granted
	}
	if len(fields) > 0 {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, fields))
		return
	}
	var role model.Role
	if err := h.db.First(&role, "role_key = ?", roleKey).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if role.IsSystem && permissions != nil {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{
			"permissions": "系统角色隐式拥有全部权限，不可编辑权限",
		}))
		return
	}
	current, err := loadRolePermissions(h.db, role)
	if err != nil {
		slog.Error("读取角色权限失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	updated := role
	updates := map[string]any{}
	if name != nil && *name != role.Name {
		updates["name"] = *name
		updated.Name = *name
	}
	if description != nil && *description != role.Description {
		updates["description"] = *description
		updated.Description = *description
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			if err := tx.Model(&model.Role{}).Where("role_key = ?", role.Key).Updates(updates).Error; err != nil {
				return err
			}
			if err := h.audit.Record(tx, actorID, "role.update", "role", role.Key, c.GetString("request_id"), "",
				roleAuditSummary(role, current), roleAuditSummary(updated, current)); err != nil {
				return err
			}
		}
		if permissions != nil && !samePermissionSet(current, permissions) {
			if err := replaceRolePermissions(tx, role.Key, permissions); err != nil {
				return err
			}
			if err := h.audit.Record(tx, actorID, "role.permissions", "role", role.Key, c.GetString("request_id"), "",
				roleAuditSummary(role, current), roleAuditSummary(updated, permissions)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("更新角色失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	final := current
	if permissions != nil {
		final = permissions
	}
	var members int64
	h.db.Model(&model.User{}).Where("role_key = ?", role.Key).Count(&members)
	c.JSON(http.StatusOK, rolePayload(updated, final, members))
}

// DeleteRole 删除自定义角色。系统角色与仍有成员的角色都不允许删除。
func (h *AdminHandler) DeleteRole(c *gin.Context) {
	roleKey := strings.TrimSpace(c.Param("key"))
	var role model.Role
	if err := h.db.First(&role, "role_key = ?", roleKey).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if role.IsSystem {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"key": "系统角色不可删除"}))
		return
	}
	current, err := loadRolePermissions(h.db, role)
	if err != nil {
		slog.Error("读取角色权限失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	var members int64
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.User{}).Where("role_key = ?", role.Key).Count(&members).Error; err != nil {
			return err
		}
		if members > 0 {
			return errRoleHasMembers
		}
		if err := tx.Where("role_key = ?", role.Key).Delete(&model.RolePermission{}).Error; err != nil {
			return err
		}
		if err := tx.Where("role_key = ?", role.Key).Delete(&model.Role{}).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "role.delete", "role", role.Key, c.GetString("request_id"), "",
			roleAuditSummary(role, current), nil)
	})
	if errors.Is(err, errRoleHasMembers) {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{
			"key": fmt.Sprintf("该角色仍有 %d 名成员，请先调整成员角色", members),
		}))
		return
	}
	if err != nil {
		slog.Error("删除角色失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	noContent(c)
}

// errRoleHasMembers 表示角色仍有成员，删除被拒。
var errRoleHasMembers = errors.New("角色仍有成员")

type assignRoleReq struct {
	RoleKey *string `json:"roleKey"`
}

// AssignUserRole 调整用户的后台角色，是防锁死规则最集中的写入口：
// 不能改自己的角色、不能让系统角色失去最后一个 active 用户、角色必须真实存在。
func (h *AdminHandler) AssignUserRole(c *gin.Context) {
	targetID, ok := parseUUIDParam(c)
	if !ok {
		return
	}
	var req assignRoleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	var roleKey *string
	roleName := ""
	if req.RoleKey != nil {
		trimmed := strings.TrimSpace(*req.RoleKey)
		if trimmed != "" {
			var role model.Role
			if err := h.db.First(&role, "role_key = ?", trimmed).Error; err != nil {
				errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"roleKey": "角色不存在"}))
				return
			}
			roleKey = &role.Key
			roleName = role.Name
		}
	}
	var target model.User
	if err := h.db.First(&target, "id = ?", targetID).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	if sameRoleKey(target.RoleKey, roleKey) {
		// 角色没有变化时不写库、不撤销会话、不写审计。
		c.JSON(http.StatusOK, gin.H{"id": target.ID.String(), "roleKey": roleKeyJSON(roleKey)})
		return
	}
	actorID, _ := uuid.Parse(c.GetString("user_id"))
	if targetID == actorID {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"id": "不能修改自己的角色"}))
		return
	}
	beforeName := h.roleDisplayName(target.RoleKey)
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if isSystemRoleKey(target.RoleKey) && !isSystemRoleKey(roleKey) {
			remain, err := h.countActiveSystemMembers(tx, targetID)
			if err != nil {
				return err
			}
			if remain == 0 {
				return errLastSystemMember
			}
		}
		if err := authz.AssignRole(tx, targetID, roleKey); err != nil {
			return err
		}
		// 角色变更后撤销该用户全部 refresh token，避免旧会话继续使用旧角色。
		if err := tx.Model(&model.RefreshToken{}).
			Where("user_id = ? AND revoked_at IS NULL", targetID).
			Update("revoked_at", time.Now()).Error; err != nil {
			return err
		}
		return h.audit.Record(tx, actorID, "user.role", "user", targetID.String(), c.GetString("request_id"), "",
			userRoleAudit(target.RoleKey, beforeName), userRoleAudit(roleKey, roleName))
	})
	if errors.Is(err, errLastSystemMember) {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{
			"roleKey": "系统角色必须保留至少一个 active 用户",
		}))
		return
	}
	if err != nil {
		slog.Error("调整用户角色失败", "err", err)
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": target.ID.String(), "roleKey": roleKeyJSON(roleKey)})
}

// ===== 角色相关辅助 =====

func rolePayload(role model.Role, permissions []string, memberCount int64) gin.H {
	return gin.H{
		"key":         role.Key,
		"name":        role.Name,
		"description": role.Description,
		"isSystem":    role.IsSystem,
		"memberCount": memberCount,
		"permissions": permissions,
		"createdAt":   formatTime(role.CreatedAt),
		"updatedAt":   formatTime(role.UpdatedAt),
	}
}

// roleAuditSummary 是审计用的文本快照：存角色名与权限 key 的文本，
// 不存外键，保证角色被删除后历史摘要仍能还原。
func roleAuditSummary(role model.Role, permissions []string) gin.H {
	if permissions == nil {
		permissions = []string{}
	}
	return gin.H{
		"key":         role.Key,
		"name":        role.Name,
		"description": role.Description,
		"isSystem":    role.IsSystem,
		"permissions": permissions,
	}
}

// userRoleAudit 是成员角色变更的审计摘要，同样存角色名文本快照。
func userRoleAudit(roleKey *string, roleName string) gin.H {
	return gin.H{"roleKey": roleKeyJSON(roleKey), "roleName": roleName}
}

func roleKeyJSON(roleKey *string) any {
	if roleKey == nil {
		return nil
	}
	return *roleKey
}

func validateRoleFields(key, name, description string) map[string]string {
	fields := map[string]string{}
	if !roleKeyPattern.MatchString(key) {
		fields["key"] = "角色标识需以小写字母开头，只含小写字母、数字、点、下划线与连字符，长度 2-64"
	} else if key == authz.SystemRoleKey || strings.HasPrefix(key, authz.SystemRoleKey+".") {
		fields["key"] = "系统角色标识保留，不能占用"
	}
	if name == "" || len([]rune(name)) > 64 {
		fields["name"] = "角色名需为 1-64 个字符"
	}
	if len([]rune(description)) > 200 {
		fields["description"] = "角色说明不能超过 200 个字符"
	}
	return fields
}

// normalizePermissionKeys 去重并校验权限点，返回可写入的 key 与未注册的 key。
func normalizePermissionKeys(raw []string) (granted []string, unknown []string) {
	seen := make(map[string]struct{}, len(raw))
	granted = make([]string, 0, len(raw))
	for _, item := range raw {
		key := strings.TrimSpace(item)
		if key == "" {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		if !authz.IsKnown(key) {
			unknown = append(unknown, key)
			continue
		}
		granted = append(granted, key)
	}
	return granted, unknown
}

// loadRolePermissions 读取角色已授予的权限 key（只保留注册表内的，已排序）。
func loadRolePermissions(db *gorm.DB, role model.Role) ([]string, error) {
	if role.IsSystem {
		return authz.Keys(), nil
	}
	var keys []string
	if err := db.Model(&model.RolePermission{}).Where("role_key = ?", role.Key).
		Order("permission_key ASC").Pluck("permission_key", &keys).Error; err != nil {
		return nil, err
	}
	granted := make([]string, 0, len(keys))
	for _, key := range keys {
		if authz.IsKnown(key) {
			granted = append(granted, key)
		}
	}
	return granted, nil
}

func replaceRolePermissions(tx *gorm.DB, roleKey string, permissions []string) error {
	if err := tx.Where("role_key = ?", roleKey).Delete(&model.RolePermission{}).Error; err != nil {
		return err
	}
	if len(permissions) == 0 {
		return nil
	}
	rows := make([]model.RolePermission, 0, len(permissions))
	for _, key := range permissions {
		rows = append(rows, model.RolePermission{RoleKey: roleKey, PermissionKey: key})
	}
	return tx.Create(&rows).Error
}

func samePermissionSet(current, next []string) bool {
	if len(current) != len(next) {
		return false
	}
	for i := range current {
		if current[i] != next[i] {
			return false
		}
	}
	return true
}

func sameRoleKey(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func isSystemRoleKey(roleKey *string) bool {
	return roleKey != nil && *roleKey == authz.SystemRoleKey
}

// roleDisplayName 读取角色显示名，角色不存在时返回空串。审计摘要只存文本快照。
func (h *AdminHandler) roleDisplayName(roleKey *string) string {
	if roleKey == nil || *roleKey == "" {
		return ""
	}
	var role model.Role
	if err := h.db.First(&role, "role_key = ?", *roleKey).Error; err != nil {
		return ""
	}
	return role.Name
}

// countActiveSystemMembers 统计除 exclude 之外仍在系统角色上的 active 用户数。
func (h *AdminHandler) countActiveSystemMembers(tx *gorm.DB, exclude uuid.UUID) (int64, error) {
	var remain int64
	err := tx.Model(&model.User{}).
		Where("role_key = ? AND status = ? AND id <> ?", authz.SystemRoleKey, "active", exclude).
		Count(&remain).Error
	return remain, err
}
