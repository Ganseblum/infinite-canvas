package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
	platformstorage "github.com/infinite-canvas/server/internal/platform/storage"
)

// setupReconcile 建用户与存储账户，写一条媒体并按正常路径 Commit 记账（L2+L3 同事务），
// 返回可直接执行对账的服务。
func setupReconcile(t *testing.T, g *gorm.DB, mediaBytes int64) (*ReconcileService, model.PlatformUser) {
	t.Helper()
	user := createUserRow(t, g)
	storSvc := platformstorage.NewService(g, model.ProductCanvas)
	if err := storSvc.EnsureAccount(g, user.ID, 1<<30); err != nil {
		t.Fatalf("建存储账户失败: %v", err)
	}
	if mediaBytes > 0 {
		seedMedia(t, g, newMapStorage(), user.ID, "image:R"+uuid.NewString()[:8], mediaBytes)
		if err := g.Transaction(func(tx *gorm.DB) error {
			return storSvc.Commit(tx, user.ID, mediaBytes)
		}); err != nil {
			t.Fatalf("记账失败: %v", err)
		}
	}
	return NewReconcileService(g, storSvc), user
}

func TestReconcileStorageConsistent(t *testing.T) {
	g := newServiceDB(t)
	svc, _ := setupReconcile(t, g, 100)
	checked, fixed, err := svc.ReconcileStorage(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if checked < 1 {
		t.Fatalf("至少检查一个账号, got %d", checked)
	}
	if fixed != 0 {
		t.Fatalf("三层平账不应修正, got %d", fixed)
	}
}

func TestReconcileStorageFixesUsageDrift(t *testing.T) {
	g := newServiceDB(t)
	svc, user := setupReconcile(t, g, 100)
	// 模拟进程中断：分产品计数层漂移。
	if err := g.Model(&model.StorageUsage{}).
		Where("user_id = ? AND product = ?", user.ID, model.ProductCanvas).
		Update("bytes", 999).Error; err != nil {
		t.Fatalf("制造漂移失败: %v", err)
	}
	checked, fixed, err := svc.ReconcileStorage(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if fixed != 1 {
		t.Fatalf("应修正一个账号, got %d (checked=%d)", fixed, checked)
	}
	var usage model.StorageUsage
	if err := g.First(&usage, "user_id = ? AND product = ?", user.ID, model.ProductCanvas).Error; err != nil {
		t.Fatalf("读取用量失败: %v", err)
	}
	if usage.Bytes != 100 {
		t.Fatalf("计数层应被重算回事实源 100, got %d", usage.Bytes)
	}
	var account model.StorageAccount
	if err := g.First(&account, "user_id = ?", user.ID).Error; err != nil {
		t.Fatalf("读取账户失败: %v", err)
	}
	if account.UsedBytes != 100 {
		t.Fatalf("共享池聚合应同步重算为 100, got %d", account.UsedBytes)
	}
}

func TestReconcileStorageFixesAccountDrift(t *testing.T) {
	g := newServiceDB(t)
	svc, user := setupReconcile(t, g, 100)
	// 模拟共享池聚合层漂移。
	if err := g.Model(&model.StorageAccount{}).Where("user_id = ?", user.ID).
		Update("used_bytes", 9999).Error; err != nil {
		t.Fatalf("制造漂移失败: %v", err)
	}
	_, fixed, err := svc.ReconcileStorage(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if fixed != 1 {
		t.Fatalf("应修正一个账号, got %d", fixed)
	}
	var account model.StorageAccount
	if err := g.First(&account, "user_id = ?", user.ID).Error; err != nil {
		t.Fatalf("读取账户失败: %v", err)
	}
	if account.UsedBytes != 100 {
		t.Fatalf("共享池聚合应重算为 100, got %d", account.UsedBytes)
	}
}
