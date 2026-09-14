package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrObjectNotFound 表示对象在存储侧不存在。业务层据此决定 404 还是 500。
var ErrObjectNotFound = errors.New("storage: object not found")

// Presigned 是一次预签名读取的结果。local 驱动没有签名问题，URL 为空，
// ExpiresAt 为零值，调用方（media handler）据此直接回流二进制。
type Presigned struct {
	URL       string
	ExpiresAt time.Time
}

// Storage 是媒体对象存储的最小接口，local 与 s3 两种驱动遵循同一契约。
// path 由 handler 按「用户 id/类型段/对象 id」组装后传入，接口层不关心 key 格式。
type Storage interface {
	// Kind 返回驱动标识：local | s3。media handler 据此选择直读或 302。
	Kind() string
	// Put 流式写入对象，返回写入字节数与服务端计算的 sha256 十六进制摘要。
	Put(ctx context.Context, path string, r io.Reader, contentType string) (bytes int64, checksum string, err error)
	// Get 打开对象读取流，对象不存在时返回 ErrObjectNotFound。
	Get(ctx context.Context, path string) (io.ReadCloser, error)
	// Presign 生成读路径。s3 返回对齐过整点的预签名 URL 与到期时间；
	// local 返回零值，表示由 handler 直接回流。
	Presign(ctx context.Context, path string) (Presigned, error)
	// Delete 删除对象，对象不存在视为成功（幂等）。
	Delete(ctx context.Context, path string) error
	// Stat 返回对象字节数，对象不存在时返回 ErrObjectNotFound。
	Stat(ctx context.Context, path string) (int64, error)
}
