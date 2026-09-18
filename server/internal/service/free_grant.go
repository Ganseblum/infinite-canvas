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
	dayKey        string
	deviceSet     map[string]struct{}
	claimCountSet map[string]int
}

// maxRiskSetSize 限制风控集合的容量：键来自外部输入（UA/IP），无上限会撑爆内存。
const maxRiskSetSize = 10000

func NewFreeGrantService(db *gorm.DB) *FreeGrantService {
	now := time.Now().In(grantZone)
	return &FreeGrantService{
		db:            db,
		dayKey:        now.Format("2006-01-02"),
		deviceSet:     make(map[string]struct{}),
		claimCountSet: make(map[string]int),
	}
}

// RiskScore 计算一次领取的风险评分（0-100）。IP 网段和设备只作为风险信号，
// 不单独永久封禁共享网络。集合按 UTC+8 日界清空并在超容量时重置，不无限增长。
func (s *FreeGrantService) RiskScore(uid uuid.UUID, ip, userAgent string) int {
	score := 0
	dayKey := time.Now().In(grantZone).Format("2006-01-02")

	s.mu.Lock()
	defer s.mu.Unlock()

	if dayKey != s.dayKey || len(s.deviceSet) > maxRiskSetSize {
		s.dayKey = dayKey
		s.deviceSet = make(map[string]struct{})
		s.claimCountSet = make(map[string]int)
	}

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

// grantZone 与用量分析看板同口径：日界按 UTC+8 计算。
var grantZone = time.FixedZone("UTC+8", 8*3600)

// WithinDailyBudget 检查当日剩余预算是否足够再发一次领取。
// 预算口径：当日已批准领取数 × 单次领取估算成本 < 每日预算（差异清单 #46——
// 旧实现每次固定记 100、计数在进程内存，成本闸门形同虚设）。
// 领取记录落库持久化，跨重启与多实例一致；按 UTC+8 日界统计。
func (s *FreeGrantService) WithinDailyBudget(dailyBudgetMicros int64, estimatePerClaimMicros int64) bool {
	if dailyBudgetMicros <= 0 {
		return false
	}
	if estimatePerClaimMicros <= 0 {
		// 无法估算时不设闸门等于关掉预算，按拒绝处理。
		return false
	}
	now := time.Now().In(grantZone)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, grantZone)
	var granted int64
	if err := s.db.Model(&model.FreeGrantClaim{}).
		Where("status = ? AND created_at >= ? AND created_at < ?", "granted", dayStart, dayStart.Add(24*time.Hour)).
		Count(&granted).Error; err != nil {
		slog.Error("统计当日领取数失败，预算闸门按拒绝处理", "err", err)
		return false
	}
	return (granted+1)*estimatePerClaimMicros <= dailyBudgetMicros
}

// EstimatePerClaimMicros 估算一次领取的成本：该活动覆盖的免费试用包
// （图片试用次数 × 图片最低单价 + 视频试用次数 × 视频最低单价），
// 单价取免费试用模型价格矩阵里的最低组合。目录不可用时返回 0，预算闸门关闭领取。
func (s *FreeGrantService) EstimatePerClaimMicros() int64 {
	var models []model.ModelCatalog
	if err := s.db.Where("free_trial_eligible = ? AND enabled = ?", true, true).Find(&models).Error; err != nil {
		slog.Error("读取免费试用模型失败", "err", err)
		return 0
	}
	minByCapability := map[string]int64{}
	for _, m := range models {
		cost, err := ParseCreditCost(m.CreditCost)
		if err != nil || len(cost.Prices) == 0 {
			continue
		}
		minCost := cost.Prices[0].CostMicros
		for _, entry := range cost.Prices[1:] {
			if entry.CostMicros < minCost {
				minCost = entry.CostMicros
			}
		}
		if existing, ok := minByCapability[m.Capability]; !ok || minCost < existing {
			minByCapability[m.Capability] = minCost
		}
	}
	var estimate int64
	if image, ok := minByCapability["image"]; ok {
		estimate += image * FreeImageTrialLimit
	}
	if video, ok := minByCapability["video"]; ok {
		estimate += video * FreeVideoTrialLimit
	}
	return estimate
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
