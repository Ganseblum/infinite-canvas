package handler

import "sync"

// concurrencySlots 是进程内的用户并发槽位。
// 单实例部署够用；横向扩展时按第一期的结论换 Redis。
type concurrencySlots struct {
	mu    sync.Mutex
	inUse map[string]int
}

func newConcurrencySlots() *concurrencySlots {
	return &concurrencySlots{inUse: map[string]int{}}
}

func slotKey(capability, userID string) string { return capability + ":" + userID }

// acquire 尝试占用一个槽位，超过上限返回 false。
func (s *concurrencySlots) acquire(capability, userID string, limit int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := slotKey(capability, userID)
	if s.inUse[key] >= limit {
		return false
	}
	s.inUse[key]++
	return true
}

// release 释放槽位。必须放在 defer 里，流式请求在任何时刻断开都不会泄漏。
func (s *concurrencySlots) release(capability, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := slotKey(capability, userID)
	if s.inUse[key] <= 0 {
		return
	}
	s.inUse[key]--
	if s.inUse[key] == 0 {
		delete(s.inUse, key)
	}
}

// count 返回当前占用数，供测试验证槽位没有泄漏。
func (s *concurrencySlots) count(capability, userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inUse[slotKey(capability, userID)]
}
