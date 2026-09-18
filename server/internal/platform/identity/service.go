// Package identity 平台身份域：platform_users 与 sessions 的唯一业务入口。
// 认证、授权、管理端对身份状态的读写一律经由本域，禁止按表名直查，
// 表名漂移只可能发生在本包内。
package identity

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

// CleanupFunc 是匿名化事务内的业务数据清理回调：身份侧行更新完成后、
// 事务提交前调用，与身份变更共享同一事务。
type CleanupFunc func(tx *gorm.DB, user *model.PlatformUser) error

// Service 平台身份域服务。
type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

// ===== 读 =====

// GetByID 按主键读取平台账号。
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*model.PlatformUser, error) {
	var user model.PlatformUser
	if err := s.db.WithContext(ctx).First(&user, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// GetByEmail 按邮箱读取平台账号（登录查号用，邮箱入库时已统一小写）。
func (s *Service) GetByEmail(ctx context.Context, email string) (*model.PlatformUser, error) {
	var user model.PlatformUser
	if err := s.db.WithContext(ctx).First(&user, "email = ?", email).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// GetByUsername 按用户名读取平台账号（登录查号用，保留原始大小写）。
func (s *Service) GetByUsername(ctx context.Context, username string) (*model.PlatformUser, error) {
	var user model.PlatformUser
	if err := s.db.WithContext(ctx).First(&user, "username = ?", username).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// MediaTokenVersion 读取媒体令牌版本号，用于 ic_media cookie 校验。
func (s *Service) MediaTokenVersion(ctx context.Context, id uuid.UUID) (int, error) {
	var ver int
	err := s.db.WithContext(ctx).Model(&model.PlatformUser{}).Select("media_token_version").
		Where("id = ?", id).Scan(&ver).Error
	return ver, err
}

// ===== 会话 =====

// CreateSession 落一条会话记录（注册/登录/改密后重签）。
func (s *Service) CreateSession(ctx context.Context, session *model.Session) error {
	return s.db.WithContext(ctx).Create(session).Error
}

// GetSessionByHash 按令牌哈希读取会话。
func (s *Service) GetSessionByHash(ctx context.Context, tokenHash string) (*model.Session, error) {
	var session model.Session
	if err := s.db.WithContext(ctx).First(&session, "token_hash = ?", tokenHash).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

// RevokeSessionByHash 按令牌哈希吊销会话（登出）。
func (s *Service) RevokeSessionByHash(ctx context.Context, tokenHash string, now time.Time) error {
	return s.db.WithContext(ctx).Model(&model.Session{}).
		Where("token_hash = ?", tokenHash).Update("revoked_at", now).Error
}

// RevokeSessionIfActive 原子消费一个未吊销会话：并发轮换时只有一个请求置位成功。
// 返回 false 表示会话已被其他请求轮换（竞争失败方），不能按复用处理。
func (s *Service) RevokeSessionIfActive(ctx context.Context, sessionID uuid.UUID, now time.Time) (bool, error) {
	res := s.db.WithContext(ctx).Model(&model.Session{}).
		Where("id = ? AND revoked_at IS NULL", sessionID).
		Update("revoked_at", now)
	return res.RowsAffected > 0, res.Error
}

// RevokeSessions 吊销用户全部未吊销会话（独立调用）。
func (s *Service) RevokeSessions(ctx context.Context, userID uuid.UUID, now time.Time) error {
	return s.RevokeSessionsTx(s.db.WithContext(ctx), userID, now)
}

// RevokeSessionsTx 在调用方事务内吊销用户全部未吊销会话。
func (s *Service) RevokeSessionsTx(tx *gorm.DB, userID uuid.UUID, now time.Time) error {
	return tx.Model(&model.Session{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", now).Error
}

// ===== 安全事件 =====

// BumpMediaTokenVersion 递增媒体令牌版本，旧 ic_media cookie 立即失效（差异清单 #9）。
func (s *Service) BumpMediaTokenVersion(ctx context.Context, userID uuid.UUID) error {
	return s.BumpMediaTokenVersionTx(s.db.WithContext(ctx), userID)
}

// BumpMediaTokenVersionTx 在调用方事务内递增媒体令牌版本。
func (s *Service) BumpMediaTokenVersionTx(tx *gorm.DB, userID uuid.UUID) error {
	return tx.Model(&model.PlatformUser{}).Where("id = ?", userID).
		UpdateColumn("media_token_version", gorm.Expr("media_token_version + 1")).Error
}

// SetStatus 更新账号状态（active | disabled | pending_deletion）。
func (s *Service) SetStatus(ctx context.Context, userID uuid.UUID, status string) error {
	return s.SetStatusTx(s.db.WithContext(ctx), userID, status)
}

// SetStatusTx 在调用方事务内更新账号状态。
func (s *Service) SetStatusTx(tx *gorm.DB, userID uuid.UUID, status string) error {
	return tx.Model(&model.PlatformUser{}).Where("id = ?", userID).Update("status", status).Error
}

// ===== 注销 =====

// RequestDeletion 把账号置为 pending_deletion 并预约到期匿名化。
// 重复申请幂等，不重置倒计时。
func (s *Service) RequestDeletion(ctx context.Context, userID uuid.UUID, now time.Time) (time.Time, error) {
	user, err := s.GetByID(ctx, userID)
	if err != nil {
		return time.Time{}, err
	}
	if user.Status == "pending_deletion" && user.DeletionScheduledAt != nil {
		return *user.DeletionScheduledAt, nil
	}
	scheduledAt := now.AddDate(0, 0, DeletionCoolingDays)
	err = s.db.WithContext(ctx).Model(&model.PlatformUser{}).Where("id = ?", userID).Updates(map[string]any{
		"status":                "pending_deletion",
		"deletion_scheduled_at": scheduledAt,
	}).Error
	return scheduledAt, err
}

// CancelDeletion 撤销注销申请，账号恢复 active；未处于冷静期返回 ErrNotPendingDeletion。
func (s *Service) CancelDeletion(ctx context.Context, userID uuid.UUID) error {
	result := s.db.WithContext(ctx).Model(&model.PlatformUser{}).
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

// AnonymizeExpired 处理到期账号：身份侧（置 disabled、写入占位邮箱与用户名、清空密码哈希、
// 吊销并抹除全部会话痕迹）与业务数据清理（cleanup 回调）共享一个事务。返回事务已成功
// 提交的账号 id 列表——媒体对象等物理删除只允许针对列表内的账号执行，防止事务失败
// 后对象被误删。单个账号失败只记日志、不阻塞其余账号。
func (s *Service) AnonymizeExpired(ctx context.Context, now time.Time, cleanup CleanupFunc) ([]uuid.UUID, error) {
	var users []model.PlatformUser
	if err := s.db.WithContext(ctx).
		Where("status = ? AND deletion_scheduled_at IS NOT NULL AND deletion_scheduled_at <= ?", "pending_deletion", now).
		Limit(100).Find(&users).Error; err != nil {
		return nil, err
	}
	processed := make([]uuid.UUID, 0, len(users))
	for i := range users {
		if err := s.anonymizeOne(ctx, &users[i], now, cleanup); err != nil {
			slog.Error("账号匿名化失败", "user", users[i].ID, "err", err)
			continue
		}
		processed = append(processed, users[i].ID)
	}
	return processed, nil
}

func (s *Service) anonymizeOne(ctx context.Context, user *model.PlatformUser, now time.Time, cleanup CleanupFunc) error {
	placeholderEmail := fmt.Sprintf("deleted-%s@invalid", user.ID.String())
	placeholderUsername := fmt.Sprintf("deleted-%s", user.ID.String()[:12])
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.PlatformUser{}).Where("id = ?", user.ID).Updates(map[string]any{
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
		// 吊销全部未吊销会话；会话痕迹匿名化：全部令牌的 IP/UA 置空，
		// 只保留吊销时间供审计（差异清单 #114）。
		if err := tx.Model(&model.Session{}).
			Where("user_id = ? AND revoked_at IS NULL", user.ID).
			Update("revoked_at", now).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Session{}).
			Where("user_id = ?", user.ID).
			Updates(map[string]any{"ip": "", "user_agent": ""}).Error; err != nil {
			return err
		}
		if cleanup == nil {
			return nil
		}
		return cleanup(tx, user)
	})
}
