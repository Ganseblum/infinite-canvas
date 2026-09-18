package membership

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/infinite-canvas/server/internal/db"
	"github.com/infinite-canvas/server/internal/model"
)

func newMembershipDB(t *testing.T) *gorm.DB {
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
	if err := db.SeedMembershipPlans(g); err != nil {
		t.Fatalf("写入默认档位失败: %v", err)
	}
	return g
}

func newMembershipUser(t *testing.T, g *gorm.DB) model.PlatformUser {
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

func TestActivePlanWithoutSubscriptionFallsBackToFree(t *testing.T) {
	g := newMembershipDB(t)
	svc := NewService(g)
	user := newMembershipUser(t, g)

	planID, graceEndsAt, err := svc.ActivePlan(context.Background(), user.ID, time.Now())
	if err != nil {
		t.Fatalf("派生档位失败: %v", err)
	}
	if planID != "free" || graceEndsAt != nil {
		t.Fatalf("无订阅应回落 free 档且无宽限期（D6）, got %s %v", planID, graceEndsAt)
	}
}

func TestActivePlanPaidAndSunset(t *testing.T) {
	g := newMembershipDB(t)
	svc := NewService(g)
	user := newMembershipUser(t, g)
	now := time.Now()

	sub := model.MembershipSubscription{
		ID: uuid.New(), UserID: user.ID, PlanID: "paid", Status: "active",
		StartedAt: now.AddDate(0, 0, -10), PeriodEnd: now.AddDate(0, 0, 20),
	}
	if err := g.Create(&sub).Error; err != nil {
		t.Fatalf("写入订阅失败: %v", err)
	}

	planID, graceEndsAt, err := svc.ActivePlan(context.Background(), user.ID, now)
	if err != nil || planID != "paid" || graceEndsAt != nil {
		t.Fatalf("未到期应为 paid: planID=%s graceEndsAt=%v err=%v", planID, graceEndsAt, err)
	}

	// 过期 30 天：sunset 档，graceEndsAt = period_end + 60 天。
	if err := g.Model(&model.MembershipSubscription{}).Where("id = ?", sub.ID).
		Update("period_end", now.AddDate(0, 0, -30)).Error; err != nil {
		t.Fatalf("调整订阅时间失败: %v", err)
	}
	planID, graceEndsAt, err = svc.ActivePlan(context.Background(), user.ID, now)
	if err != nil || planID != "sunset" {
		t.Fatalf("过期 30 天应为 sunset: planID=%s err=%v", planID, err)
	}
	if graceEndsAt == nil || !graceEndsAt.After(now) {
		t.Fatalf("日落宽限期应晚于当前时间: %v", graceEndsAt)
	}
	want := now.AddDate(0, 0, -30).AddDate(0, 0, SunsetGraceDays)
	if graceEndsAt.Sub(want) > time.Minute || want.Sub(*graceEndsAt) > time.Minute {
		t.Fatalf("graceEndsAt 应为 period_end+60 天: got %v want %v", *graceEndsAt, want)
	}

	// 过期 90 天：超出宽限期，回落 free 且无宽限期。
	if err := g.Model(&model.MembershipSubscription{}).Where("id = ?", sub.ID).
		Update("period_end", now.AddDate(0, 0, -90)).Error; err != nil {
		t.Fatalf("调整订阅时间失败: %v", err)
	}
	planID, graceEndsAt, err = svc.ActivePlan(context.Background(), user.ID, now)
	if err != nil || planID != "free" || graceEndsAt != nil {
		t.Fatalf("过期 90 天应回落 free: planID=%s graceEndsAt=%v err=%v", planID, graceEndsAt, err)
	}
}

func TestGrantFromOrderStacksRenewal(t *testing.T) {
	g := newMembershipDB(t)
	svc := NewService(g)
	user := newMembershipUser(t, g)
	now := time.Now()
	planID := "paid"

	order := &model.Order{ID: uuid.New(), UserID: user.ID, PlanID: &planID}
	if err := svc.GrantFromOrder(g, order, now); err != nil {
		t.Fatalf("首次发放失败: %v", err)
	}
	var first model.MembershipSubscription
	if err := g.Where("user_id = ?", user.ID).Order("period_end DESC").First(&first).Error; err != nil {
		t.Fatalf("读取订阅失败: %v", err)
	}
	want := now.AddDate(0, 0, 30)
	if first.PeriodEnd.Sub(want) > time.Minute || want.Sub(first.PeriodEnd) > time.Minute {
		t.Fatalf("首期应为 now+30 天: got %v want %v", first.PeriodEnd, want)
	}

	// 权益期内续购：从当前 period_end 再叠 30 天，不能缩短到期时间。
	later := first.PeriodEnd.Add(-5 * 24 * time.Hour)
	renewal := &model.Order{ID: uuid.New(), UserID: user.ID, PlanID: &planID}
	if err := svc.GrantFromOrder(g, renewal, later); err != nil {
		t.Fatalf("续期失败: %v", err)
	}
	var renewed model.MembershipSubscription
	if err := g.Where("user_id = ?", user.ID).Order("period_end DESC").First(&renewed).Error; err != nil {
		t.Fatalf("读取续期订阅失败: %v", err)
	}
	wantRenewed := first.PeriodEnd.AddDate(0, 0, 30)
	if renewed.PeriodEnd.Sub(wantRenewed) > time.Minute || wantRenewed.Sub(renewed.PeriodEnd) > time.Minute {
		t.Fatalf("续期应从当前周期末叠加: got %v want %v", renewed.PeriodEnd, wantRenewed)
	}

	// 点数包订单（无 PlanID）是幂等空操作。
	if err := svc.GrantFromOrder(g, &model.Order{ID: uuid.New(), UserID: user.ID}, now); err != nil {
		t.Fatalf("点数包订单不应触发发放: %v", err)
	}
	var count int64
	g.Model(&model.MembershipSubscription{}).Where("user_id = ?", user.ID).Count(&count)
	if count != 2 {
		t.Fatalf("订阅行数应为 2, got %d", count)
	}
}

func TestSyncQuotaWritesStorageAccount(t *testing.T) {
	g := newMembershipDB(t)
	svc := NewService(g)
	user := newMembershipUser(t, g)
	now := time.Now()

	if err := svc.SyncQuotaWithin(g, user.ID, now); err != nil {
		t.Fatalf("回写免费配额失败: %v", err)
	}
	var account model.StorageAccount
	if err := g.First(&account, "user_id = ?", user.ID).Error; err != nil {
		t.Fatalf("免费配额行不存在: %v", err)
	}
	var freePlan model.MembershipPlan
	if err := g.First(&freePlan, "id = ?", "free").Error; err != nil {
		t.Fatalf("读取 free 档失败: %v", err)
	}
	if account.QuotaBytes != freePlan.StorageBytes {
		t.Fatalf("免费配额不符: got %d want %d", account.QuotaBytes, freePlan.StorageBytes)
	}

	// 发放付费订阅后配额应升档。
	planID := "paid"
	if err := svc.GrantFromOrder(g, &model.Order{ID: uuid.New(), UserID: user.ID, PlanID: &planID}, now); err != nil {
		t.Fatalf("发放付费订阅失败: %v", err)
	}
	if err := g.First(&account, "user_id = ?", user.ID).Error; err != nil {
		t.Fatalf("读取配额行失败: %v", err)
	}
	var paidPlan model.MembershipPlan
	if err := g.First(&paidPlan, "id = ?", "paid").Error; err != nil {
		t.Fatalf("读取 paid 档失败: %v", err)
	}
	if account.QuotaBytes != paidPlan.StorageBytes {
		t.Fatalf("付费配额不符: got %d want %d", account.QuotaBytes, paidPlan.StorageBytes)
	}
}

func TestGrantFromOrderRejectsUnknownPlan(t *testing.T) {
	g := newMembershipDB(t)
	svc := NewService(g)
	user := newMembershipUser(t, g)
	planID := "ghost"
	err := svc.GrantFromOrder(g, &model.Order{ID: uuid.New(), UserID: user.ID, PlanID: &planID}, time.Now())
	if err == nil || errors.Is(err, gorm.ErrRecordNotFound) == false && !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("未知档位应报错, got %v", err)
	}
}
