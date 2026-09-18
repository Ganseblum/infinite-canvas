package authz

import (
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
)

// RoleProjection 返回旧 users.role 列的保守投影值。
// 只在 role_key 恰为系统角色时投影为 admin，其余（含自定义角色）一律 user：
// 投影列只是回滚安全带，宁可少给权限也不多给。
func RoleProjection(roleKey *string) string {
	if roleKey != nil && *roleKey == SystemRoleKey {
		return "admin"
	}
	return "user"
}

// AssignRole 是唯一的角色写入助手：一次写入 role_key 与旧 role 投影列。
// 所有角色变更（含清空角色）都必须走这里，避免两列漂移。
func AssignRole(tx *gorm.DB, userID uuid.UUID, roleKey *string) error {
	return tx.Model(&model.PlatformUser{}).Where("id = ?", userID).Updates(map[string]any{
		"role_key": roleKey,
		"role":     RoleProjection(roleKey),
	}).Error
}
