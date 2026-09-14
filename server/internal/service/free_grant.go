package service

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
)

// FreeGrantService 免费赠送资格风控。本期只确认资格、写领取记录，
// 不直接改点数或试用计数（第三期统一权益服务才处理 granted 桶）。
type FreeGrantService struct {
	db            *gorm.DB
	mu            sync.Mutex
	budgetSpent   int64
	budgetDayKey  string
	deviceSet     map[string]struct{}
	claimCountSet map[string]int
}

func NewFreeGrantService(db *gorm.DB) *FreeGrantService {
	return &FreeGrantService{
		db:            db,
		budgetSpent:   0,
		budgetDayKey:  time.Now().Format("2006-01-02"),
		deviceSet:     make(map[string]struct{}),
		claimCountSet: make(map[string]int),
	}
}

// RiskScore 计算一次领取的风险评分（0-100）。IP 网段和设备只作为风险信号，
// 不单独永久封禁共享网络。
func (s *FreeGrantService) RiskScore(uid uuid.UUID, ip, userAgent string) int {
	score := 0
	dayKey := time.Now().Format("2006-01-02")

	s.mu.Lock()
	defer s.mu.Unlock()

	// 设备指纹：同一 UA 在本日关联多个账号 → 风险
	deviceFingerprint := hashString(userAgent)
	if _, ok := s.deviceSet[deviceFingerprint]; ok {
		score += 30
	} else {
		s.deviceSet[deviceFingerprint] = struct{}{}
	}

	// 本日该账号的领取尝试次数
	s.claimCountSet[uid.String()+":"+dayKey]++
	if s.claimCountSet[uid.String()+":"+dayKey] > 3 {
		score += 40
	}

	// 同 IP 历史领取量（简单启发式：超出一定量加分）
	ipHash := hashString(ip)
	if _, ok := s.deviceSet["ip:"+ipHash]; ok {
		score += 10
	} else {
		s.deviceSet["ip:"+ipHash] = struct{}{}
	}

	return score
}

// WithinDailyBudget 检查当日剩余预算是否足够发一次。预算按天重置。
func (s *FreeGrantService) WithinDailyBudget(dailyBudgetMicros int64) bool {
	if dailyBudgetMicros <= 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dayKey := time.Now().Format("2006-01-02")
	if dayKey != s.budgetDayKey {
		s.budgetDayKey = dayKey
		s.budgetSpent = 0
	}
	if s.budgetSpent >= dailyBudgetMicros {
		return false
	}
	// 每次领取按固定成本估算（与第三期 granted 桶数量对齐，这里不记账）
	s.budgetSpent += 100
	return true
}

// RecordClaimed 供测试与后续统计使用（可选）。
func (s *FreeGrantService) RecordClaimed(userID uuid.UUID, campaignID string) {
	if err := s.db.Create(&model.FreeGrantClaim{
		ID:         uuid.New(),
		UserID:     userID,
		CampaignID: campaignID,
		Status:     "granted",
	}).Error; err != nil {
		slog.Warn("记录赠送领取失败", "err", err)
	}
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
