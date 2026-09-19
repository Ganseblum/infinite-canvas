package storage

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// OrigPath 返回干净原件的对象路径：{uid}/orig/{storageKey 冒号后 id 段}。
// 无扩展名；storageKey 不含冒号时返回空串，调用方视为无原件。
//
// 安全口径与 ObjectPath 一致：本函数不做二次校验、按输入原样拼接。依据是
// storageKey 由服务端生成且必须先过 ai 域的 StorageKeyRe（internal/model/storage_key.go，
// 仅允许「类型:[A-Za-z0-9_-]{1,64}」，天然拦掉 ..、/ 与特殊字符），uid 来自鉴权后的
// 当前用户而非请求参数。即使出现越界输入，local 驱动的 resolve（local.go:29）也会
// 用根目录前缀检查拒绝越出根目录的路径，S3 侧 key 是不透明字符串、无文件系统语义。
func OrigPath(userID, storageKey string) string {
	_, id, ok := strings.Cut(storageKey, ":")
	if !ok {
		return ""
	}
	return userID + "/orig/" + id
}

// origNegCache 是 S3 驱动的「无原件」负缓存：只记录已确认不存在的结论，命中后
// HasOriginal 直接返回 false，省掉每次下发对 S3 的 HeadObject。条目随进程生命周期
// 存活，唯一自愈路径是进程重启，因此只允许对「该 key 从未有过 orig」的对象写入负
// 结论（orig 只在生成落盘时写入，先于媒体行对外可见，见计划 A 节异常矩阵）。
// 零值/nil 实例安全：get 恒 false、add 不写入，退化为每次直查。
type origNegCache struct {
	mu      sync.RWMutex
	missing map[string]struct{}
}

func (c *origNegCache) get(path string) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.missing[path]
	return ok
}

func (c *origNegCache) add(path string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.missing == nil {
		c.missing = make(map[string]struct{})
	}
	c.missing[path] = struct{}{}
}

// hasOriginalCached 是 HasOriginal 的共享实现。neg 非 nil 时启用负缓存（S3 驱动），
// nil 时每次直接 Stat（local 等驱动）。Stat 返回 ErrObjectNotFound 才写负缓存；
// 其余错误不写缓存、原样返回，避免把瞬时故障错记成「确认不存在」。
func hasOriginalCached(ctx context.Context, stor Storage, neg *origNegCache, path string) (bool, error) {
	if neg.get(path) {
		return false, nil
	}
	_, err := stor.Stat(ctx, path)
	if errors.Is(err, ErrObjectNotFound) {
		neg.add(path)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// HasOriginal 判定干净原件是否存在。S3 驱动走进程内负缓存：命中直接 false，
// 未命中 HeadObject，NotFound 写负缓存；其余错误不写缓存直接返回。
// local 驱动每次直接 Stat。storageKey 无冒号（无原件路径）时返回 false。
func HasOriginal(ctx context.Context, stor Storage, userID, storageKey string) (bool, error) {
	path := OrigPath(userID, storageKey)
	if path == "" {
		return false, nil
	}
	if s, ok := stor.(*S3); ok {
		return hasOriginalCached(ctx, s, s.origNeg, path)
	}
	return hasOriginalCached(ctx, stor, nil, path)
}
