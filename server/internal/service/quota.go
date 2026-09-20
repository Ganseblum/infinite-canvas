package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/billing"
	"github.com/infinite-canvas/server/internal/platform/membership"
)

// 试用指标与一次性额度上限已收敛到 billing 域，这里保留别名供既有调用方使用。
const (
	MetricFreeImageTrial = billing.MetricFreeImageTrial
	MetricFreeVideoTrial = billing.MetricFreeVideoTrial
	PeriodTotal          = billing.PeriodTotal

	FreeImageTrialLimit = billing.FreeImageTrialLimit
	FreeVideoTrialLimit = billing.FreeVideoTrialLimit
)

// IsDuplicateKey 转发 billing 域实现（credit.go 退役后的继任者），供既有调用方继续使用。
var IsDuplicateKey = billing.IsDuplicateKey

// QuotaService 只保留与点数、存储域无关的展示型查询：
// 媒体到期预警与今日生成计数。档位派生、存储配额与试用计数分别在
// membership / storage / billing 三域实现。
type QuotaService struct {
	db *gorm.DB
}

func NewQuotaService(db *gorm.DB) *QuotaService { return &QuotaService{db: db} }

// PlanDefFor 派生当前会员档位并读取定义，供水印闸门、单文件上限等按档位分支的逻辑使用。
// 无订阅时按 free 档解析（D6）。
func PlanDefFor(ctx context.Context, db *gorm.DB, userID uuid.UUID) (model.MembershipPlan, error) {
	m := membership.NewService(db)
	planID, _, err := m.ActivePlan(ctx, userID, time.Now())
	if err != nil {
		return model.MembershipPlan{}, err
	}
	return m.PlanDef(ctx, planID)
}

// MediaExpiry 是「最近一批到期媒体」的摘要，供 /api/me 渲染到期预警条。
type MediaExpiry struct {
	NearestAt     *time.Time
	ExpiringCount int64
}

// ExpiringMedia 按「最后触达时间 + 档位保留期」现算最近的到期媒体。
// nearestAt 是全部媒体里最早的到期时间，expiringCount 只统计未来 2 天内到期的数量。
func (s *QuotaService) ExpiringMedia(ctx context.Context, userID uuid.UUID, retentionDays int, now time.Time) (MediaExpiry, error) {
	touched, err := NewCleanupService(s.db, nil).touchedMap(ctx, userID)
	if err != nil {
		return MediaExpiry{}, err
	}
	var files []model.MediaFile
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Find(&files).Error; err != nil {
		return MediaExpiry{}, err
	}
	var expiry MediaExpiry
	window := now.AddDate(0, 0, 2)
	for _, file := range files {
		lastTouched := file.CreatedAt
		if t, ok := touched[file.StorageKey]; ok && t.After(lastTouched) {
			lastTouched = t
		}
		expires := lastTouched.AddDate(0, 0, retentionDays)
		if expiry.NearestAt == nil || expires.Before(*expiry.NearestAt) {
			at := expires
			expiry.NearestAt = &at
		}
		if expires.After(now) && expires.Before(window) {
			expiry.ExpiringCount++
		}
	}
	return expiry, nil
}

// CountGenerationsToday 统计今日生成条数（仅用于展示，不参与拦截）。
func (s *QuotaService) CountGenerationsToday(ctx context.Context, userID uuid.UUID) (int64, error) {
	var count int64
	// 按 UTC 日界取当日零点，仅作展示口径。
	start := time.Now().UTC().Truncate(24 * time.Hour)
	err := s.db.WithContext(ctx).Model(&model.Generation{}).
		Where("user_id = ? AND created_at >= ?", userID, start).Count(&count).Error
	return count, err
}
