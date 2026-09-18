package billing

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/infinite-canvas/server/internal/db"
	"github.com/infinite-canvas/server/internal/model"
)

func newBillingDB(t *testing.T) *gorm.DB {
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

func newBillingUser(t *testing.T, g *gorm.DB) model.PlatformUser {
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

// seedBalance 直接入账并写 purchase 流水，构造对账等式成立的起点。
func seedBalance(t *testing.T, g *gorm.DB, svc *Service, userID uuid.UUID, purchased, granted int64) {
	t.Helper()
	if err := svc.EnsureAccount(g, userID); err != nil {
		t.Fatalf("建账户行失败: %v", err)
	}
	if err := g.Model(&model.CreditAccount{}).Where("user_id = ?", userID).
		Updates(map[string]any{"purchased_micros": purchased, "granted_micros": granted}).Error; err != nil {
		t.Fatalf("写入余额失败: %v", err)
	}
	if purchased > 0 {
		if err := g.Create(&model.CreditTransaction{
			ID: uuid.New(), UserID: userID, Product: svc.product, Bucket: BucketPurchased, Type: TxTypePurchase,
			AmountMicros: purchased, BalanceAfterMicros: purchased, RefType: "order", RefID: "seed", CreatedAt: time.Now(),
		}).Error; err != nil {
			t.Fatalf("写入流水失败: %v", err)
		}
	}
	if granted > 0 {
		if err := g.Create(&model.CreditTransaction{
			ID: uuid.New(), UserID: userID, Product: svc.product, Bucket: BucketGranted, Type: TxTypeGrant,
			AmountMicros: granted, BalanceAfterMicros: granted, RefType: "admin", RefID: "seed", CreatedAt: time.Now(),
		}).Error; err != nil {
			t.Fatalf("写入流水失败: %v", err)
		}
	}
}

func TestReserveDeductsGrantedBeforePurchased(t *testing.T) {
	g := newBillingDB(t)
	svc := NewService(g, model.ProductCanvas)
	user := newBillingUser(t, g)
	seedBalance(t, g, svc, user.ID, 500_000, 300_000)

	txs, err := svc.Reserve(g, user.ID, 400_000, "req-1")
	if err != nil {
		t.Fatalf("扣减失败: %v", err)
	}
	if len(txs) != 2 {
		t.Fatalf("跨桶扣减应产生两条流水, got %d", len(txs))
	}
	for _, tx := range txs {
		if tx.RefID != "req-1" {
			t.Fatalf("流水 ref_id 应为 bizKey, got %q", tx.RefID)
		}
	}
	balance, _ := svc.Balance(context.Background(), user.ID)
	if balance.GrantedMicros != 0 || balance.PurchasedMicros != 400_000 {
		t.Fatalf("扣减后余额错误: granted=%d purchased=%d", balance.GrantedMicros, balance.PurchasedMicros)
	}

	// 逐桶对账等式成立。
	totals, err := svc.SumByBucket(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("汇总流水失败: %v", err)
	}
	if totals[BucketPurchased] != balance.PurchasedMicros || totals[BucketGranted] != balance.GrantedMicros {
		t.Fatalf("流水合计与余额不一致: totals=%v balance=%+v", totals, balance)
	}
}

func TestReserveInsufficientCredits(t *testing.T) {
	g := newBillingDB(t)
	svc := NewService(g, model.ProductCanvas)
	user := newBillingUser(t, g)

	txs, err := svc.Reserve(g, user.ID, 100, "req-1")
	if !errors.Is(err, ErrInsufficientCredits) {
		t.Fatalf("余额不足应返回 ErrInsufficientCredits, got %v", err)
	}
	if len(txs) != 0 {
		t.Fatalf("余额不足不应产生流水, got %d", len(txs))
	}
	balance, _ := svc.Balance(context.Background(), user.ID)
	if balance.PurchasedMicros != 0 || balance.GrantedMicros != 0 {
		t.Fatalf("余额不足不应改变余额: %+v", balance)
	}
}

func TestRefundByBizKeyIsIdempotent(t *testing.T) {
	g := newBillingDB(t)
	svc := NewService(g, model.ProductCanvas)
	user := newBillingUser(t, g)
	seedBalance(t, g, svc, user.ID, 500_000, 300_000)

	if _, err := svc.Reserve(g, user.ID, 400_000, "req-1"); err != nil {
		t.Fatalf("扣减失败: %v", err)
	}
	// 按 bizKey 退款：退回原桶。
	if err := svc.Refund(g, user.ID, "req-1"); err != nil {
		t.Fatalf("退款失败: %v", err)
	}
	if err := svc.Refund(g, user.ID, "req-1"); err != nil {
		t.Fatalf("重复退款应幂等: %v", err)
	}
	balance, _ := svc.Balance(context.Background(), user.ID)
	if balance.GrantedMicros != 300_000 || balance.PurchasedMicros != 500_000 {
		t.Fatalf("退款后余额未回原值: granted=%d purchased=%d", balance.GrantedMicros, balance.PurchasedMicros)
	}
	var refunds int64
	g.Model(&model.CreditTransaction{}).Where("user_id = ? AND type = ?", user.ID, TxTypeRefund).Count(&refunds)
	if refunds != 2 {
		t.Fatalf("重复退款不应产生多余退款流水, got %d", refunds)
	}
	totals, _ := svc.SumByBucket(context.Background(), user.ID)
	if totals[BucketPurchased] != 500_000 || totals[BucketGranted] != 300_000 {
		t.Fatalf("重复退款后流水合计应回到原余额: %v", totals)
	}
}

func TestConcurrentReserveNeverOverdraws(t *testing.T) {
	g := newBillingDB(t)
	svc := NewService(g, model.ProductCanvas)
	user := newBillingUser(t, g)
	seedBalance(t, g, svc, user.ID, 1_000, 0)

	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// 生产约定 Reserve 在调用方事务内执行；测试同样包事务保证读改写原子。
			err := g.Transaction(func(tx *gorm.DB) error {
				_, err := svc.Reserve(tx, user.ID, 100, uuid.NewString())
				return err
			})
			if err == nil {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if succeeded != 10 {
		t.Fatalf("并发扣减应恰好成功 10 次, got %d", succeeded)
	}
	balance, _ := svc.Balance(context.Background(), user.ID)
	if balance.PurchasedMicros != 0 {
		t.Fatalf("并发扣减后余额应为 0, got %d", balance.PurchasedMicros)
	}
	totals, _ := svc.SumByBucket(context.Background(), user.ID)
	if totals[BucketPurchased] != 0 {
		t.Fatalf("流水合计应为 0, got %d", totals[BucketPurchased])
	}
}

func TestAdjustRejectsNegative(t *testing.T) {
	g := newBillingDB(t)
	svc := NewService(g, model.ProductCanvas)
	user := newBillingUser(t, g)
	if err := svc.EnsureAccount(g, user.ID); err != nil {
		t.Fatalf("建账户行失败: %v", err)
	}
	if _, err := svc.Adjust(g, user.ID, BucketGranted, -100, "补偿", "admin"); !errors.Is(err, ErrInsufficientCredits) {
		t.Fatalf("扣成负数应被拒绝, got %v", err)
	}
	balance, _ := svc.Balance(context.Background(), user.ID)
	if balance.GrantedMicros != 0 {
		t.Fatalf("被拒绝的调整不应改变余额: %+v", balance)
	}
}

func TestConsumeAndRefundFreeTrial(t *testing.T) {
	g := newBillingDB(t)
	svc := NewService(g, model.ProductCanvas)
	user := newBillingUser(t, g)
	if err := g.Create(&model.FreeGrantClaim{
		ID: uuid.New(), UserID: user.ID, CampaignID: "test-campaign", Status: "granted",
	}).Error; err != nil {
		t.Fatalf("写入领取记录失败: %v", err)
	}

	consumed, err := svc.ConsumeFreeTrial(g, user.ID, MetricFreeImageTrial)
	if err != nil || !consumed {
		t.Fatalf("首次占用应成功: consumed=%v err=%v", consumed, err)
	}
	remaining, err := svc.FreeTrialRemaining(context.Background(), user.ID, MetricFreeImageTrial)
	if err != nil {
		t.Fatalf("读取剩余次数失败: %v", err)
	}
	if remaining != FreeImageTrialLimit-1 {
		t.Fatalf("剩余次数应为 %d, got %d", FreeImageTrialLimit-1, remaining)
	}
	if err := svc.RefundFreeTrial(g, user.ID, MetricFreeImageTrial); err != nil {
		t.Fatalf("退还试用失败: %v", err)
	}
	remaining, _ = svc.FreeTrialRemaining(context.Background(), user.ID, MetricFreeImageTrial)
	if remaining != FreeImageTrialLimit {
		t.Fatalf("退还后剩余次数应回到上限, got %d", remaining)
	}
}
