package service

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/storage"
)

// CleanupService 把孤儿回收与保留期清理合并为同一个扫描任务。
// 两件事的输入完全一样（都要收集画布、素材、生成记录引用到的 storage_key），
// 分开写两套引用收集逻辑早晚会不一致。
type CleanupService struct {
	db      *gorm.DB
	storage storage.Storage
}

func NewCleanupService(db *gorm.DB, stor storage.Storage) *CleanupService {
	return &CleanupService{db: db, storage: stor}
}

// MediaRef 是一条待清理的媒体记录与其清理原因。
type MediaRef struct {
	StorageKey string `json:"storageKey"`
	Bytes      int64  `json:"bytes"`
	Reason     string `json:"reason"` // orphan | expired | over_quota
}

// CleanupReport 是一次扫描或执行的汇总。
type CleanupReport struct {
	UserID       string     `json:"userId"`
	DryRun       bool       `json:"dryRun"`
	Scanned      int        `json:"scanned"`
	Reclaimed    int        `json:"reclaimed"`
	FreedBytes   int64      `json:"freedBytes"`
	StorageUsed  int64      `json:"storageUsed"`
	StorageLimit int64      `json:"storageLimit"`
	Items        []MediaRef `json:"items"`
}

// Reclaim 扫描并（dry-run 时只列出）清理该用户的孤儿与过期媒体，
// 再按日落降档规则继续清理直到用量落回指定上限内。
// 完成后重算存储用量，保证计数与实际记录一致。
func (s *CleanupService) Reclaim(ctx context.Context, userID uuid.UUID, retentionDays int, limitBytes int64, now time.Time, dryRun bool) (CleanupReport, error) {
	report := CleanupReport{UserID: userID.String(), DryRun: dryRun, StorageLimit: limitBytes}
	touched, err := s.touchedMap(ctx, userID)
	if err != nil {
		return report, err
	}

	var files []model.MediaFile
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Find(&files).Error; err != nil {
		return report, err
	}
	report.Scanned = len(files)

	// 按最后触达时间从旧到新排序：孤儿没有触达时间，退化为媒体创建时间。
	type entry struct {
		file     model.MediaFile
		lastSeen time.Time
		orphan   bool
	}
	entries := make([]entry, 0, len(files))
	var totalBytes int64
	for _, file := range files {
		lastSeen, referenced := touched[file.StorageKey]
		if !referenced {
			lastSeen = file.CreatedAt
		}
		entries = append(entries, entry{file: file, lastSeen: lastSeen, orphan: !referenced})
		totalBytes += file.Bytes
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].lastSeen.Equal(entries[j].lastSeen) {
			return entries[i].file.CreatedAt.Before(entries[j].file.CreatedAt)
		}
		return entries[i].lastSeen.Before(entries[j].lastSeen)
	})

	expiryLine := now.AddDate(0, 0, -retentionDays)
	var selected []MediaRef
	selectedKeys := map[string]bool{}
	for _, item := range entries {
		reason := ""
		switch {
		case item.orphan:
			reason = "orphan"
		case item.lastSeen.Before(expiryLine):
			reason = "expired"
		}
		if reason != "" {
			selected = append(selected, MediaRef{StorageKey: item.file.StorageKey, Bytes: item.file.Bytes, Reason: reason})
			selectedKeys[item.file.StorageKey] = true
		}
	}
	// 日落期满的降档清理：从最旧的开始继续删，直到落回上限以内，不是删光。
	freed := int64(0)
	for _, item := range selected {
		freed += item.Bytes
	}
	for _, item := range entries {
		if totalBytes-freed <= limitBytes {
			break
		}
		if selectedKeys[item.file.StorageKey] {
			continue
		}
		selected = append(selected, MediaRef{StorageKey: item.file.StorageKey, Bytes: item.file.Bytes, Reason: "over_quota"})
		selectedKeys[item.file.StorageKey] = true
		freed += item.file.Bytes
	}

	report.Items = selected
	if !dryRun {
		for _, item := range selected {
			var file model.MediaFile
			if err := s.db.WithContext(ctx).
				Where("user_id = ? AND storage_key = ?", userID, item.StorageKey).
				First(&file).Error; err != nil {
				slog.Warn("清理时读取媒体记录失败", "storageKey", item.StorageKey, "err", err)
				continue
			}
			if err := s.storage.Delete(ctx, file.ObjectPath); err != nil {
				// 对象删除失败时保留数据库记录，下次扫描会再次尝试，不静默丢账。
				slog.Error("删除媒体对象失败", "storageKey", item.StorageKey, "err", err)
				continue
			}
			// 连带删除干净原件（orig）：best-effort，失败只记日志、不影响主删除结果；
			// storageKey 无冒号（无原件路径）时直接跳过。
			if origPath := storage.OrigPath(userID.String(), file.StorageKey); origPath != "" {
				if err := s.storage.Delete(ctx, origPath); err != nil {
					slog.Error("删除媒体干净原件失败", "user", userID, "storageKey", item.StorageKey, "err", err)
				}
			}
			if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := tx.Where("id = ?", file.ID).Delete(&model.MediaFile{}).Error; err != nil {
					return err
				}
				_, err := NewQuotaService(s.db).AddUsage(tx, userID, MetricStorageBytes, -file.Bytes)
				return err
			}); err != nil {
				slog.Error("清理媒体记录失败", "storageKey", item.StorageKey, "err", err)
				continue
			}
			report.Reclaimed++
			report.FreedBytes += file.Bytes
		}
	} else {
		report.Reclaimed = len(selected)
		report.FreedBytes = freed
	}

	used, err := NewQuotaService(s.db).RecalculateStorage(ctx, userID)
	if err == nil {
		report.StorageUsed = used
	}
	return report, nil
}

// touchedMap 收集该用户全部引用到的 storageKey 及其最后触达时间。
// 软删除的记录同样参与计算：deleted_at 不为空的画布仍可能是用户想恢复的。
func (s *CleanupService) touchedMap(ctx context.Context, userID uuid.UUID) (map[string]time.Time, error) {
	touched := map[string]time.Time{}
	touch := func(key string, at time.Time) {
		if key == "" {
			return
		}
		if current, ok := touched[key]; !ok || at.After(current) {
			touched[key] = at
		}
	}

	var canvases []model.Canvas
	if err := s.db.WithContext(ctx).Unscoped().
		Select("id", "data", "updated_at").Where("user_id = ?", userID).Find(&canvases).Error; err != nil {
		return nil, err
	}
	for _, canvas := range canvases {
		for _, key := range ExtractStorageKeys(canvas.Data) {
			touch(key, canvas.UpdatedAt)
		}
	}

	var assets []model.Asset
	if err := s.db.WithContext(ctx).Unscoped().
		Select("id", "data", "storage_key", "updated_at").Where("user_id = ?", userID).Find(&assets).Error; err != nil {
		return nil, err
	}
	for _, asset := range assets {
		touch(asset.StorageKey, asset.UpdatedAt)
		for _, key := range ExtractStorageKeys(asset.Data) {
			touch(key, asset.UpdatedAt)
		}
	}

	var generations []model.Generation
	if err := s.db.WithContext(ctx).Unscoped().
		Select("id", "config", "result", "created_at").Where("user_id = ?", userID).Find(&generations).Error; err != nil {
		return nil, err
	}
	for _, generation := range generations {
		for _, key := range ExtractStorageKeys(generation.Config) {
			touch(key, generation.CreatedAt)
		}
		for _, key := range ExtractStorageKeys(generation.Result) {
			touch(key, generation.CreatedAt)
		}
	}
	return touched, nil
}
