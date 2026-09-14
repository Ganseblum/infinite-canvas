package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalPutGetDeleteStat(t *testing.T) {
	root := t.TempDir()
	local := NewLocal(root)
	ctx := context.Background()
	payload := []byte("local object")

	n, checksum, err := local.Put(ctx, "user-1/image/key1", bytes.NewReader(payload), "image/png")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	want := sha256.Sum256(payload)
	if n != int64(len(payload)) || checksum != hex.EncodeToString(want[:]) {
		t.Fatalf("Put 返回值不符: n=%d checksum=%s", n, checksum)
	}
	if _, err := os.Stat(filepath.Join(root, "user-1", "image", "key1")); err != nil {
		t.Fatalf("嵌套目录未创建: %v", err)
	}

	reader, err := local.Get(ctx, "user-1/image/key1")
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	data, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("Get 内容不符: err=%v data=%q", err, data)
	}

	size, err := local.Stat(ctx, "user-1/image/key1")
	if err != nil || size != int64(len(payload)) {
		t.Fatalf("Stat 不符: size=%d err=%v", size, err)
	}

	if err := local.Delete(ctx, "user-1/image/key1"); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	if _, err := local.Get(ctx, "user-1/image/key1"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("删除后 Get 应返回 ErrObjectNotFound, got %v", err)
	}
	// 重复删除幂等
	if err := local.Delete(ctx, "user-1/image/key1"); err != nil {
		t.Fatalf("重复 Delete 应成功: %v", err)
	}
}

func TestLocalRejectsEscapingPaths(t *testing.T) {
	root := t.TempDir()
	local := NewLocal(root)
	ctx := context.Background()

	if _, _, err := local.Put(ctx, "../escape", strings.NewReader("x"), "text/plain"); err == nil {
		t.Fatal("Put ../ 应被拒绝")
	}
	if _, err := local.Get(ctx, "../escape"); err == nil {
		t.Fatal("Get ../ 应被拒绝")
	}
	if err := local.Delete(ctx, "../escape"); err == nil {
		t.Fatal("Delete ../ 应被拒绝")
	}
	if _, err := local.Stat(ctx, "../../etc/passwd"); err == nil {
		t.Fatal("Stat 越界路径应被拒绝")
	}
	// 上传中断不留下临时文件
	if _, _, err := local.Put(ctx, "user/image/key", io.MultiReader(strings.NewReader("abc"), errReader{}), "image/png"); err == nil {
		t.Fatal("读取失败时 Put 应报错")
	}
	entries, err := os.ReadDir(filepath.Join(root, "user", "image"))
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("失败上传不应留下文件: %v", entries)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read interrupted") }

func TestS3PresignAlignedSignedAndCached(t *testing.T) {
	cfg := S3Config{
		Endpoint:        "https://s3.example.com",
		Region:          "auto",
		Bucket:          "media",
		AccessKeyID:     "AKIAEXAMPLE",
		SecretAccessKey: "secret",
		ForcePathStyle:  true,
		PresignAlign:    time.Hour,
		PresignTTL:      2 * time.Hour,
	}
	s3, err := NewS3(cfg)
	if err != nil {
		t.Fatalf("NewS3 失败: %v", err)
	}
	ctx := context.Background()
	// 静态时钟：10:30 签发，对齐到 10:00
	s3.now = func() time.Time { return time.Date(2026, 9, 14, 10, 30, 0, 0, time.UTC) }
	presigned, err := s3.Presign(ctx, "user-1/image/abc")
	if err != nil {
		t.Fatalf("Presign 失败: %v", err)
	}
	u, err := url.Parse(presigned.URL)
	if err != nil {
		t.Fatalf("解析预签名 URL 失败: %v", err)
	}
	if u.Host != "s3.example.com" || u.Path != "/media/user-1/image/abc" {
		t.Fatalf("path style URL 不符: %s", presigned.URL)
	}
	q := u.Query()
	if q.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" {
		t.Fatalf("算法参数不符: %s", q.Get("X-Amz-Algorithm"))
	}
	if q.Get("X-Amz-Date") != "20260914T100000Z" {
		t.Fatalf("签发时间应对齐整点: %s", q.Get("X-Amz-Date"))
	}
	if q.Get("X-Amz-Expires") != "7200" {
		t.Fatalf("有效期应为 2h: %s", q.Get("X-Amz-Expires"))
	}
	if !strings.Contains(q.Get("X-Amz-Credential"), "AKIAEXAMPLE/20260914/auto/s3/aws4_request") {
		t.Fatalf("凭证范围不符: %s", q.Get("X-Amz-Credential"))
	}
	if q.Get("X-Amz-Signature") == "" {
		t.Fatal("预签名 URL 缺少签名")
	}
	wantExpiry := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	if !presigned.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("到期时间应为对齐点 + TTL: %s", presigned.ExpiresAt)
	}

	// 同一对齐窗口内签出完全相同的 URL（缓存键稳定）
	s3.now = func() time.Time { return time.Date(2026, 9, 14, 10, 59, 59, 0, time.UTC) }
	again, err := s3.Presign(ctx, "user-1/image/abc")
	if err != nil {
		t.Fatalf("Presign 失败: %v", err)
	}
	if again.URL != presigned.URL {
		t.Fatal("同一小时内签出的 URL 必须一致")
	}
	// 跨过对齐点后 URL 变化
	s3.now = func() time.Time { return time.Date(2026, 9, 14, 11, 0, 1, 0, time.UTC) }
	next, err := s3.Presign(ctx, "user-1/image/abc")
	if err != nil {
		t.Fatalf("Presign 失败: %v", err)
	}
	if next.URL == presigned.URL {
		t.Fatal("跨对齐点后应签出新的 URL")
	}
}

func TestS3VirtualHostStyleAndConfigValidation(t *testing.T) {
	cfg := S3Config{
		Endpoint:        "https://s3.example.com",
		Region:          "auto",
		Bucket:          "media",
		AccessKeyID:     "AKIAEXAMPLE",
		SecretAccessKey: "secret",
		PresignAlign:    time.Hour,
		PresignTTL:      2 * time.Hour,
	}
	s3, err := NewS3(cfg)
	if err != nil {
		t.Fatalf("NewS3 失败: %v", err)
	}
	s3.now = func() time.Time { return time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC) }
	presigned, err := s3.Presign(context.Background(), "user-1/image/abc")
	if err != nil {
		t.Fatalf("Presign 失败: %v", err)
	}
	u, _ := url.Parse(presigned.URL)
	if u.Host != "media.s3.example.com" || u.Path != "/user-1/image/abc" {
		t.Fatalf("virtual-hosted 风格不符: %s", presigned.URL)
	}

	// TTL 必须等于 2 × ALIGN
	bad := cfg
	bad.PresignTTL = time.Hour
	if _, err := NewS3(bad); err == nil {
		t.Fatal("TTL != 2 × ALIGN 应报错")
	}
	bad.PresignAlign = 0
	if _, err := NewS3(bad); err == nil {
		t.Fatal("ALIGN 为 0 应报错")
	}
}
