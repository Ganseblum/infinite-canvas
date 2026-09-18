// Package billing 平台点数域：credit_accounts 与 credit_transactions 的唯一业务入口。
// 不变量：双桶余额等于流水之和。共享余额（D3）：一个余额全产品消耗，
// 流水带 product 维度；服务构造时绑定 product（画布侧固定 youc-canvas）。
package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/infinite-canvas/server/internal/cursor"
	"github.com/infinite-canvas/server/internal/model"
)

const (
	BucketPurchased = "purchased"
	BucketGranted   = "granted"

	TxTypePurchase = "purchase"
	TxTypeConsume  = "consume"
	TxTypeRefund   = "refund"
	TxTypeGrant    = "grant"
	TxTypeExpire   = "expire"

	// PeriodTotal 与试用指标：usage_records 在 M2 仅保留试用计数。
	PeriodTotal          = "total"
	MetricFreeImageTrial = "free_image_trials"
	MetricFreeVideoTrial = "free_video_trials"

	// FreeImageTrialLimit 与 FreeVideoTrialLimit 是一次性免费额度的上限。
	FreeImageTrialLimit = int64(3)
	FreeVideoTrialLimit = int64(1)
)

// ErrInsufficientCredits 表示总余额不足，调用方映射为 402 INSUFFICIENT_CREDITS。
var ErrInsufficientCredits = errors.New("点数余额不足")

// Service 平台点数域服务。
type Service struct {
	db      *gorm.DB
	product string
}

func NewService(db *gorm.DB, product string) *Service { return &Service{db: db, product: product} }

// DB 暴露底层连接，供报价等既有服务把领取记录查询并入现有调用链；不用于绕过域方法写余额。
func (s *Service) DB() *gorm.DB { return s.db }

// EnsureAccount 为新用户建零值账户行。注册事务与上线时为已有用户补行都走这里。
func (s *Service) EnsureAccount(tx *gorm.DB, userID uuid.UUID) error {
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.CreditAccount{UserID: userID}).Error
}

// Balance 读取双桶余额。
func (s *Service) Balance(ctx context.Context, userID uuid.UUID) (model.CreditAccount, error) {
	var account model.CreditAccount
	err := s.db.WithContext(ctx).First(&account, "user_id = ?", userID).Error
	return account, err
}

// Reserve 在调用方事务内先扣 granted、再扣 purchased，返回实际产生的消费流水。
// 余额不足时不更新任何桶、不写流水，返回 ErrInsufficientCredits；
// 行锁保证并发扣费串行化。bizKey 写入流水的 ref_id，退款按它定位原消费流水。
func (s *Service) Reserve(tx *gorm.DB, userID uuid.UUID, points int64, bizKey string) ([]model.CreditTransaction, error) {
	if points <= 0 {
		return nil, fmt.Errorf("扣减点数必须大于 0: %d", points)
	}
	account, err := lockAccount(tx, userID)
	if err != nil {
		return nil, err
	}
	if account.PurchasedMicros+account.GrantedMicros < points {
		return nil, ErrInsufficientCredits
	}

	now := time.Now()
	grantedDeduct := points
	if grantedDeduct > account.GrantedMicros {
		grantedDeduct = account.GrantedMicros
	}
	purchasedDeduct := points - grantedDeduct

	newGranted := account.GrantedMicros - grantedDeduct
	newPurchased := account.PurchasedMicros - purchasedDeduct

	txs := make([]model.CreditTransaction, 0, 2)
	if purchasedDeduct > 0 {
		txs = append(txs, model.CreditTransaction{
			ID:                 uuid.New(),
			UserID:             userID,
			Product:            s.product,
			Bucket:             BucketPurchased,
			Type:               TxTypeConsume,
			AmountMicros:       -purchasedDeduct,
			BalanceAfterMicros: newPurchased,
			RefType:            "generation",
			RefID:              bizKey,
			CreatedAt:          now,
		})
	}
	if grantedDeduct > 0 {
		txs = append(txs, model.CreditTransaction{
			ID:                 uuid.New(),
			UserID:             userID,
			Product:            s.product,
			Bucket:             BucketGranted,
			Type:               TxTypeConsume,
			AmountMicros:       -grantedDeduct,
			BalanceAfterMicros: newGranted,
			RefType:            "generation",
			RefID:              bizKey,
			CreatedAt:          now,
		})
	}
	if err := tx.Model(&model.CreditAccount{}).Where("user_id = ?", userID).Updates(map[string]any{
		"purchased_micros": newPurchased,
		"granted_micros":   newGranted,
	}).Error; err != nil {
		return nil, err
	}
	if len(txs) > 0 {
		if err := tx.Create(&txs).Error; err != nil {
			return nil, err
		}
	}
	return txs, nil
}

// Refund 在调用方事务内按 bizKey 退还全部未退的消费流水（原桶退回）。
// 重复退款命中 refund_of_transaction_id 唯一索引时按成功幂等处理。
func (s *Service) Refund(tx *gorm.DB, userID uuid.UUID, bizKey string) error {
	var consumes []model.CreditTransaction
	if err := tx.Where("user_id = ? AND ref_id = ? AND type = ?", userID, bizKey, TxTypeConsume).
		Find(&consumes).Error; err != nil {
		return err
	}
	for _, consume := range consumes {
		if err := s.insertRefund(tx, userID, consume); err != nil {
			if IsDuplicateKey(err) {
				// 唯一索引保证同一消费流水最多退款一次，重复退款视为已成功。
				continue
			}
			return err
		}
	}
	return nil
}

// Purchase 充值到账：购买桶按实付 1:1 入账，赠送桶单独入账。全部写入在调用方事务内。
// 付费权益不再由点数入账延长（D2/D6）：会员时长由 membership.GrantFromOrder 处理。
func (s *Service) Purchase(tx *gorm.DB, order *model.Order, now time.Time) error {
	account, err := lockAccount(tx, order.UserID)
	if err != nil {
		return err
	}
	newPurchased := account.PurchasedMicros + order.PurchasedMicros
	newGranted := account.GrantedMicros + order.GrantedMicros

	if order.PurchasedMicros > 0 {
		if err := tx.Create(&model.CreditTransaction{
			ID:                 uuid.New(),
			UserID:             order.UserID,
			Product:            order.Product,
			Bucket:             BucketPurchased,
			Type:               TxTypePurchase,
			AmountMicros:       order.PurchasedMicros,
			BalanceAfterMicros: newPurchased,
			RefType:            "order",
			RefID:              order.ID.String(),
			Note:               order.PackageID,
			CreatedAt:          now,
		}).Error; err != nil {
			return err
		}
	}
	if order.GrantedMicros > 0 {
		if err := tx.Create(&model.CreditTransaction{
			ID:                 uuid.New(),
			UserID:             order.UserID,
			Product:            order.Product,
			Bucket:             BucketGranted,
			Type:               TxTypePurchase,
			AmountMicros:       order.GrantedMicros,
			BalanceAfterMicros: newGranted,
			RefType:            "order",
			RefID:              order.ID.String(),
			Note:               order.PackageID,
			CreatedAt:          now,
		}).Error; err != nil {
			return err
		}
	}
	return tx.Model(&model.CreditAccount{}).Where("user_id = ?", order.UserID).Updates(map[string]any{
		"purchased_micros": newPurchased,
		"granted_micros":   newGranted,
	}).Error
}

// Adjust 管理员按指定桶增减点数，写 type=grant 流水。扣到负数返回 ErrInsufficientCredits。
func (s *Service) Adjust(tx *gorm.DB, userID uuid.UUID, bucket string, amountMicros int64, note, actorID string) (model.CreditAccount, error) {
	if bucket != BucketPurchased && bucket != BucketGranted {
		return model.CreditAccount{}, fmt.Errorf("桶取值非法: %s", bucket)
	}
	account, err := lockAccount(tx, userID)
	if err != nil {
		return model.CreditAccount{}, err
	}
	newPurchased := account.PurchasedMicros
	newGranted := account.GrantedMicros
	if bucket == BucketPurchased {
		newPurchased += amountMicros
	} else {
		newGranted += amountMicros
	}
	if newPurchased < 0 || newGranted < 0 {
		return model.CreditAccount{}, ErrInsufficientCredits
	}
	balanceAfter := newPurchased
	if bucket == BucketGranted {
		balanceAfter = newGranted
	}
	entry := model.CreditTransaction{
		ID:                 uuid.New(),
		UserID:             userID,
		Product:            s.product,
		Bucket:             bucket,
		Type:               TxTypeGrant,
		AmountMicros:       amountMicros,
		BalanceAfterMicros: balanceAfter,
		RefType:            "admin",
		RefID:              actorID,
		Note:               note,
		CreatedAt:          time.Now(),
	}
	if err := tx.Create(&entry).Error; err != nil {
		return model.CreditAccount{}, err
	}
	if err := tx.Model(&model.CreditAccount{}).Where("user_id = ?", userID).Updates(map[string]any{
		"purchased_micros": newPurchased,
		"granted_micros":   newGranted,
	}).Error; err != nil {
		return model.CreditAccount{}, err
	}
	account.PurchasedMicros = newPurchased
	account.GrantedMicros = newGranted
	return account, nil
}

// RecentTransactions 返回最近 N 条流水，供个人中心余额卡片直接渲染。
func (s *Service) RecentTransactions(ctx context.Context, userID uuid.UUID, limit int) ([]model.CreditTransaction, error) {
	var items []model.CreditTransaction
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").Limit(limit).Find(&items).Error
	return items, err
}

// ListTransactions 游标分页返回流水，形状与第二期生成记录一致。
func (s *Service) ListTransactions(ctx context.Context, userID uuid.UUID, cursorStr string, size int, txType string) ([]model.CreditTransaction, string, error) {
	query := s.db.WithContext(ctx).Where("user_id = ?", userID)
	if txType != "" {
		if !validTransactionType(txType) {
			return nil, "", cursor.ErrInvalidCursor
		}
		query = query.Where("type = ?", txType)
	}
	if cursorStr != "" {
		createdAt, id, err := cursor.Decode(cursorStr)
		if err != nil {
			return nil, "", err
		}
		query = query.Where("(created_at < ?) OR (created_at = ? AND id < ?)", createdAt, createdAt, id)
	}
	var items []model.CreditTransaction
	if err := query.Order("created_at DESC, id DESC").Limit(size + 1).Find(&items).Error; err != nil {
		return nil, "", err
	}
	nextCursor := ""
	if len(items) > size {
		items = items[:size]
		last := items[len(items)-1]
		nextCursor = cursor.Encode(last.CreatedAt, last.ID)
	}
	return items, nextCursor, nil
}

func validTransactionType(t string) bool {
	switch t {
	case TxTypePurchase, TxTypeConsume, TxTypeRefund, TxTypeGrant, TxTypeExpire:
		return true
	default:
		return false
	}
}

// SumByBucket 返回逐桶流水合计，供管理后台与对账使用。
func (s *Service) SumByBucket(ctx context.Context, userID uuid.UUID) (map[string]int64, error) {
	type row struct {
		Bucket string
		Total  int64
	}
	var rows []row
	err := s.db.WithContext(ctx).Model(&model.CreditTransaction{}).
		Select("bucket, SUM(amount_micros) AS total").
		Where("user_id = ?", userID).Group("bucket").Scan(&rows).Error
	totals := map[string]int64{}
	for _, r := range rows {
		totals[r.Bucket] = r.Total
	}
	return totals, err
}

// ===== 免费试用（usage_records 仅保留试用计数） =====

// ConsumeFreeTrial 条件递增一次免费试用计数：HasGrantedClaim 通过且计数未达上限才消耗。
// 返回是否消耗成功，调用方据此放行免费试用。
func (s *Service) ConsumeFreeTrial(tx *gorm.DB, userID uuid.UUID, metric string) (bool, error) {
	granted, err := s.HasGrantedClaim(tx, userID)
	if err != nil {
		return false, err
	}
	if !granted {
		return false, nil
	}
	limit, err := FreeTrialLimitOf(metric)
	if err != nil {
		return false, err
	}
	// 先补零值行（幂等），再条件递增：计数达到上限后不再增加。
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.UsageRecord{
		UserID: userID, Metric: metric, Period: PeriodTotal, Value: 0,
	}).Error; err != nil {
		return false, err
	}
	res := tx.Model(&model.UsageRecord{}).
		Where("user_id = ? AND metric = ? AND period = ? AND value < ?", userID, metric, PeriodTotal, limit).
		Update("value", gorm.Expr("value + 1"))
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// RefundFreeTrial 条件递减一次免费试用计数：value > 0 才 -1，行不存在或已为 0 不动作。
// 生成失败时退还试用次数用，配合请求行的状态条件更新保证只退一次。
func (s *Service) RefundFreeTrial(tx *gorm.DB, userID uuid.UUID, metric string) error {
	res := tx.Model(&model.UsageRecord{}).
		Where("user_id = ? AND metric = ? AND period = ? AND value > 0", userID, metric, PeriodTotal).
		Update("value", gorm.Expr("value - 1"))
	return res.Error
}

// FreeTrialRemaining 读取一次性免费试用计数与上限的差值。
// 计数只增不减，删除生成历史不返还额度。
func (s *Service) FreeTrialRemaining(ctx context.Context, userID uuid.UUID, metric string) (int64, error) {
	limit, err := FreeTrialLimitOf(metric)
	if err != nil {
		return 0, err
	}
	var record model.UsageRecord
	err = s.db.WithContext(ctx).First(&record, "user_id = ? AND metric = ? AND period = ?", userID, metric, PeriodTotal).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return limit, nil
	}
	if err != nil {
		return 0, err
	}
	remaining := limit - record.Value
	if remaining < 0 {
		return 0, nil
	}
	return remaining, nil
}

// HasGrantedClaim 判断用户是否存在已批准的免费额度领取记录（差异清单 #5）。
// 消费免费次数必须同时满足「已批准领取记录 + 一次性用量计数未达上限」，
// 被风控拒绝（status=denied）或从未领取的账号不能直接享受试用。
func (s *Service) HasGrantedClaim(txOrDB *gorm.DB, userID uuid.UUID) (bool, error) {
	var count int64
	err := txOrDB.Model(&model.FreeGrantClaim{}).
		Where("user_id = ? AND status = ?", userID, "granted").
		Count(&count).Error
	return count > 0, err
}

// FreeTrialLimitOf 返回试用指标的上限。
func FreeTrialLimitOf(metric string) (int64, error) {
	switch metric {
	case MetricFreeImageTrial:
		return FreeImageTrialLimit, nil
	case MetricFreeVideoTrial:
		return FreeVideoTrialLimit, nil
	default:
		return 0, fmt.Errorf("未知试用指标: %s", metric)
	}
} // lockAccount 在事务中锁定该用户的点数账户行。行不存在时先补零值行再锁定。
func lockAccount(tx *gorm.DB, userID uuid.UUID) (model.CreditAccount, error) {
	var account model.CreditAccount
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, "user_id = ?", userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.CreditAccount{UserID: userID}).Error; err != nil {
			return account, err
		}
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, "user_id = ?", userID).Error
	}
	return account, err
}

// insertRefund 写入一条指向原消费流水的退款流水并回补对应桶余额。
func (s *Service) insertRefund(tx *gorm.DB, userID uuid.UUID, consume model.CreditTransaction) error {
	account, err := lockAccount(tx, userID)
	if err != nil {
		return err
	}
	newPurchased := account.PurchasedMicros
	newGranted := account.GrantedMicros
	if consume.Bucket == BucketPurchased {
		newPurchased -= consume.AmountMicros // consume 金额为负，减去即回补
	} else {
		newGranted -= consume.AmountMicros
	}
	balanceAfter := newPurchased
	if consume.Bucket == BucketGranted {
		balanceAfter = newGranted
	}
	refund := model.CreditTransaction{
		ID:                    uuid.New(),
		UserID:                userID,
		Product:               consume.Product,
		Bucket:                consume.Bucket,
		Type:                  TxTypeRefund,
		AmountMicros:          -consume.AmountMicros,
		BalanceAfterMicros:    balanceAfter,
		RefType:               consume.RefType,
		RefID:                 consume.RefID,
		RefundOfTransactionID: &consume.ID,
		Note:                  "生成失败退还",
		CreatedAt:             time.Now(),
	}
	if err := tx.Create(&refund).Error; err != nil {
		return err
	}
	return tx.Model(&model.CreditAccount{}).Where("user_id = ?", userID).Updates(map[string]any{
		"purchased_micros": newPurchased,
		"granted_micros":   newGranted,
	}).Error
}

// IsDuplicateKey 判断错误是否为唯一索引冲突（MySQL 1062 与 SQLite 的 unique constraint）。
func IsDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique constraint") || strings.Contains(msg, "1062")
}
