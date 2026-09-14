package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Local 是本地磁盘驱动，落盘根目录由 MEDIA_ROOT 指定。
// 写入先落临时文件再 rename，上传中断不会留下半截文件被后续请求当成完整对象。
type Local struct {
	root string
}

func NewLocal(root string) *Local {
	return &Local{root: root}
}

func (l *Local) Kind() string { return "local" }

// resolve 把安全的相对路径拼到根目录下，并再次确认结果没有越出根目录。
func (l *Local) resolve(path string) (string, error) {
	cleanRoot := filepath.Clean(l.root)
	dst := filepath.Join(cleanRoot, filepath.FromSlash(path))
	if dst != cleanRoot && !strings.HasPrefix(dst, cleanRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("storage: 非法对象路径 %q", path)
	}
	return dst, nil
}

func (l *Local) Put(_ context.Context, path string, r io.Reader, _ string) (int64, string, error) {
	dst, err := l.resolve(path)
	if err != nil {
		return 0, "", err
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, "", err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return 0, "", err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), r)
	if err != nil {
		cleanup()
		// 返回已读字节数：handler 据此判断是否为超过单文件上限的中断。
		return n, "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return 0, "", err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func (l *Local) Get(_ context.Context, path string) (io.ReadCloser, error) {
	dst, err := l.resolve(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(dst)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrObjectNotFound
	}
	return f, err
}

func (l *Local) Presign(context.Context, string) (Presigned, error) {
	// 本地驱动不走预签名，handler 直接回流二进制。
	return Presigned{}, nil
}

func (l *Local) Delete(_ context.Context, path string) error {
	dst, err := l.resolve(path)
	if err != nil {
		return err
	}
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (l *Local) Stat(_ context.Context, path string) (int64, error) {
	dst, err := l.resolve(path)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(dst)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, ErrObjectNotFound
	}
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
