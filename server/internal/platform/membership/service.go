// Package membership 平台会员域：membership_plans 与 membership_subscriptions 的
// 唯一业务入口。平台会员（D2）：一次订阅全产品生效；付费身份由 period_end 表达，
// 到期后 60 天内为日落宽限（D6 拍板：只有点数无订阅回落 free 档但可消费）。
package membership

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/infinite-canvas/server/internal/model"
)

// SunsetGraceDays 是订阅到期后的日落宽限天数（graceEndsAt = period_end + 60 天）。
const SunsetGraceDays = 60

// ErrPlanNotFound 表示会员档位不存在或未启用。
var ErrPlanNotFound = errors.New("会员档位不存在或未启用")

// Service 平台会员域服务。
type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

// ActivePlan 派生当前会员档位：最新订阅 period_end 未到为 paid；
// 到期后 60 天宽限内为 sunset（graceEndsAt = period_end + 60 天）；其余 free。
// 无订阅时回落 free 档（D6：只有点数、无有效订阅仍可消费）。
func (s *Service) ActivePlan(ctx context.Context, userID uuid.UUID, now time.Time) (planID string, graceEndsAt *time.Time, err error) {
	return activePlanWithin(s.db.WithContext(ctx), userID, now)
}

// activePlanWithin 在给定的 db/事务句柄上派生档位，供事务内编排复用。
func activePlanWithin(tx *gorm.DB, userID uuid.UUID, now time.Time) (planID string, graceEndsAt *time.Time, err error) {
	var sub model.MembershipSubscription
	err = tx.
		Where("user_id = ?", userID).
		Order("period_end DESC, created_at DESC").
		First(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "free", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	if sub.PeriodEnd.After(now) {
		return sub.PlanID, nil, nil
	}
	end := sub.PeriodEnd.AddDate(0, 0, SunsetGraceDays)
	if end.After(now) {
		return "sunset", &end, nil
	}
	return "free", nil, nil
}

// PlanDef 读取会员档位定义。
func (s *Service) PlanDef(ctx context.Context, planID string) (model.MembershipPlan, error) {
	var plan model.MembershipPlan
	err := s.db.WithContext(ctx).First(&plan, "id = ?", planID).Error
	return plan, err
}

// GrantFromOrder 在调用方事务内按订单发放/续期会员：从 max(当前 period_end, now) 起叠一个
// 档位周期，避免权益期内再购买反而缩短到期时间。非会员订单（PlanID 为空）是幂等空操作。
func (s *Service) GrantFromOrder(tx *gorm.DB, order *model.Order, now time.Time) error {
	if order.PlanID == nil || *order.PlanID == "" {
		return nil
	}
	return s.grant(tx, order.UserID, *order.PlanID, now, order.ID.String())
}

// Compensate 管理员补偿发放会员（不经过订单），reason 记入来源。
func (s *Service) Compensate(tx *gorm.DB, userID uuid.UUID, planID string, reason string) error {
	return s.grant(tx, userID, planID, time.Now(), reason)
}

func (s *Service) grant(tx *gorm.DB, userID uuid.UUID, planID string, now time.Time, sourceRef string) error {
	var plan model.MembershipPlan
	if err := tx.First(&plan, "id = ?", planID).Error; err != nil {
		return err
	}
	if plan.DurationDays <= 0 {
		return ErrPlanNotFound
	}
	// 从该用户当前最晚的 period_end（未过期）往后叠，否则从 now 起。
	base := now
	var latest model.MembershipSubscription
	err := tx.Where("user_id = ?", userID).Order("period_end DESC").First(&latest).Error
	if err == nil && latest.PeriodEnd.After(now) {
		base = latest.PeriodEnd
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	sub := model.MembershipSubscription{
		ID:        uuid.New(),
		UserID:    userID,
		PlanID:    planID,
		Status:    "active",
		StartedAt: now,
		PeriodEnd: base.AddDate(0, 0, plan.DurationDays),
		SourceRef: sourceRef,
	}
	if err := tx.Create(&sub).Error; err != nil {
		return err
	}
	return s.SyncQuotaWithin(tx, userID, now)
}

// SyncQuota 把当前档位的共享池配额回写到 storage_accounts.quota（独立调用）。
// 档位配额变更或订阅状态变化后调用，保证配额与档位一致。
func (s *Service) SyncQuota(ctx context.Context, userID uuid.UUID) error {
	return s.SyncQuotaWithin(s.db.WithContext(ctx), userID, time.Now())
}

// SyncQuotaWithin 在调用方事务内回写共享池配额（SQLite/MySQL 通用 upsert）。
func (s *Service) SyncQuotaWithin(tx *gorm.DB, userID uuid.UUID, now time.Time) error {
	planID, _, err := activePlanWithin(tx, userID, now)
	if err != nil {
		return err
	}
	var plan model.MembershipPlan
	if err := tx.First(&plan, "id = ?", planID).Error; err != nil {
		return err
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{"quota_bytes": plan.StorageBytes}),
	}).Create(&model.StorageAccount{UserID: userID, QuotaBytes: plan.StorageBytes}).Error
}
