package authz

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/infinite-canvas/server/internal/model"
)

func newAuthzTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	g, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	sqlDB, err := g.DB()
	if err != nil {
		t.Fatalf("获取底层连接失败: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := g.AutoMigrate(&model.PlatformUser{}, &model.Role{}, &model.Permission{}, &model.RolePermission{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return g
}

func TestSyncIsIdempotentAndKeepsEditedName(t *testing.T) {
	g := newAuthzTestDB(t)
	if err := Sync(g); err != nil {
		t.Fatalf("首次同步失败: %v", err)
	}
	if err := Sync(g); err != nil {
		t.Fatalf("重复同步必须幂等: %v", err)
	}
	var permissionCount int64
	if err := g.Model(&model.Permission{}).Count(&permissionCount).Error; err != nil {
		t.Fatalf("统计权限点失败: %v", err)
	}
	if permissionCount != int64(len(registry)) {
		t.Fatalf("权限点投影条数应为 %d, got %d", len(registry), permissionCount)
	}
	var sysRole model.Role
	if err := g.First(&sysRole, "role_key = ?", SystemRoleKey).Error; err != nil {
		t.Fatalf("系统角色应存在: %v", err)
	}
	if !sysRole.IsSystem {
		t.Fatalf("系统角色 is_system 应为 true")
	}
	// 显示名可改：人工改过之后再次同步不允许被写回默认值。
	if err := g.Model(&model.Role{}).Where("role_key = ?", SystemRoleKey).Update("name", "运营管理员").Error; err != nil {
		t.Fatalf("修改显示名失败: %v", err)
	}
	if err := Sync(g); err != nil {
		t.Fatalf("再次同步失败: %v", err)
	}
	var reloaded model.Role
	if err := g.First(&reloaded, "role_key = ?", SystemRoleKey).Error; err != nil {
		t.Fatalf("读取系统角色失败: %v", err)
	}
	if reloaded.Name != "运营管理员" {
		t.Fatalf("人工修改的显示名被覆盖: %s", reloaded.Name)
	}
}

func TestSyncDeprecatesRemovedPermissionWithoutDeleting(t *testing.T) {
	g := newAuthzTestDB(t)
	if err := Sync(g); err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	legacy := model.Permission{Key: "legacy.old", Module: "stats", Name: "旧权限点"}
	if err := g.Create(&legacy).Error; err != nil {
		t.Fatalf("写入旧权限点失败: %v", err)
	}
	if err := g.Create(&model.RolePermission{RoleKey: SystemRoleKey, PermissionKey: "legacy.old"}).Error; err != nil {
		t.Fatalf("写入旧权限分配失败: %v", err)
	}
	if err := Sync(g); err != nil {
		t.Fatalf("再次同步失败: %v", err)
	}
	var reloaded model.Permission
	if err := g.First(&reloaded, "permission_key = ?", "legacy.old").Error; err != nil {
		t.Fatalf("代码中消失的权限点只允许标废弃，不允许删除: %v", err)
	}
	if reloaded.DeprecatedAt == nil {
		t.Fatalf("代码中消失的权限点应标记 deprecated_at")
	}
	var assigned int64
	if err := g.Model(&model.RolePermission{}).Where("permission_key = ?", "legacy.old").Count(&assigned).Error; err != nil {
		t.Fatalf("统计旧权限分配失败: %v", err)
	}
	if assigned != 1 {
		t.Fatalf("废弃权限的历史分配不允许删除, got %d", assigned)
	}
	if IsKnown("legacy.old") {
		t.Fatalf("未登记的 key 不允许通过 IsKnown（中间件据此失败关闭）")
	}
}

func TestSyncRestoresDeprecatedMarkerWhenKeyReturns(t *testing.T) {
	g := newAuthzTestDB(t)
	if err := Sync(g); err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	now := time.Now()
	if err := g.Model(&model.Permission{}).Where("permission_key = ?", PermStatsRead).
		Update("deprecated_at", now).Error; err != nil {
		t.Fatalf("标记废弃失败: %v", err)
	}
	if err := Sync(g); err != nil {
		t.Fatalf("再次同步失败: %v", err)
	}
	var restored model.Permission
	if err := g.First(&restored, "permission_key = ?", PermStatsRead).Error; err != nil {
		t.Fatalf("读取权限点失败: %v", err)
	}
	if restored.DeprecatedAt != nil {
		t.Fatalf("key 回到注册表后应撤销废弃标记")
	}
}

func TestRetiredAliasMigrationIsIdempotent(t *testing.T) {
	g := newAuthzTestDB(t)
	if err := Sync(g); err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	if err := g.Create(&model.Permission{Key: "stats.legacy_read", Module: "stats", Name: "旧统计权限"}).Error; err != nil {
		t.Fatalf("写入旧权限点失败: %v", err)
	}
	if err := g.Create(&model.RolePermission{RoleKey: SystemRoleKey, PermissionKey: "stats.legacy_read"}).Error; err != nil {
		t.Fatalf("写入旧分配失败: %v", err)
	}
	alias := map[string]string{"stats.legacy_read": PermStatsRead}
	for i := 0; i < 2; i++ {
		if err := applyRetiredAliases(g, alias); err != nil {
			t.Fatalf("第 %d 次别名迁移失败: %v", i+1, err)
		}
	}
	var copies int64
	if err := g.Model(&model.RolePermission{}).
		Where("role_key = ? AND permission_key = ?", SystemRoleKey, PermStatsRead).
		Count(&copies).Error; err != nil {
		t.Fatalf("统计新 key 分配失败: %v", err)
	}
	if copies != 1 {
		t.Fatalf("别名迁移必须幂等复制，新 key 分配条数 got %d", copies)
	}
	var old model.Permission
	if err := g.First(&old, "permission_key = ?", "stats.legacy_read").Error; err != nil {
		t.Fatalf("别名旧 key 不允许删除: %v", err)
	}
	if old.DeprecatedAt == nil {
		t.Fatalf("别名旧 key 应标记 deprecated_at")
	}
	if err := applyRetiredAliases(g, map[string]string{"stats.legacy_read": "not.registered"}); err == nil {
		t.Fatalf("别名目标未注册时必须报错，避免把权限迁到不存在的 key")
	}
}

func TestSyncBackfillsLegacyRoleColumn(t *testing.T) {
	g := newAuthzTestDB(t)
	if err := Sync(g); err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	legacyAdmin := model.PlatformUser{
		ID: uuid.New(), Email: "legacy-admin@example.com", Username: "legacyadmin",
		PasswordHash: "x", Role: "admin", Status: "active",
	}
	legacyUser := model.PlatformUser{
		ID: uuid.New(), Email: "legacy-user@example.com", Username: "legacyuser",
		PasswordHash: "x", Role: "user", Status: "active",
	}
	for _, user := range []*model.PlatformUser{&legacyAdmin, &legacyUser} {
		if err := g.Create(user).Error; err != nil {
			t.Fatalf("写入老用户失败: %v", err)
		}
	}
	if err := Sync(g); err != nil {
		t.Fatalf("再次同步失败: %v", err)
	}
	var reloadedAdmin, reloadedUser model.PlatformUser
	if err := g.First(&reloadedAdmin, "id = ?", legacyAdmin.ID).Error; err != nil {
		t.Fatalf("读取老管理员失败: %v", err)
	}
	if reloadedAdmin.RoleKey == nil || *reloadedAdmin.RoleKey != SystemRoleKey {
		t.Fatalf("老 role='admin' 应回填 role_key=admin, got %v", reloadedAdmin.RoleKey)
	}
	if err := g.First(&reloadedUser, "id = ?", legacyUser.ID).Error; err != nil {
		t.Fatalf("读取老用户失败: %v", err)
	}
	if reloadedUser.RoleKey != nil {
		t.Fatalf("老 role='user' 的 role_key 必须保持为空, got %v", *reloadedUser.RoleKey)
	}
	// 自定义角色用户的旧投影必须是保守的 user。
	editor := "editor"
	if err := g.Create(&model.Role{Key: editor, Name: "编辑"}).Error; err != nil {
		t.Fatalf("写入自定义角色失败: %v", err)
	}
	drifted := model.PlatformUser{
		ID: uuid.New(), Email: "drifted@example.com", Username: "drifted",
		PasswordHash: "x", Role: "admin", Status: "active", RoleKey: &editor,
	}
	if err := g.Create(&drifted).Error; err != nil {
		t.Fatalf("写入漂移用户失败: %v", err)
	}
	if err := Sync(g); err != nil {
		t.Fatalf("再次同步失败: %v", err)
	}
	var reloadedDrift model.PlatformUser
	if err := g.First(&reloadedDrift, "id = ?", drifted.ID).Error; err != nil {
		t.Fatalf("读取漂移用户失败: %v", err)
	}
	if reloadedDrift.Role != "user" {
		t.Fatalf("非系统角色的旧投影必须回落为 user, got %s", reloadedDrift.Role)
	}
}

func TestAssignRoleWritesProjection(t *testing.T) {
	g := newAuthzTestDB(t)
	if err := Sync(g); err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	user := model.PlatformUser{
		ID: uuid.New(), Email: "assign@example.com", Username: "assign",
		PasswordHash: "x", Role: "user", Status: "active",
	}
	if err := g.Create(&user).Error; err != nil {
		t.Fatalf("写入用户失败: %v", err)
	}
	systemKey := SystemRoleKey
	if err := AssignRole(g, user.ID, &systemKey); err != nil {
		t.Fatalf("分配系统角色失败: %v", err)
	}
	var promoted model.PlatformUser
	if err := g.First(&promoted, "id = ?", user.ID).Error; err != nil {
		t.Fatalf("读取用户失败: %v", err)
	}
	if promoted.Role != "admin" || promoted.RoleKey == nil || *promoted.RoleKey != SystemRoleKey {
		t.Fatalf("系统角色写入后两列都应更新: role=%s role_key=%v", promoted.Role, promoted.RoleKey)
	}
	customKey := "editor"
	if err := AssignRole(g, user.ID, &customKey); err != nil {
		t.Fatalf("分配自定义角色失败: %v", err)
	}
	if err := g.First(&promoted, "id = ?", user.ID).Error; err != nil {
		t.Fatalf("读取用户失败: %v", err)
	}
	if promoted.Role != "user" || promoted.RoleKey == nil || *promoted.RoleKey != customKey {
		t.Fatalf("自定义角色的投影必须是 user: role=%s role_key=%v", promoted.Role, promoted.RoleKey)
	}
	if err := AssignRole(g, user.ID, nil); err != nil {
		t.Fatalf("清空角色失败: %v", err)
	}
	if err := g.First(&promoted, "id = ?", user.ID).Error; err != nil {
		t.Fatalf("读取用户失败: %v", err)
	}
	if promoted.Role != "user" || promoted.RoleKey != nil {
		t.Fatalf("清空角色后 role_key 必须为 NULL: role=%s role_key=%v", promoted.Role, promoted.RoleKey)
	}
}
