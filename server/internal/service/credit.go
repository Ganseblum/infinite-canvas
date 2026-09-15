package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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
)

// ErrInsufficientCredits 表示总余额不足，调用方映射为 402 INSUFFICIENT_CREDITS。
var ErrInsufficientCredits = errors.New("点数余额不足")

// CreditService 是全部余额变化的唯一入口。禁止任何 handler 直接 UPDATE credits，
// 这是「逐桶余额等于流水之和」这条对账等式能够成立的前提。
type CreditService struct {
	db *gorm.DB
}

func NewCreditService(db *gorm.DB) *CreditService { return &CreditService{db: db} }

// EnsureCredit 为新用户建零值行。注册事务与上线时为已有用户补行都走这里。
func (s *CreditService) EnsureCredit(tx *gorm.DB, userID uuid.UUID) error {
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Credit{UserID: userID}).Error
}

// Balance 读取双桶余额与权益截止时间。
func (s *CreditService) Balance(ctx context.Context, userID uuid.UUID) (model.Credit, error) {
	var credit model.Credit
	err := s.db.WithContext(ctx).First(&credit, "user_id = ?", userID).Error
	return credit, err
}

// Reserve 先扣 granted、再扣 purchased，返回实际产生的消费流水。
// 余额不足时不更新任何桶、不写流水，返回 ErrInsufficientCredits。
func (s *CreditService) Reserve(ctx context.Context, userID uuid.UUID, amountMicros int64, refID string, note string) ([]model.CreditTransaction, error) {
	if amountMicros <= 0 {
		return nil, fmt.Errorf("扣减金额必须大于 0: %d", amountMicros)
	}
	var txs []model.CreditTransaction
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		credit, err := lockCredit(tx, userID)
		if err != nil {
			return err
		}
		if credit.PurchasedMicros+credit.GrantedMicros < amountMicros {
			return ErrInsufficientCredits
		}

		now := time.Now()
		grantedDeduct := amountMicros
		if grantedDeduct > credit.GrantedMicros {
			grantedDeduct = credit.GrantedMicros
		}
		purchasedDeduct := amountMicros - grantedDeduct

		newGranted := credit.GrantedMicros - grantedDeduct
		newPurchased := credit.PurchasedMicros - purchasedDeduct
		if purchasedDeduct > 0 {
			txs = append(txs, model.CreditTransaction{
				ID:                 uuid.New(),
				UserID:             userID,
				Bucket:             BucketPurchased,
				Type:               TxTypeConsume,
				AmountMicros:       -purchasedDeduct,
				BalanceAfterMicros: newPurchased,
				RefType:            "generation",
				RefID:              refID,
				Note:               note,
				CreatedAt:          now,
			})
		}
		if grantedDeduct > 0 {
			txs = append(txs, model.CreditTransaction{
				ID:                 uuid.New(),
				UserID:             userID,
				Bucket:             BucketGranted,
				Type:               TxTypeConsume,
				AmountMicros:       -grantedDeduct,
				BalanceAfterMicros: newGranted,
				RefType:            "generation",
				RefID:              refID,
				Note:               note,
				CreatedAt:          now,
			})
		}
		if err := tx.Model(&model.Credit{}).Where("user_id = ?", userID).Updates(map[string]any{
			"purchased_micros": newPurchased,
			"granted_micros":   newGranted,
		}).Error; err != nil {
			return err
		}
		if len(txs) > 0 {
			return tx.Create(&txs).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return txs, nil
}

// Refund 按消费流水逐条退回原桶。重复退款命中 refund_of_transaction_id 唯一索引时按成功幂等处理。
func (s *CreditService) Refund(ctx context.Context, userID uuid.UUID, consumes []uuid.UUID, note string) error {
	if len(consumes) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, consumeID := range consumes {
			var consume model.CreditTransaction
			if err := tx.First(&consume, "id = ?", consumeID).Error; err != nil {
				return err
			}
			if consume.UserID != userID || consume.Type != TxTypeConsume {
				return fmt.Errorf("流水 %s 不是该用户的消费流水", consumeID)
			}
			if err := insertRefund(tx, userID, consume, note); err != nil {
				if IsDuplicateKey(err) {
					// 唯一索引保证同一消费流水最多退款一次，重复退款视为已成功。
					continue
				}
				return err
			}
		}
		return nil
	})
}

// Purchase 充值到账：购买桶按实付 1:1 入账，赠送桶单独入账，并延长 paid_until。
// 全部写入在同一事务内，任何一步失败整体回滚。
func (s *CreditService) Purchase(ctx context.Context, tx *gorm.DB, order *model.Order, now time.Time) error {
	credit, err := lockCredit(tx, order.UserID)
	if err != nil {
		return err
	}
	newPurchased := credit.PurchasedMicros + order.PurchasedMicros
	newGranted := credit.GrantedMicros + order.GrantedMicros

	// 从 max(当前值, now()) 往后叠，避免权益期内再充值反而缩短到期时间。
	base := now
	if credit.PaidUntil != nil && credit.PaidUntil.After(now) {
		base = *credit.PaidUntil
	}
	paidUntil := base.AddDate(0, 0, order.EntitlementDays)

	if order.PurchasedMicros > 0 {
		if err := tx.Create(&model.CreditTransaction{
			ID:                 uuid.New(),
			UserID:             order.UserID,
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
	return tx.Model(&model.Credit{}).Where("user_id = ?", order.UserID).Updates(map[string]any{
		"purchased_micros": newPurchased,
		"granted_micros":   newGranted,
		"paid_until":       paidUntil,
	}).Error
}

// Adjust 管理员按指定桶增减点数，写 type=grant 流水。扣到负数返回 ErrInsufficientCredits。
func (s *CreditService) Adjust(ctx context.Context, tx *gorm.DB, userID uuid.UUID, bucket string, amountMicros int64, note, actorID string) (model.Credit, error) {
	if bucket != BucketPurchased && bucket != BucketGranted {
		return model.Credit{}, fmt.Errorf("桶取值非法: %s", bucket)
	}
	credit, err := lockCredit(tx, userID)
	if err != nil {
		return model.Credit{}, err
	}
	newPurchased := credit.PurchasedMicros
	newGranted := credit.GrantedMicros
	if bucket == BucketPurchased {
		newPurchased += amountMicros
	} else {
		newGranted += amountMicros
	}
	if newPurchased < 0 || newGranted < 0 {
		return model.Credit{}, ErrInsufficientCredits
	}
	balanceAfter := newPurchased
	if bucket == BucketGranted {
		balanceAfter = newGranted
	}
	entry := model.CreditTransaction{
		ID:                 uuid.New(),
		UserID:             userID,
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
		return model.Credit{}, err
	}
	if err := tx.Model(&model.Credit{}).Where("user_id = ?", userID).Updates(map[string]any{
		"purchased_micros": newPurchased,
		"granted_micros":   newGranted,
	}).Error; err != nil {
		return model.Credit{}, err
	}
	credit.PurchasedMicros = newPurchased
	credit.GrantedMicros = newGranted
	return credit, nil
}

// RecentTransactions 返回最近 N 条流水，供个人中心余额卡片直接渲染。
func (s *CreditService) RecentTransactions(ctx context.Context, userID uuid.UUID, limit int) ([]model.CreditTransaction, error) {
	var items []model.CreditTransaction
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").Limit(limit).Find(&items).Error
	return items, err
}

// ListTransactions 游标分页返回流水，形状与第二期生成记录一致。
func (s *CreditService) ListTransactions(ctx context.Context, userID uuid.UUID, cursor string, size int, txType string) ([]model.CreditTransaction, string, error) {
	query := s.db.WithContext(ctx).Where("user_id = ?", userID)
	if txType != "" {
		if !validTransactionType(txType) {
			return nil, "", ErrInvalidCursor
		}
		query = query.Where("type = ?", txType)
	}
	if cursor != "" {
		createdAt, id, err := DecodeCursor(cursor)
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
		nextCursor = EncodeCursor(last.CreatedAt, last.ID)
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
func (s *CreditService) SumByBucket(ctx context.Context, userID uuid.UUID) (map[string]int64, error) {
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

// lockCredit 在事务中锁定该用户的 credits 行。行不存在时先补零值行再锁定。
func lockCredit(tx *gorm.DB, userID uuid.UUID) (model.Credit, error) {
	var credit model.Credit
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&credit, "user_id = ?", userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Credit{UserID: userID}).Error; err != nil {
			return credit, err
		}
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&credit, "user_id = ?", userID).Error
	}
	return credit, err
}

// insertRefund 写入一条指向原消费流水的退款流水并回补对应桶余额。
func insertRefund(tx *gorm.DB, userID uuid.UUID, consume model.CreditTransaction, note string) error {
	credit, err := lockCredit(tx, userID)
	if err != nil {
		return err
	}
	newPurchased := credit.PurchasedMicros
	newGranted := credit.GrantedMicros
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
		Bucket:                consume.Bucket,
		Type:                  TxTypeRefund,
		AmountMicros:          -consume.AmountMicros,
		BalanceAfterMicros:    balanceAfter,
		RefType:               consume.RefType,
		RefID:                 consume.RefID,
		RefundOfTransactionID: &consume.ID,
		Note:                  note,
		CreatedAt:             time.Now(),
	}
	if err := tx.Create(&refund).Error; err != nil {
		return err
	}
	return tx.Model(&model.Credit{}).Where("user_id = ?", userID).Updates(map[string]any{
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
