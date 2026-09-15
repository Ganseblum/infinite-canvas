package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
)

// DeletionCoolingDays 是注销冷静期天数。余额不退，写进确认文案。
const DeletionCoolingDays = 7

// ErrNotPendingDeletion 表示未处于注销冷静期，调用方映射为 409 DELETION_NOT_PENDING。
var ErrNotPendingDeletion = errors.New("账号未处于注销冷静期")

// DeletionService 负责账号注销申请、撤销与到期匿名化。
type DeletionService struct {
	db *gorm.DB
}

func NewDeletionService(db *gorm.DB) *DeletionService { return &DeletionService{db: db} }

// Request 把账号置为 pending_deletion 并预约到期匿名化。重复申请幂等，不重置倒计时。
func (s *DeletionService) Request(ctx context.Context, userID uuid.UUID, now time.Time) (time.Time, error) {
	var user model.User
	if err := s.db.WithContext(ctx).First(&user, "id = ?", userID).Error; err != nil {
		return time.Time{}, err
	}
	if user.Status == "pending_deletion" && user.DeletionScheduledAt != nil {
		return *user.DeletionScheduledAt, nil
	}
	scheduledAt := now.AddDate(0, 0, DeletionCoolingDays)
	err := s.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"status":                "pending_deletion",
		"deletion_scheduled_at": scheduledAt,
	}).Error
	return scheduledAt, err
}

// Cancel 撤销注销申请，账号恢复 active。
func (s *DeletionService) Cancel(ctx context.Context, userID uuid.UUID) error {
	result := s.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ? AND status = ?", userID, "pending_deletion").
		Updates(map[string]any{"status": "active", "deletion_scheduled_at": nil})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotPendingDeletion
	}
	return nil
}

// AnonymizeExpired 处理到期账号：置 disabled、写入占位邮箱与用户名、清空密码哈希、
// 撤销全部 refresh token，并软删除该用户全部业务数据。返回处理数量。
func (s *DeletionService) AnonymizeExpired(ctx context.Context, now time.Time) (int, error) {
	var users []model.User
	if err := s.db.WithContext(ctx).
		Where("status = ? AND deletion_scheduled_at IS NOT NULL AND deletion_scheduled_at <= ?", "pending_deletion", now).
		Limit(100).Find(&users).Error; err != nil {
		return 0, err
	}
	count := 0
	for i := range users {
		user := &users[i]
		if err := s.anonymizeOne(ctx, user, now); err != nil {
			slog.Error("账号匿名化失败", "user", user.ID, "err", err)
			continue
		}
		count++
	}
	return count, nil
}

func (s *DeletionService) anonymizeOne(ctx context.Context, user *model.User, now time.Time) error {
	placeholderEmail := fmt.Sprintf("deleted-%s@invalid", user.ID.String())
	placeholderUsername := fmt.Sprintf("deleted-%s", user.ID.String()[:12])
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.User{}).Where("id = ?", user.ID).Updates(map[string]any{
			"status":                "disabled",
			"display_name":          "",
			"avatar_url":            "",
			"email":                 placeholderEmail,
			"username":              placeholderUsername,
			"password_hash":         "",
			"deletion_scheduled_at": nil,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.RefreshToken{}).
			Where("user_id = ? AND revoked_at IS NULL", user.ID).
			Update("revoked_at", now).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", user.ID).Delete(&model.Canvas{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", user.ID).Delete(&model.Asset{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", user.ID).Delete(&model.Generation{}).Error; err != nil {
			return err
		}
		// 媒体记录硬删除并同步扣减存储计数，磁盘对象由保留期清理任务回收。
		var mediaFiles []model.MediaFile
		if err := tx.Where("user_id = ?", user.ID).Find(&mediaFiles).Error; err != nil {
			return err
		}
		var mediaBytes int64
		for _, file := range mediaFiles {
			mediaBytes += file.Bytes
		}
		if err := tx.Where("user_id = ?", user.ID).Delete(&model.MediaFile{}).Error; err != nil {
			return err
		}
		if mediaBytes > 0 {
			if _, err := NewQuotaService(s.db).AddUsage(tx, user.ID, MetricStorageBytes, -mediaBytes); err != nil {
				return err
			}
		}
		// 邮箱/密码重置令牌随账号注销失效。
		if err := tx.Where("user_id = ?", user.ID).Delete(&model.EmailToken{}).Error; err != nil {
			return err
		}
		return nil
	})
}
