package service

import (
	"context"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/identity"
	"github.com/infinite-canvas/server/internal/storage"
)

// DeletionService 编排账号注销到期处理：身份侧行更新走 identity 域（同事务回调），
// 业务数据清理留在本服务，媒体对象在事务提交后 best-effort 删除。
type DeletionService struct {
	db       *gorm.DB
	identity *identity.Service
	// storage 用于匿名化提交后删除用户的媒体对象本体；未注入时只清索引
	// （后台任务接线时传入，与媒体接口共用同一驱动）。
	storage storage.Storage
}

func NewDeletionService(db *gorm.DB, stor ...storage.Storage) *DeletionService {
	s := &DeletionService{db: db, identity: identity.NewService(db)}
	if len(stor) > 0 {
		s.storage = stor[0]
	}
	return s
}

// AnonymizeExpired 处理到期账号：身份匿名化与业务数据清理共享事务，
// 提交后逐个删除媒体对象与干净原件。返回处理数量。
func (s *DeletionService) AnonymizeExpired(ctx context.Context, now time.Time) (int, error) {
	type mediaObject struct {
		userID     string
		storageKey string
		objectPath string
		origPath   string
	}
	objects := make([]mediaObject, 0, 8)
	n, err := s.identity.AnonymizeExpired(ctx, now, func(tx *gorm.DB, user *model.PlatformUser) error {
		// 媒体记录硬删除并同步扣减存储计数；对象与干净原件（orig）路径在事务内收集，
		// 提交后再逐个删除（行删掉后保留期清理任务按 media_files 扫描，永远扫不到这些对象）。
		var mediaFiles []model.MediaFile
		if err := tx.Where("user_id = ?", user.ID).Find(&mediaFiles).Error; err != nil {
			return err
		}
		var mediaBytes int64
		for _, file := range mediaFiles {
			mediaBytes += file.Bytes
			objects = append(objects, mediaObject{
				userID:     user.ID.String(),
				storageKey: file.StorageKey,
				objectPath: file.ObjectPath,
				origPath:   storage.OrigPath(user.ID.String(), file.StorageKey),
			})
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
		// 注销时下架该用户全部社区作品，公开流不再展示其内容。
		if err := tx.Model(&model.CommunityWork{}).Where("user_id = ?", user.ID).
			Update("status", "removed").Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", user.ID).Delete(&model.MediaFile{}).Error; err != nil {
			return err
		}
		// 邮箱/密码重置令牌随账号注销失效。
		if err := tx.Where("user_id = ?", user.ID).Delete(&model.EmailToken{}).Error; err != nil {
			return err
		}
		if mediaBytes > 0 {
			if _, err := NewQuotaService(s.db).AddUsage(tx, user.ID, MetricStorageBytes, -mediaBytes); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return n, err
	}
	// 事务提交后再删对象。单个对象删除失败只记日志、不阻塞匿名化：
	// 失败回滚会把用户重新拉回注销流程，对象残留好过误删或中断。
	if s.storage != nil {
		for _, obj := range objects {
			if err := s.storage.Delete(ctx, obj.objectPath); err != nil {
				slog.Error("注销清理媒体对象失败", "userId", obj.userID, "path", obj.objectPath, "err", err)
				continue
			}
			// 连带删除干净原件（orig）：best-effort，失败只记日志、不影响主删除结果；
			// storageKey 无冒号（无原件路径）时直接跳过。
			if obj.origPath == "" {
				continue
			}
			if err := s.storage.Delete(ctx, obj.origPath); err != nil {
				slog.Error("注销清理干净原件失败", "userId", obj.userID, "storageKey", obj.storageKey, "err", err)
			}
		}
	}
	return n, nil
}
