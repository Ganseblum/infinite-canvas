package db

import (
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/model"
)

func newDBTestDB(t *testing.T) *gorm.DB {
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
	if err := Migrate(g); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := authz.Sync(g); err != nil {
		t.Fatalf("同步角色与权限目录失败: %v", err)
	}
	return g
}

func TestEnsureAdminWritesSystemRoleKey(t *testing.T) {
	g := newDBTestDB(t)
	if err := EnsureAdmin(g, "root@example.com", "password123"); err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}
	var created model.User
	if err := g.First(&created, "email = ?", "root@example.com").Error; err != nil {
		t.Fatalf("读取管理员失败: %v", err)
	}
	if created.RoleKey == nil || *created.RoleKey != authz.SystemRoleKey {
		t.Fatalf("新建管理员必须写 role_key=admin, got %v", created.RoleKey)
	}
	if created.Role != "admin" {
		t.Fatalf("旧投影列应为 admin, got %s", created.Role)
	}
	originalHash := created.PasswordHash

	// 老用户（role='admin'，role_key 为空）应被提升为系统角色，且不重置密码。
	legacy := model.User{
		ID: uuid.New(), Email: "legacy@example.com", Username: "legacy",
		PasswordHash: "legacy-hash", Role: "admin", Status: "active",
	}
	if err := g.Create(&legacy).Error; err != nil {
		t.Fatalf("写入老管理员失败: %v", err)
	}
	if err := EnsureAdmin(g, "legacy@example.com", "newpassword"); err != nil {
		t.Fatalf("提升老管理员失败: %v", err)
	}
	var reloaded model.User
	if err := g.First(&reloaded, "id = ?", legacy.ID).Error; err != nil {
		t.Fatalf("读取老管理员失败: %v", err)
	}
	if reloaded.RoleKey == nil || *reloaded.RoleKey != authz.SystemRoleKey {
		t.Fatalf("已存在用户应被提升为系统角色, got %v", reloaded.RoleKey)
	}
	if reloaded.PasswordHash != "legacy-hash" {
		t.Fatalf("EnsureAdmin 不允许重置已有管理员的密码")
	}

	// 幂等：重复执行不改密码、不改角色。
	if err := EnsureAdmin(g, "root@example.com", "password123"); err != nil {
		t.Fatalf("重复执行 EnsureAdmin 失败: %v", err)
	}
	var again model.User
	if err := g.First(&again, "email = ?", "root@example.com").Error; err != nil {
		t.Fatalf("读取管理员失败: %v", err)
	}
	if again.PasswordHash != originalHash {
		t.Fatalf("重复执行不允许改动密码哈希")
	}

	if err := EnsureAdmin(g, "", ""); err == nil {
		t.Fatalf("ADMIN_EMAIL / ADMIN_PASSWORD 为空时必须报错")
	}
}
