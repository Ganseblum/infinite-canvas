package ai

import (
	"os"
	"strconv"
	"sync"
	"time"
)

// concurrencySlots 是进程内的用户并发槽位。
// 单实例部署够用；横向扩展时按第一期的结论换 Redis。
// 全局并发与每用户每分钟限流为差异清单 #16 的补充闸门，边界值已报备：
// AI_GLOBAL_CONCURRENCY 默认 8（0=不限）、AI_RATE_LIMIT_PER_MINUTE 默认 30（0=不限）。
type concurrencySlots struct {
	mu          sync.Mutex
	inUse       map[string]int
	globalInUse int
	globalLimit int
	rateLimit   int
	rate        map[string][]time.Time
}

func newConcurrencySlots() *concurrencySlots {
	return &concurrencySlots{
		inUse:       map[string]int{},
		globalLimit: envInt("AI_GLOBAL_CONCURRENCY", 8),
		rateLimit:   envInt("AI_RATE_LIMIT_PER_MINUTE", 30),
		rate:        map[string][]time.Time{},
	}
}

func envInt(name string, fallback int) int {
	if raw := os.Getenv(name); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			return n
		}
	}
	return fallback
}

// allowRate 每用户每分钟滑动窗口限流，超限返回 false。
func (s *concurrencySlots) allowRate(userID string) bool {
	if s.rateLimit <= 0 {
		return true
	}
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.rate[userID][:0]
	for _, stamp := range s.rate[userID] {
		if stamp.After(cutoff) {
			kept = append(kept, stamp)
		}
	}
	if len(kept) >= s.rateLimit {
		s.rate[userID] = kept
		return false
	}
	s.rate[userID] = append(kept, now)
	// 集合只增不减会随外部输入（用户 id）无限膨胀，超容量时整体清一次过期项。
	if len(s.rate) > 10000 {
		for id, stamps := range s.rate {
			keptID := stamps[:0]
			for _, stamp := range stamps {
				if stamp.After(cutoff) {
					keptID = append(keptID, stamp)
				}
			}
			if len(keptID) == 0 {
				delete(s.rate, id)
			} else {
				s.rate[id] = keptID
			}
		}
	}
	return true
}

func slotKey(capability, userID string) string { return capability + ":" + userID }

// acquire 尝试占用一个槽位，超过用户上限或全局上限返回 false。
func (s *concurrencySlots) acquire(capability, userID string, limit int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.globalLimit > 0 && s.globalInUse >= s.globalLimit {
		return false
	}
	key := slotKey(capability, userID)
	if s.inUse[key] >= limit {
		return false
	}
	s.inUse[key]++
	s.globalInUse++
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
	s.globalInUse--
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
