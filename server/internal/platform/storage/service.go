// Package storage 平台存储域：storage_accounts（共享池账户）与 storage_usage
// （分产品用量）的唯一业务入口。共享池（D1）：配额来自当前会员档位，用量按产品分账。
// 错误语义延续现状：ErrReadOnly → 403 READ_ONLY、ErrQuotaExceeded → 507。
package storage

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/infinite-canvas/server/internal/model"
)

// ErrQuotaExceeded 表示本次写入会超过配额上限，调用方映射为 507。
var ErrQuotaExceeded = errors.New("存储配额不足")

// ErrReadOnly 表示实际用量超过当前配额，处于只读态，调用方映射为 403 READ_ONLY。
// 与 ErrQuotaExceeded 刻意分开：一个引导充值，一个引导清理。
var ErrReadOnly = errors.New("当前处于只读状态，需要充值恢复上传")

// Service 平台存储域服务。
type Service struct {
	db      *gorm.DB
	product string
}

func NewService(db *gorm.DB, product string) *Service { return &Service{db: db, product: product} }

// EnsureAccount 为新用户建零值账户行（配额由调用方按注册时档位传入）。
func (s *Service) EnsureAccount(tx *gorm.DB, userID uuid.UUID, quotaBytes int64) error {
	return tx.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&model.StorageAccount{UserID: userID, QuotaBytes: quotaBytes}).Error
}

// Snapshot 读取当前用量与配额（共享池口径：跨产品聚合）。
func (s *Service) Snapshot(ctx context.Context, userID uuid.UUID) (used int64, quota int64, err error) {
	var account model.StorageAccount
	if err := s.db.WithContext(ctx).First(&account, "user_id = ?", userID).Error; err != nil {
		return 0, 0, err
	}
	return account.UsedBytes, account.QuotaBytes, nil
}

// Check 校验本次写入增量：先看只读态，再看增量是否超过配额上限。
// 只读不记账；调用方映射 403/507 时沿用原响应体形状。
func (s *Service) Check(ctx context.Context, userID uuid.UUID, deltaBytes int64) error {
	used, quota, err := s.Snapshot(ctx, userID)
	if err != nil {
		return err
	}
	if used > quota {
		return ErrReadOnly
	}
	if used+deltaBytes > quota {
		return ErrQuotaExceeded
	}
	return nil
}

// Commit 在调用方事务内记账：分产品用量与共享池聚合同事务更新。
// 幂等性由调用方的业务行（media_files）保证，本层只做增量记账。
func (s *Service) Commit(tx *gorm.DB, userID uuid.UUID, deltaBytes int64) error {
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "product"}},
		DoUpdates: clause.Assignments(map[string]any{"bytes": gorm.Expr("bytes + ?", deltaBytes)}),
	}).Create(&model.StorageUsage{UserID: userID, Product: s.product, Bytes: deltaBytes}).Error; err != nil {
		return err
	}
	return tx.Model(&model.StorageAccount{}).Where("user_id = ?", userID).
		Update("used_bytes", gorm.Expr("used_bytes + ?", deltaBytes)).Error
}

// Recalculate 用 media_files 的 SUM(bytes) 覆盖分产品用量，并把共享池聚合
// 重算为全部分产品用量之和，用于修正人为改坏或异常中断导致的漂移
// （夜间对账任务的修正动作，天然幂等）。返回本产品重算后的用量。
func (s *Service) Recalculate(ctx context.Context, userID uuid.UUID) (int64, error) {
	var total int64
	if err := s.db.WithContext(ctx).Model(&model.MediaFile{}).
		Where("user_id = ? AND product = ?", userID, s.product).
		Select("COALESCE(SUM(bytes), 0)").Scan(&total).Error; err != nil {
		return 0, err
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "product"}},
			DoUpdates: clause.Assignments(map[string]any{"bytes": total}),
		}).Create(&model.StorageUsage{UserID: userID, Product: s.product, Bytes: total}).Error; err != nil {
			return err
		}
		return tx.Exec(
			`UPDATE storage_accounts SET used_bytes = (SELECT COALESCE(SUM(bytes), 0) FROM storage_usage WHERE user_id = ?) WHERE user_id = ?`,
			userID, userID,
		).Error
	})
	return total, err
}
