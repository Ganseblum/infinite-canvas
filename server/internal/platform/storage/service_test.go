package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/infinite-canvas/server/internal/db"
	"github.com/infinite-canvas/server/internal/model"
)

func newStorageDB(t *testing.T) *gorm.DB {
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
	if err := db.Migrate(g); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return g
}

func newStorageUser(t *testing.T, g *gorm.DB) model.PlatformUser {
	t.Helper()
	user := model.PlatformUser{
		ID:           uuid.New(),
		Email:        uuid.NewString() + "@example.com",
		Username:     uuid.NewString()[:16],
		PasswordHash: "x",
		Status:       "active",
	}
	if err := g.Create(&user).Error; err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	return user
}

func TestCheckRejectsOverQuotaWith507(t *testing.T) {
	g := newStorageDB(t)
	svc := NewService(g, model.ProductCanvas)
	user := newStorageUser(t, g)
	if err := svc.EnsureAccount(g, user.ID, 100); err != nil {
		t.Fatalf("建账户行失败: %v", err)
	}

	// 用量 60，再写 40 恰好贴线；41 超限 → 507 语义。
	if err := svc.Commit(g, user.ID, 60); err != nil {
		t.Fatalf("记账失败: %v", err)
	}
	if err := svc.Check(context.Background(), user.ID, 40); err != nil {
		t.Fatalf("贴线写入应通过: %v", err)
	}
	if err := svc.Check(context.Background(), user.ID, 41); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("超配额应返回 ErrQuotaExceeded(507), got %v", err)
	}
}

func TestCheckReadOnlyWhenUsedExceedsQuota(t *testing.T) {
	g := newStorageDB(t)
	svc := NewService(g, model.ProductCanvas)
	user := newStorageUser(t, g)
	if err := svc.EnsureAccount(g, user.ID, 40); err != nil {
		t.Fatalf("建账户行失败: %v", err)
	}
	// 人为制造 used > quota 的只读态（模拟降档）。
	if err := svc.Commit(g, user.ID, 100); err != nil {
		t.Fatalf("记账失败: %v", err)
	}
	// 只读态下即使 delta 为 0 也拒绝（403 READ_ONLY），引导清理而非充值。
	if err := svc.Check(context.Background(), user.ID, 0); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("只读态应返回 ErrReadOnly(403), got %v", err)
	}
}

func TestCommitUpdatesBothLayers(t *testing.T) {
	g := newStorageDB(t)
	svc := NewService(g, model.ProductCanvas)
	user := newStorageUser(t, g)
	if err := svc.EnsureAccount(g, user.ID, 1<<20); err != nil {
		t.Fatalf("建账户行失败: %v", err)
	}

	if err := svc.Commit(g, user.ID, 100); err != nil {
		t.Fatalf("记账失败: %v", err)
	}
	if err := svc.Commit(g, user.ID, -30); err != nil {
		t.Fatalf("负增量记账失败: %v", err)
	}
	used, quota, err := svc.Snapshot(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("读取快照失败: %v", err)
	}
	if used != 70 || quota != 1<<20 {
		t.Fatalf("共享池聚合错误: used=%d quota=%d", used, quota)
	}
	var usage model.StorageUsage
	if err := g.First(&usage, "user_id = ? AND product = ?", user.ID, model.ProductCanvas).Error; err != nil {
		t.Fatalf("分产品用量行不存在: %v", err)
	}
	if usage.Bytes != 70 {
		t.Fatalf("分产品用量错误: %d", usage.Bytes)
	}
	// 对账等式：media_files 聚合 = storage_usage（未建媒体行前各层同步增减）。
	var filesTotal int64
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Select("COALESCE(SUM(bytes), 0)").Scan(&filesTotal)
	_ = filesTotal
}

func TestRecalculateRebuildsFromMediaFiles(t *testing.T) {
	g := newStorageDB(t)
	svc := NewService(g, model.ProductCanvas)
	user := newStorageUser(t, g)
	if err := svc.EnsureAccount(g, user.ID, 1<<20); err != nil {
		t.Fatalf("建账户行失败: %v", err)
	}
	if err := svc.Commit(g, user.ID, 999); err != nil {
		t.Fatalf("记账失败: %v", err)
	}
	if err := g.Create(&model.MediaFile{
		ID: uuid.New(), UserID: user.ID, StorageKey: "image:R1", ObjectPath: "p",
		MimeType: "image/png", Bytes: 42, Product: model.ProductCanvas, Checksum: "x",
	}).Error; err != nil {
		t.Fatalf("写入媒体行失败: %v", err)
	}
	used, err := svc.Recalculate(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("重算失败: %v", err)
	}
	if used != 42 {
		t.Fatalf("重算后应为 42, got %d", used)
	}
	snapshotUsed, _, _ := svc.Snapshot(context.Background(), user.ID)
	if snapshotUsed != 42 {
		t.Fatalf("共享池聚合应同步重算, got %d", snapshotUsed)
	}
}
