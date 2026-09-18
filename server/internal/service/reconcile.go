package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/membership"
	"github.com/infinite-canvas/server/internal/platform/storage"
)

// ReconcileService 夜间存储记账对账。共享池三层记账（计划 T08 异常矩阵路径三）：
//
//	L1 media_files.bytes 按 (user_id, product) 聚合 —— 事实源
//	L2 storage_usage.bytes 按 (user_id, product) 计数 —— Commit 增量记账
//	L3 storage_accounts.used_bytes 按 user 聚合 —— 共享池快读计数
//
// 正常写入路径三层同事务更新；进程中断或人为改库会漂移，夜间对账把不平的
// 账号走 storage.Recalculate（以 L1 重算 L2/L3）收敛。天然幂等，重复执行无副作用。
type ReconcileService struct {
	db      *gorm.DB
	storage *storage.Service
}

func NewReconcileService(db *gorm.DB, stor *storage.Service) *ReconcileService {
	return &ReconcileService{db: db, storage: stor}
}

// ReconcileStorage 全量比对三层记账并修正。返回检查的账号数与修正数。
// 单账号修正失败只记日志、不阻塞其余账号（与匿名化任务同口径）。
// 告警通道（未决项 D9）拍板前先落结构化日志：storage_reconcile_drift。
func (s *ReconcileService) ReconcileStorage(ctx context.Context, now time.Time) (checked, fixed int, err error) {
	// L1：media_files 聚合。
	type aggregate struct {
		UserID  uuid.UUID
		Product string
		Bytes   int64
	}
	var factRows []aggregate
	if err := s.db.WithContext(ctx).Model(&model.MediaFile{}).
		Select("user_id, product, COALESCE(SUM(bytes), 0) AS bytes").
		Group("user_id, product").
		Scan(&factRows).Error; err != nil {
		return 0, 0, err
	}
	// L2：storage_usage 分产品计数。
	var usageRows []aggregate
	if err := s.db.WithContext(ctx).Model(&model.StorageUsage{}).
		Select("user_id, product, bytes").
		Scan(&usageRows).Error; err != nil {
		return 0, 0, err
	}
	fact := map[uuid.UUID]map[string]int64{}
	for _, row := range factRows {
		if fact[row.UserID] == nil {
			fact[row.UserID] = map[string]int64{}
		}
		fact[row.UserID][row.Product] = row.Bytes
	}
	usage := map[uuid.UUID]map[string]int64{}
	for _, row := range usageRows {
		if usage[row.UserID] == nil {
			usage[row.UserID] = map[string]int64{}
		}
		usage[row.UserID][row.Product] = row.Bytes
	}
	// L3：共享池账户聚合（哪些账号需要检查以三层的并集为准）。
	var accounts []model.StorageAccount
	if err := s.db.WithContext(ctx).Find(&accounts).Error; err != nil {
		return 0, 0, err
	}
	accountUsed := make(map[uuid.UUID]int64, len(accounts))
	for _, account := range accounts {
		accountUsed[account.UserID] = account.UsedBytes
	}

	users := make(map[uuid.UUID]struct{}, len(factRows))
	for _, row := range factRows {
		users[row.UserID] = struct{}{}
	}
	for _, row := range usageRows {
		users[row.UserID] = struct{}{}
	}
	for _, account := range accounts {
		users[account.UserID] = struct{}{}
	}

	for userID := range users {
		checked++
		drift := false
		// L1 vs L2：逐产品比对事实源与计数层。
		for product, factBytes := range fact[userID] {
			if usage[userID][product] != factBytes {
				drift = true
				break
			}
		}
		// L2 有、L1 无（残留产品行）也计不平。
		if !drift {
			for product := range usage[userID] {
				if _, ok := fact[userID][product]; !ok && usage[userID][product] != 0 {
					drift = true
					break
				}
			}
		}
		// L3 vs L2 聚合：共享池计数必须等于分产品之和。
		if !drift {
			var usageTotal int64
			for _, bytes := range usage[userID] {
				usageTotal += bytes
			}
			if accountUsed[userID] != usageTotal {
				drift = true
			}
		}
		if !drift {
			continue
		}
		// 修正：以 L1 重算 L2/L3（Recalculate 幂等）。
		if _, err := s.storage.Recalculate(ctx, userID); err != nil {
			slog.Error("存储记账对账修正失败", "user", userID, "err", err)
			continue
		}
		fixed++
		slog.Warn("storage_reconcile_drift", "user", userID, "action", "recalculated", "at", now.Format(time.RFC3339))
	}
	// 配额收敛：订阅自然到期/降档后 storage_accounts.quota 不会自动回写（惰性派生的兜底），
	// 夜间按 ActivePlan 当前档位统一回写一次，保证上传侧配额与派生口径一致（评审门③ P1-2）。
	ms := membership.NewService(s.db)
	for userID := range users {
		if err := ms.SyncQuotaWithin(s.db.WithContext(ctx), userID, now); err != nil {
			slog.Error("存储配额夜间同步失败", "user", userID, "err", err)
		}
	}
	return checked, fixed, nil
}
