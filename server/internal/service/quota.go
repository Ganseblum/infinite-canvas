package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
)

const (
	MetricStorageBytes   = "storage_bytes"
	MetricFreeImageTrial = "free_image_trials"
	MetricFreeVideoTrial = "free_video_trials"
	PeriodTotal          = "total"

	sunsetGraceDays = 60

	// FreeImageTrialLimit 与 FreeVideoTrialLimit 是一次性免费额度的上限。
	// 计数落在 usage_records，只增不减。
	FreeImageTrialLimit = 3
	FreeVideoTrialLimit = 1
)

// ErrStorageQuotaExceeded 表示本次上传会超过档位存储上限，调用方映射为 507。
var ErrStorageQuotaExceeded = errors.New("存储配额不足")

// ErrReadOnly 表示实际用量超过当前派生档位上限，处于只读态，调用方映射为 402。
// 与 ErrStorageQuotaExceeded 刻意分开：一个引导充值，一个引导清理。
var ErrReadOnly = errors.New("当前处于只读状态，需要充值恢复上传")

// QuotaService 是档位派生、只读态与存储配额校验的唯一实现。
// 第二期的上传接口与本期、第四期的用量查询都复用它，不允许各自再写一份判断。
type QuotaService struct {
	db *gorm.DB
}

func NewQuotaService(db *gorm.DB) *QuotaService { return &QuotaService{db: db} }

// PlanOf 返回派生档位：购买桶非零或权益未过期为 paid；
// 权益过期 60 天内为 sunset；其余为 free。赠送桶不参与付费身份。
func PlanOf(credit model.Credit, now time.Time) string {
	if credit.PurchasedMicros > 0 {
		return "paid"
	}
	if credit.PaidUntil == nil {
		return "free"
	}
	if credit.PaidUntil.After(now) {
		return "paid"
	}
	if credit.PaidUntil.AddDate(0, 0, sunsetGraceDays).After(now) {
		return "sunset"
	}
	return "free"
}

// LoadPlan 读取档位定义表。
func (s *QuotaService) LoadPlan(ctx context.Context, planID string) (model.Plan, error) {
	var plan model.Plan
	err := s.db.WithContext(ctx).First(&plan, "id = ?", planID).Error
	return plan, err
}

// DerivePlan 读取余额并派生当前档位与其定义。
func (s *QuotaService) DerivePlan(ctx context.Context, userID uuid.UUID, now time.Time) (model.Credit, model.Plan, error) {
	var credit model.Credit
	err := s.db.WithContext(ctx).First(&credit, "user_id = ?", userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		credit = model.Credit{UserID: userID}
		err = nil
	}
	if err != nil {
		return credit, model.Plan{}, err
	}
	plan, err := s.LoadPlan(ctx, PlanOf(credit, now))
	if err != nil {
		return credit, plan, err
	}
	return credit, plan, nil
}

// StorageBytes 读取当前存储用量计数（只读路径，可能为 0）。
func (s *QuotaService) StorageBytes(ctx context.Context, userID uuid.UUID) (int64, error) {
	var record model.UsageRecord
	err := s.db.WithContext(ctx).First(&record, "user_id = ? AND metric = ? AND period = ?", userID, MetricStorageBytes, PeriodTotal).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return record.Value, err
}

// ReadOnlyState 计算只读态：实际用量超过当前派生档位上限时为真。
func (s *QuotaService) ReadOnlyState(ctx context.Context, credit model.Credit, plan model.Plan, now time.Time) (bool, error) {
	used, err := s.StorageBytes(ctx, credit.UserID)
	if err != nil {
		return false, err
	}
	return used > plan.StorageBytes, nil
}

// GraceEndsAt 返回日落期结束时间；不在日落期时为 nil。
func GraceEndsAt(credit model.Credit, now time.Time) *time.Time {
	if credit.PaidUntil == nil || credit.PaidUntil.After(now) {
		return nil
	}
	end := credit.PaidUntil.AddDate(0, 0, sunsetGraceDays)
	if end.Before(now) {
		return nil
	}
	return &end
}

// AddUsage 原子增减用量计数并返回更新后的值。
func (s *QuotaService) AddUsage(tx *gorm.DB, userID uuid.UUID, metric string, delta int64) (int64, error) {
	if delta == 0 {
		return s.usageWithin(tx, userID, metric)
	}
	updatedAt := time.Now()
	if err := tx.Exec(
		`INSERT INTO usage_records (user_id, metric, period, value, updated_at) VALUES (?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE value = value + VALUES(value), updated_at = VALUES(updated_at)`,
		userID, metric, PeriodTotal, delta, updatedAt,
	).Error; err != nil {
		// SQLite（测试库）不支持 MySQL 的 ON DUPLICATE KEY UPDATE，退回通用写法。
		if err := upsertUsagePortable(tx, userID, metric, delta, updatedAt); err != nil {
			return 0, err
		}
	}
	value, err := s.usageWithin(tx, userID, metric)
	return value, err
}

func upsertUsagePortable(tx *gorm.DB, userID uuid.UUID, metric string, delta int64, updatedAt time.Time) error {
	return tx.Exec(
		`INSERT INTO usage_records (user_id, metric, period, value, updated_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, metric, period) DO UPDATE SET value = value + excluded.value, updated_at = excluded.updated_at`,
		userID, metric, PeriodTotal, delta, updatedAt,
	).Error
}

func (s *QuotaService) usageWithin(tx *gorm.DB, userID uuid.UUID, metric string) (int64, error) {
	var record model.UsageRecord
	err := tx.First(&record, "user_id = ? AND metric = ? AND period = ?", userID, metric, PeriodTotal).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return record.Value, err
}

// FreeTrialRemaining 读取一次性免费试用计数与上限的差值。
// 计数只增不减，删除生成历史不返还额度。
func (s *QuotaService) FreeTrialRemaining(userID uuid.UUID, metric string) (int64, error) {
	limit := int64(0)
	switch metric {
	case MetricFreeImageTrial:
		limit = FreeImageTrialLimit
	case MetricFreeVideoTrial:
		limit = FreeVideoTrialLimit
	default:
		return 0, nil
	}
	var record model.UsageRecord
	err := s.db.First(&record, "user_id = ? AND metric = ? AND period = ?", userID, metric, PeriodTotal).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return limit, nil
	}
	if err != nil {
		return 0, err
	}
	remaining := limit - record.Value
	if remaining < 0 {
		return 0, nil
	}
	return remaining, nil
}

// HasGrantedFreeClaim 判断用户是否存在已批准的免费额度领取记录（差异清单 #5）。
// 消费免费次数必须同时满足「已批准领取记录 + 一次性用量计数未达上限」，
// 被风控拒绝（status=denied）或从未领取的账号不能直接享受试用。
func (s *QuotaService) HasGrantedFreeClaim(userID uuid.UUID) (bool, error) {
	var count int64
	err := s.db.Model(&model.FreeGrantClaim{}).
		Where("user_id = ? AND status = ?", userID, "granted").
		Count(&count).Error
	return count > 0, err
}

// RefundFreeTrial 条件递减一次免费试用计数：value > 0 才 -1，行不存在或已为 0 不动作。
// 生成失败时退还试用次数用（差异清单 #124），配合请求行的状态条件更新保证只退一次。
func (s *QuotaService) RefundFreeTrial(tx *gorm.DB, userID uuid.UUID, metric string) error {
	res := tx.Exec(
		`UPDATE usage_records SET value = value - 1, updated_at = ? WHERE user_id = ? AND metric = ? AND period = ? AND value > 0`,
		time.Now(), userID, metric, PeriodTotal,
	)
	return res.Error
}

// RecalculateStorage 用 media_files 的 SUM(bytes) 覆盖存储计数，用于修正人为改坏或异常中断导致的漂移。
func (s *QuotaService) RecalculateStorage(ctx context.Context, userID uuid.UUID) (int64, error) {
	var total int64
	if err := s.db.WithContext(ctx).Model(&model.MediaFile{}).
		Where("user_id = ?", userID).
		Select("COALESCE(SUM(bytes), 0)").Scan(&total).Error; err != nil {
		return 0, err
	}
	record := model.UsageRecord{
		UserID: userID,
		Metric: MetricStorageBytes,
		Period: PeriodTotal,
		Value:  total,
	}
	if err := s.db.WithContext(ctx).Save(&record).Error; err != nil {
		return 0, err
	}
	return total, nil
}

// CheckUpload 校验本次上传：先看只读态，再看增量是否超过档位上限。
// 返回的 (plan, used, limit) 供 handler 写入错误响应体，让前端能正确提示充值或清理。
func CheckUpload(used, incoming, limit int64) error {
	if used > limit {
		return ErrReadOnly
	}
	if used+incoming > limit {
		return ErrStorageQuotaExceeded
	}
	return nil
}

// StorageError 把配额错误转成带当前用量与上限的错误响应数据。
func StorageError(err error, plan model.Plan, used, incoming int64) map[string]any {
	switch {
	case errors.Is(err, ErrReadOnly):
		return map[string]any{
			"planId":  plan.ID,
			"used":    used,
			"limit":   plan.StorageBytes,
			"message": "当前用量已超过所属档位上限，请充值或清理文件后重试",
		}
	case errors.Is(err, ErrStorageQuotaExceeded):
		return map[string]any{
			"used":  used,
			"limit": plan.StorageBytes,
		}
	default:
		return nil
	}
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
	start := time.Now().UTC().Truncate(24 * time.Hour)
	err := s.db.WithContext(ctx).Model(&model.Generation{}).
		Where("user_id = ? AND created_at >= ?", userID, start).Count(&count).Error
	return count, err
}
