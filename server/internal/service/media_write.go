package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/infinite-canvas/server/internal/model"
	platformstorage "github.com/infinite-canvas/server/internal/platform/storage"
	"github.com/infinite-canvas/server/internal/storage"
)

// MediaWriteService 负责把生成产物写进媒体存储并记账。
// 生成结果、媒体索引、存储计数与生成记录在同一事务内落下，
// 与 PUT /api/media/{storageKey} 共用同一套写入逻辑，不另写一份。
type MediaWriteService struct {
	db      *gorm.DB
	storage storage.Storage
	usage   *platformstorage.Service
}

func NewMediaWriteService(db *gorm.DB, stor storage.Storage) *MediaWriteService {
	return &MediaWriteService{db: db, storage: stor, usage: platformstorage.NewService(db, model.ProductCanvas)}
}

// SaveGeneratedMedia 写入产物并返回 media_files 行。
// 调用方负责生成 storageKey 与 objectPath，media 行与用量计数在同一事务内更新。
type SaveGeneratedMediaInput struct {
	UserID     uuid.UUID
	StorageKey string
	MimeType   string
	Data       []byte
	MaxBytes   int64
	Moderation string
}

// ErrMediaQuotaExceeded 表示产物超过档位上限或单文件上限。
var ErrMediaQuotaExceeded = errors.New("存储空间不足")

// Save 写入对象并落 media_files 行与用量计数：同 storageKey 已有记录时按差额记账，
// 事务失败时补偿删除已写对象，不留无索引的孤儿文件。
func (s *MediaWriteService) Save(ctx context.Context, input SaveGeneratedMediaInput) (*model.MediaFile, error) {
	if input.MaxBytes > 0 && int64(len(input.Data)) > input.MaxBytes {
		return nil, ErrMediaQuotaExceeded
	}
	objectPath := storage.ObjectPath(input.UserID.String(), input.StorageKey)
	body := bytes.NewReader(input.Data)
	written, checksum, err := s.storage.Put(ctx, objectPath, body, input.MimeType)
	if err != nil {
		return nil, err
	}
	file := model.MediaFile{
		ID:               uuid.New(),
		UserID:           input.UserID,
		StorageKey:       input.StorageKey,
		ObjectPath:       objectPath,
		MimeType:         input.MimeType,
		Bytes:            written,
		Checksum:         checksum,
		ModerationStatus: defaultString(input.Moderation, "skipped"),
		CreatedAt:        time.Now(),
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var previous model.MediaFile
		delta := written
		if err := tx.Where("user_id = ? AND storage_key = ?", input.UserID, input.StorageKey).First(&previous).Error; err == nil {
			delta = written - previous.Bytes
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "storage_key"}},
			DoUpdates: clause.AssignmentColumns([]string{"object_path", "mime_type", "bytes", "checksum", "moderation_status"}),
		}).Create(&file).Error; err != nil {
			return err
		}
		if delta != 0 {
			if err := s.usage.Commit(tx, input.UserID, delta); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		_ = s.storage.Delete(ctx, objectPath)
		return nil, err
	}
	return &file, nil
}

// SaveWithGeneration 在保存产物的同一事务内追加一条生成记录，保证「一次生成恰好一条记录」。
func (s *MediaWriteService) SaveWithGeneration(ctx context.Context, input SaveGeneratedMediaInput, generation *model.Generation) (*model.MediaFile, error) {
	file, err := s.Save(ctx, input)
	if err != nil {
		return nil, err
	}
	if generation != nil {
		generation.ID = uuid.New()
		if generation.Result == nil {
			generation.Result = datatypes.JSON([]byte(`{}`))
		}
		if err := s.db.WithContext(ctx).Create(generation).Error; err != nil {
			return nil, err
		}
	}
	return file, nil
}

// WriteGeneration 单独写一条生成记录（视频任务在创建时就写 pending 记录）。
func (s *MediaWriteService) WriteGeneration(ctx context.Context, generation *model.Generation) error {
	if generation.ID == uuid.Nil {
		generation.ID = uuid.New()
	}
	if generation.Result == nil {
		generation.Result = datatypes.JSON([]byte(`{}`))
	}
	return s.db.WithContext(ctx).Create(generation).Error
}

// UpdateGeneration 收敛生成记录的状态与产物。
func (s *MediaWriteService) UpdateGeneration(ctx context.Context, id uuid.UUID, status string, result any) error {
	updates := map[string]any{"status": status}
	if result != nil {
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		updates["result"] = raw
	}
	return s.db.WithContext(ctx).Model(&model.Generation{}).Where("id = ?", id).Updates(updates).Error
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
