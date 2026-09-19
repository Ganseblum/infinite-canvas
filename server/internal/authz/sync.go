package authz

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/infinite-canvas/server/internal/model"
)

// SupportRoleKey 是客服系统角色的标识：绑定反馈工单权限，由管理员在用户管理里任命。
// 它是系统角色（不可删除），权限固定为反馈工单模块；需要更细的客服权限时另建自定义角色。
const SupportRoleKey = "support"

// Sync 是启动期的幂等同步，必须在 AutoMigrate 之后、EnsureAdmin 之前调用：
//
//  1. 保证系统角色 admin 与 support 存在且 is_system=true（显示名可被人工修改，不覆盖）；
//  2. 把代码注册表投影到 permissions 表：新增插入、显示名/模块/排序更新，
//     代码中已消失的 key 只置 deprecated_at，绝不删除；
//  3. 处理改名逃生门 retiredKeys：旧 key 的角色分配幂等复制到新 key 后标废弃；
//  4. 回填老 role 列到 role_key，并把旧投影列同步为保守投影。
func Sync(db *gorm.DB) error {
	if err := validateRegistry(); err != nil {
		return err
	}
	if err := ensureSystemRole(db); err != nil {
		return err
	}
	if err := syncPermissions(db); err != nil {
		return err
	}
	if err := ensureSupportRole(db); err != nil {
		return err
	}
	if err := applyRetiredAliases(db, retiredKeys); err != nil {
		return err
	}
	return backfillUserRoles(db)
}

// ensureSystemRole 保证系统角色存在。key 不可改、不可删除、不可改权限；
// 显示名由人工维护，这里只在缺失时写入默认值。
func ensureSystemRole(db *gorm.DB) error {
	var role model.Role
	err := db.First(&role, "role_key = ?", SystemRoleKey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		now := time.Now()
		return db.Create(&model.Role{
			Key:         SystemRoleKey,
			Name:        "管理员",
			Description: "系统角色，隐式拥有全部权限，不可删除或修改权限",
			IsSystem:    true,
			CreatedAt:   now,
			UpdatedAt:   now,
		}).Error
	}
	if err != nil {
		return fmt.Errorf("读取系统角色失败: %w", err)
	}
	if !role.IsSystem {
		if err := db.Model(&model.Role{}).Where("role_key = ?", SystemRoleKey).Update("is_system", true).Error; err != nil {
			return fmt.Errorf("修复系统角色标记失败: %w", err)
		}
	}
	return nil
}

// ensureSupportRole 保证客服系统角色存在，并幂等绑定反馈工单权限点。
// 显示名由人工维护，这里只在缺失时写入默认值；权限绑定按 (role_key, permission_key)
// 幂等补齐，人工解绑会在下次启动被补回（客服权限固定，与系统角色语义一致）。
func ensureSupportRole(db *gorm.DB) error {
	var role model.Role
	err := db.First(&role, "role_key = ?", SupportRoleKey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		now := time.Now()
		role = model.Role{
			Key:         SupportRoleKey,
			Name:        "客服",
			Description: "系统角色，处理用户反馈工单，不可删除",
			IsSystem:    true,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := db.Create(&role).Error; err != nil {
			return fmt.Errorf("创建客服系统角色失败: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("读取客服系统角色失败: %w", err)
	}
	if !role.IsSystem {
		if err := db.Model(&model.Role{}).Where("role_key = ?", SupportRoleKey).Update("is_system", true).Error; err != nil {
			return fmt.Errorf("修复客服系统角色标记失败: %w", err)
		}
	}
	for _, key := range []string{PermFeedbackRead, PermFeedbackWrite} {
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.RolePermission{
			RoleKey:       SupportRoleKey,
			PermissionKey: key,
			CreatedAt:     time.Now(),
		}).Error; err != nil {
			return fmt.Errorf("绑定客服权限 %s 失败: %w", key, err)
		}
	}
	return nil
}

func syncPermissions(db *gorm.DB) error {
	keys := make([]string, 0, len(registry))
	for _, def := range registry {
		keys = append(keys, def.Key)
		var existing model.Permission
		err := db.First(&existing, "permission_key = ?", def.Key).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := db.Create(&model.Permission{
				Key:         def.Key,
				Module:      def.Module,
				Name:        def.Name,
				Description: def.Description,
				Sort:        def.Sort,
				CreatedAt:   time.Now(),
			}).Error; err != nil {
				return fmt.Errorf("写入权限点 %s 失败: %w", def.Key, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("读取权限点 %s 失败: %w", def.Key, err)
		}
		updates := map[string]any{}
		if existing.Module != def.Module {
			updates["module"] = def.Module
		}
		if existing.Name != def.Name {
			updates["name"] = def.Name
		}
		if existing.Description != def.Description {
			updates["description"] = def.Description
		}
		if existing.Sort != def.Sort {
			updates["sort"] = def.Sort
		}
		if existing.DeprecatedAt != nil {
			// key 回到注册表（例如误删后恢复）时撤销废弃标记。
			updates["deprecated_at"] = nil
		}
		if len(updates) == 0 {
			continue
		}
		if err := db.Model(&model.Permission{}).Where("permission_key = ?", def.Key).Updates(updates).Error; err != nil {
			return fmt.Errorf("更新权限点 %s 失败: %w", def.Key, err)
		}
	}
	// 注册表中已消失的 key 只标废弃，保留行以便历史分配可追溯。
	if err := db.Model(&model.Permission{}).
		Where("deprecated_at IS NULL AND permission_key NOT IN ?", keys).
		Update("deprecated_at", time.Now()).Error; err != nil {
		return fmt.Errorf("标记废弃权限点失败: %w", err)
	}
	return nil
}

// applyRetiredAliases 把旧 key 上的角色分配幂等复制到新 key，再给旧 key 标废弃。
// 允许重复执行，重复执行不会产生重复分配，也不会删除任何分配。
func applyRetiredAliases(db *gorm.DB, retired map[string]string) error {
	for oldKey, newKey := range retired {
		if !IsKnown(newKey) {
			return fmt.Errorf("权限点别名 %s → %s 的目标未在注册表中", oldKey, newKey)
		}
		var roleKeys []string
		if err := db.Model(&model.RolePermission{}).
			Where("permission_key = ?", oldKey).
			Pluck("role_key", &roleKeys).Error; err != nil {
			return fmt.Errorf("读取别名权限 %s 的分配失败: %w", oldKey, err)
		}
		if len(roleKeys) > 0 {
			rows := make([]model.RolePermission, 0, len(roleKeys))
			for _, roleKey := range roleKeys {
				rows = append(rows, model.RolePermission{RoleKey: roleKey, PermissionKey: newKey})
			}
			if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error; err != nil {
				return fmt.Errorf("迁移别名权限 %s → %s 失败: %w", oldKey, newKey, err)
			}
		}
		if err := db.Model(&model.Permission{}).
			Where("permission_key = ?", oldKey).
			Update("deprecated_at", time.Now()).Error; err != nil {
			return fmt.Errorf("标记别名权限 %s 废弃失败: %w", oldKey, err)
		}
	}
	return nil
}

// backfillUserRoles 做老数据迁移与投影修复，幂等：
//   - 老 role='admin' 且尚未分配角色的用户 → 系统角色（老 role='user' 本身就是 role_key 为空）；
//   - 旧 role 列同步为保守投影：role_key 为 admin 时是 admin，其余一律 user。
func backfillUserRoles(db *gorm.DB) error {
	if err := db.Model(&model.PlatformUser{}).
		Where("role = ? AND role_key IS NULL", "admin").
		Update("role_key", SystemRoleKey).Error; err != nil {
		return fmt.Errorf("回填 role_key 失败: %w", err)
	}
	if err := db.Model(&model.PlatformUser{}).
		Where("role = ? AND role_key = ?", "user", SystemRoleKey).
		Update("role", "admin").Error; err != nil {
		return fmt.Errorf("修复 role 投影失败: %w", err)
	}
	if err := db.Model(&model.PlatformUser{}).
		Where("(role_key IS NULL OR role_key <> ?) AND role <> ?", SystemRoleKey, "user").
		Update("role", "user").Error; err != nil {
		return fmt.Errorf("修复 role 投影失败: %w", err)
	}
	return nil
}
