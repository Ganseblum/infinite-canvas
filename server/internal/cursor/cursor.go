// Package cursor 通用游标分页：对客户端不透明的 created_at|id 游标。
// 独立成包供 service 与 platform 各域共用，避免互相依赖。
package cursor

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidCursor 表示游标无法解码，调用方映射为 400 VALIDATION_FAILED。
var ErrInvalidCursor = errors.New("游标无法解码")

// Encode 生成对客户端不透明的游标：base64url(created_at(RFC3339Nano)|id)。
func Encode(createdAt time.Time, id uuid.UUID) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// Decode 解析游标，形状不符或时间无法解析时返回 ErrInvalidCursor。
func Decode(cursor string) (time.Time, uuid.UUID, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	createdRaw, idRaw, ok := strings.Cut(string(decoded), "|")
	if !ok {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	id, err := uuid.Parse(idRaw)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	return createdAt, id, nil
}
