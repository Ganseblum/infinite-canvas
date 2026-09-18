package service

import (
	"time"

	"github.com/google/uuid"

	"github.com/infinite-canvas/server/internal/cursor"
)

// 游标实现已收敛到 internal/cursor 供各域共用，这里保留原签名作兼容转发。

// ErrInvalidCursor 表示游标无法解码，调用方映射为 400 VALIDATION_FAILED。
var ErrInvalidCursor = cursor.ErrInvalidCursor

// EncodeCursor 生成对客户端不透明的游标：base64url(created_at(RFC3339Nano)|id)。
func EncodeCursor(createdAt time.Time, id uuid.UUID) string { return cursor.Encode(createdAt, id) }

// DecodeCursor 解析游标，形状不符或时间无法解析时返回 ErrInvalidCursor。
func DecodeCursor(cursorStr string) (time.Time, uuid.UUID, error) { return cursor.Decode(cursorStr) }
