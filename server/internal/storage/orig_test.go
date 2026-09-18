package storage

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"testing"
	"time"
)

func TestOrigPath(t *testing.T) {
	cases := []struct {
		uid, key, want string
	}{
		{"user-1", "image:abc123", "user-1/orig/abc123"},
		{"user-1", "video-reference:abc", "user-1/orig/abc"},
		{"user-1", "nocolon", ""},
		{"user-1", "", ""},
	}
	for _, c := range cases {
		if got := OrigPath(c.uid, c.key); got != c.want {
			t.Fatalf("OrigPath(%q, %q) = %q, want %q", c.uid, c.key, got, c.want)
		}
	}
}

// TestOrigPathTraversalLock 锁死 OrigPath 的信任口径：与 ObjectPath 一致，不做二次
// 校验、原样拼接，依赖上游 storageKeyRe 与 local.resolve 兜底。若未来有人在此偷偷
// 加白名单或改拼接方式，本用例会失败以提醒同步评估调用方语义。
func TestOrigPathTraversalLock(t *testing.T) {
	if got := OrigPath("user-1", "image:../evil"); got != "user-1/orig/../evil" {
		t.Fatalf("含 .. 的 id 段应原样拼接由上游拦截, got %q", got)
	}
	if got := OrigPath("user-1", "image:a/b"); got != "user-1/orig/a/b" {
		t.Fatalf("含 / 的 id 段应原样拼接由上游拦截, got %q", got)
	}
	if got := OrigPath("", "image:abc"); got != "/orig/abc" {
		t.Fatalf("空 uid 应原样拼接由上游拦截, got %q", got)
	}
	// 兜底验证：即使恶意路径进来，local 驱动的 resolve 也不会让它越出根目录。
	local := NewLocal(t.TempDir())
	ctx := context.Background()
	if _, err := local.Stat(ctx, OrigPath("user-1", "image:../evil")); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("根目录内的错位路径应走 Stat 不存在路径, got %v", err)
	}
	if _, err := local.Stat(ctx, "../../../etc/passwd"); err == nil || errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("真越界路径必须被 resolve 拒绝, got %v", err)
	}
}

func TestLocalHasOriginal(t *testing.T) {
	local := NewLocal(t.TempDir())
	ctx := context.Background()
	key := "image:abc123"
	orig := OrigPath("user-1", key)

	// 无原件 → false；local 直接 Stat，无负缓存，写入后立即可见（自愈）。
	ok, err := HasOriginal(ctx, local, "user-1", key)
	if err != nil || ok {
		t.Fatalf("Put 前 HasOriginal 应为 false/nil, got %v/%v", ok, err)
	}
	if _, _, err := local.Put(ctx, orig, bytes.NewReader([]byte("clean")), "image/png"); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	ok, err = HasOriginal(ctx, local, "user-1", key)
	if err != nil || !ok {
		t.Fatalf("Put 后 HasOriginal 应为 true/nil, got %v/%v", ok, err)
	}
	if err := local.Delete(ctx, orig); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	ok, err = HasOriginal(ctx, local, "user-1", key)
	if err != nil || ok {
		t.Fatalf("删除后 HasOriginal 应为 false/nil, got %v/%v", ok, err)
	}
	// storageKey 无冒号：视为无原件。
	if ok, err := HasOriginal(ctx, local, "user-1", "nocolon"); ok || err != nil {
		t.Fatalf("无冒号 key 应返回 false/nil, got %v/%v", ok, err)
	}
}

// TestLocalPresignWithTTLZeroValue 锁死 local 驱动的零值语义：URL 空、ExpiresAt 零值。
func TestLocalPresignWithTTLZeroValue(t *testing.T) {
	local := NewLocal(t.TempDir())
	p, err := local.PresignWithTTL(context.Background(), "user-1/orig/abc", 360*time.Second)
	if err != nil {
		t.Fatalf("PresignWithTTL 不应报错: %v", err)
	}
	if p.URL != "" || !p.ExpiresAt.IsZero() {
		t.Fatalf("local 应返回零值 Presigned, got %+v", p)
	}
	// 既有 Presign 经委托后零值语义不变。
	p, err = local.Presign(context.Background(), "user-1/image/abc")
	if err != nil || p.URL != "" || !p.ExpiresAt.IsZero() {
		t.Fatalf("local Presign 零值语义被破坏: %+v %v", p, err)
	}
}

func TestS3PresignWithTTL(t *testing.T) {
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
	// 静态时钟取一个非对齐时刻：10:30:15。
	now := time.Date(2026, 9, 18, 10, 30, 15, 0, time.UTC)
	s3.now = func() time.Time { return now }

	p, err := s3.PresignWithTTL(ctx, "user-1/orig/abc", 360*time.Second)
	if err != nil {
		t.Fatalf("PresignWithTTL 失败: %v", err)
	}
	u, err := url.Parse(p.URL)
	if err != nil {
		t.Fatalf("解析预签名 URL 失败: %v", err)
	}
	if q := u.Query(); q.Get("X-Amz-Expires") != "360" {
		t.Fatalf("有效期应为精确 360s, got %s", q.Get("X-Amz-Expires"))
	}
	if u.Query().Get("X-Amz-Date") != "20260918T103015Z" {
		t.Fatalf("签发时间不应被对齐整点截断, got %s", u.Query().Get("X-Amz-Date"))
	}
	if want := now.Add(360 * time.Second); !p.ExpiresAt.Equal(want) {
		t.Fatalf("到期时间应为 now+360s: got %s want %s", p.ExpiresAt, want)
	}

	// 不对齐：签发时刻前进 1 秒即产生新 URL（与 Presign 的整点复用语义相反）。
	s3.now = func() time.Time { return now.Add(time.Second) }
	again, err := s3.PresignWithTTL(ctx, "user-1/orig/abc", 360*time.Second)
	if err != nil {
		t.Fatalf("PresignWithTTL 失败: %v", err)
	}
	if again.URL == p.URL {
		t.Fatal("PresignWithTTL 不应复用对齐窗口内的 URL")
	}

	// ttl <= 0 必须报错，避免签出零/负有效期的直链。
	s3.now = func() time.Time { return now }
	if _, err := s3.PresignWithTTL(ctx, "user-1/orig/abc", 0); err == nil {
		t.Fatal("ttl=0 应报错")
	}
	if _, err := s3.PresignWithTTL(ctx, "user-1/orig/abc", -time.Second); err == nil {
		t.Fatal("负 ttl 应报错")
	}
}

func TestOrigNegativeCache(t *testing.T) {
	var nilCache *origNegCache
	if nilCache.get("x") {
		t.Fatal("nil 缓存 get 应恒 false")
	}
	nilCache.add("x") // 不应 panic，静默退化为不缓存

	c := &origNegCache{}
	if c.get("p1") {
		t.Fatal("空缓存不应命中")
	}
	c.add("p1")
	if !c.get("p1") {
		t.Fatal("add 后应命中")
	}
	if c.get("p2") {
		t.Fatal("不同 key 不应命中")
	}
}

// statFake 只实现 Stat，其余方法嵌入 Storage 接口占位（不会被调用）。
type statFake struct {
	Storage
	calls int
	err   error
}

func (f *statFake) Stat(context.Context, string) (int64, error) {
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	return 1, nil
}

// TestHasOriginalCached 覆盖负缓存三态：穿透写入、命中短路、非 NotFound 不写。
// S3 真连接无法在单测注入，负缓存逻辑抽成 hasOriginalCached 后在此用假 Storage 驱动。
func TestHasOriginalCached(t *testing.T) {
	ctx := context.Background()
	const path = "user-1/orig/abc"

	// 1) Stat NotFound → 写负缓存；此后即使对象已出现，命中负缓存仍返回 false
	//    且不再 Stat——自愈路径只有进程重启，这是既定决策。
	fake := &statFake{err: ErrObjectNotFound}
	neg := &origNegCache{}
	ok, err := hasOriginalCached(ctx, fake, neg, path)
	if err != nil || ok {
		t.Fatalf("NotFound 应返回 false/nil, got %v/%v", ok, err)
	}
	fake.err = nil
	ok, err = hasOriginalCached(ctx, fake, neg, path)
	if err != nil || ok {
		t.Fatalf("命中负缓存应返回 false/nil, got %v/%v", ok, err)
	}
	if fake.calls != 1 {
		t.Fatalf("命中负缓存不应再 Stat, Stat 调用 %d 次", fake.calls)
	}

	// 2) Stat 其它错误 → 原样返回且不写负缓存（下次仍穿透）。
	fake = &statFake{err: errors.New("network down")}
	neg = &origNegCache{}
	if _, err := hasOriginalCached(ctx, fake, neg, path); err == nil {
		t.Fatal("非 NotFound 错误应原样返回")
	}
	if neg.get(path) {
		t.Fatal("非 NotFound 错误不得写负缓存")
	}

	// 3) Stat 命中 → true，且正向结论不入缓存（存在性以 Stat 实时为准）。
	fake = &statFake{}
	neg = &origNegCache{}
	ok, err = hasOriginalCached(ctx, fake, neg, path)
	if err != nil || !ok {
		t.Fatalf("Stat 命中应返回 true/nil, got %v/%v", ok, err)
	}
	if neg.get(path) {
		t.Fatal("正向结论不得入负缓存")
	}

	// 4) neg 为 nil（local 等驱动）时每次穿透直查。
	fake = &statFake{}
	ok, err = hasOriginalCached(ctx, fake, nil, path)
	if err != nil || !ok {
		t.Fatalf("无缓存路径应返回 true/nil, got %v/%v", ok, err)
	}
	if fake.calls != 1 {
		t.Fatalf("无缓存路径应恰好 Stat 一次, got %d", fake.calls)
	}
}

// TestS3HasOriginalWiring 验证 *S3 走带负缓存的分支：HasOriginal 判定走 *S3 断言，
// 负缓存实例在 NewS3 初始化。真实 S3 交互无法在单测覆盖，见 TestHasOriginalCached。
func TestS3HasOriginalWiring(t *testing.T) {
	s3, err := NewS3(S3Config{
		Endpoint: "https://s3.example.com", Region: "auto", Bucket: "media",
		AccessKeyID: "ak", SecretAccessKey: "sk",
		PresignAlign: time.Hour, PresignTTL: 2 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewS3 失败: %v", err)
	}
	if s3.origNeg == nil {
		t.Fatal("NewS3 应初始化负缓存")
	}
	// 无冒号 key 在进入驱动分支前就返回 false，不触发任何 S3 调用。
	ok, err := HasOriginal(context.Background(), s3, "user-1", "nocolon")
	if err != nil || ok {
		t.Fatalf("无冒号 key 应返回 false/nil, got %v/%v", ok, err)
	}
}
