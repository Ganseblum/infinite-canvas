package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/crypto"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/storage"
)

// QuarantineService 管理隔离区：待审核与待复核的原件只存在这里，
// 加密存储、不通过普通媒体接口暴露、到期物理删除。
type QuarantineService struct {
	db      *gorm.DB
	storage storage.Storage
	cipher  *crypto.Cipher
	ttl     time.Duration
}

func NewQuarantineService(db *gorm.DB, stor storage.Storage, cipher *crypto.Cipher, ttl time.Duration) *QuarantineService {
	return &QuarantineService{db: db, storage: stor, cipher: cipher, ttl: ttl}
}

// Quarantined 是一次写入隔离区的结果。
type Quarantined struct {
	Key        string
	ObjectPath string
	Bytes      int64
	Checksum   string
	ExpiresAt  time.Time
}

// quarantinePath 是隔离对象的存储路径，前缀独立于用户媒体命名空间。
func quarantinePath(userID uuid.UUID, key string) string {
	return "quarantine/" + userID.String() + "/" + key
}

// Put 加密写入隔离区。返回值里的 Key 只用于审核记录，不出现在任何用户接口。
func (s *QuarantineService) Put(ctx context.Context, userID uuid.UUID, data []byte, mimeType string) (Quarantined, error) {
	if s.cipher == nil {
		return Quarantined{}, errors.New("隔离区未启用加密")
	}
	key := "q-" + uuid.NewString()
	nonce, sealed, err := s.cipher.Encrypt(data)
	if err != nil {
		return Quarantined{}, err
	}
	// 密文格式：nonce || ciphertext，读取时按 nonce 长度切分。
	payload := append(append([]byte{}, nonce...), sealed...)
	path := quarantinePath(userID, key)
	written, checksum, err := s.storage.Put(ctx, path, bytes.NewReader(payload), "application/octet-stream")
	if err != nil {
		return Quarantined{}, err
	}
	return Quarantined{
		Key:        key,
		ObjectPath: path,
		Bytes:      written,
		Checksum:   checksum,
		ExpiresAt:  time.Now().Add(s.ttl),
	}, nil
}

// Get 读取并解密隔离对象。只允许管理接口调用。
func (s *QuarantineService) Get(ctx context.Context, userID uuid.UUID, key string) ([]byte, error) {
	if s.cipher == nil {
		return nil, errors.New("隔离区未启用加密")
	}
	path := quarantinePath(userID, key)
	reader, err := s.storage.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(raw) < 12 {
		return nil, errors.New("隔离对象损坏")
	}
	plaintext, err := s.cipher.Decrypt(raw[:12], raw[12:])
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

// Delete 删除隔离对象，幂等。
func (s *QuarantineService) Delete(ctx context.Context, userID uuid.UUID, key string) error {
	if key == "" {
		return nil
	}
	return s.storage.Delete(ctx, quarantinePath(userID, key))
}

// CleanupExpired 删除超过 TTL 的隔离原件，只保留审核记录的哈希与摘要。
func (s *QuarantineService) CleanupExpired(ctx context.Context, now time.Time) (int, error) {
	var records []model.ModerationRecord
	if err := s.db.WithContext(ctx).
		Where("quarantine_key <> '' AND quarantine_expires_at IS NOT NULL AND quarantine_expires_at <= ?", now).
		Limit(200).Find(&records).Error; err != nil {
		return 0, err
	}
	removed := 0
	for i := range records {
		record := &records[i]
		if err := s.Delete(ctx, record.UserID, record.QuarantineKey); err != nil {
			continue
		}
		// 原件删掉后清空 key，保留哈希与摘要。
		if err := s.db.WithContext(ctx).Model(&model.ModerationRecord{}).Where("id = ?", record.ID).
			Updates(map[string]any{"quarantine_key": ""}).Error; err != nil {
			continue
		}
		removed++
	}
	return removed, nil
}

// Publish 把隔离原件解密后写入正式媒体路径，供产物审核通过时提交。
// 返回写入的正式对象路径与字节数；调用方负责在同一逻辑单元里写 media_files。
func (s *QuarantineService) Publish(ctx context.Context, userID uuid.UUID, key string, prefix, objectID string) (string, int64, string, error) {
	data, err := s.Get(ctx, userID, key)
	if err != nil {
		return "", 0, "", err
	}
	path := userID.String() + "/" + prefix + "/" + objectID
	written, checksum, err := s.storage.Put(ctx, path, bytes.NewReader(data), "application/octet-stream")
	if err != nil {
		return "", 0, "", err
	}
	return path, written, checksum, nil
}

// Stats 汇总隔离区当前占用，供管理统计使用。
func (s *QuarantineService) Stats(ctx context.Context, now time.Time) (count int64, bytes int64, err error) {
	type row struct {
		Count int64
		Bytes int64
	}
	var result row
	err = s.db.WithContext(ctx).Model(&model.ModerationRecord{}).
		Where("quarantine_key <> '' AND (quarantine_expires_at IS NULL OR quarantine_expires_at > ?)", now).
		Select("COUNT(*) AS count, COALESCE(SUM(quarantine_bytes), 0) AS bytes").
		Scan(&result).Error
	return result.Count, result.Bytes, err
}

// PreviewURL 生成管理员专用的短期预览信息。local 驱动直接回二进制，
// s3 驱动返回预签名 URL；本函数只负责给出「有效」判断与过期时间。
func (s *QuarantineService) PreviewURL(ctx context.Context, userID uuid.UUID, key string) (string, time.Time, error) {
	if key == "" {
		return "", time.Time{}, errors.New("隔离原件已删除")
	}
	path := quarantinePath(userID, key)
	presigned, err := s.storage.Presign(ctx, path)
	if err != nil {
		return "", time.Time{}, err
	}
	if presigned.URL == "" {
		// local 驱动没有签名地址，走管理预览接口直接回流。
		return "", time.Now().Add(5 * time.Minute), nil
	}
	expires := presigned.ExpiresAt
	if expiry := time.Now().Add(5 * time.Minute); expiry.Before(expires) {
		expires = expiry
	}
	return presigned.URL, expires, nil
}
