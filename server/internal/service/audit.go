package service

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
)

// AuditService 记录管理操作审计。审计记录只能追加，不能通过业务接口修改或删除。
type AuditService struct {
	db *gorm.DB
}

func NewAuditService(db *gorm.DB) *AuditService { return &AuditService{db: db} }

// Record 在调用方的事务里追加一条审计记录。before/after 只保存脱敏后的摘要，
// 由调用方保证不包含 API Key、提示词或媒体内容。
func (s *AuditService) Record(tx *gorm.DB, actorID uuid.UUID, action, targetType, targetID, requestID, reason string, before, after any) error {
	entry := model.AdminAuditLog{
		ID:          uuid.New(),
		ActorUserID: actorID,
		Action:      action,
		TargetType:  targetType,
		TargetID:    targetID,
		RequestID:   requestID,
		Reason:      reason,
		CreatedAt:   time.Now(),
	}
	if before != nil {
		if raw, err := marshalSummary(before); err == nil {
			entry.BeforeSummary = datatypes.JSON(raw)
		}
	}
	if after != nil {
		if raw, err := marshalSummary(after); err == nil {
			entry.AfterSummary = datatypes.JSON(raw)
		}
	}
	return tx.Create(&entry).Error
}

// marshalSummary 序列化审计摘要；入参已是 JSON 或字节切片时直接透传，避免二次编码。
func marshalSummary(value any) ([]byte, error) {
	if raw, ok := value.(datatypes.JSON); ok {
		return raw, nil
	}
	if raw, ok := value.([]byte); ok {
		return raw, nil
	}
	return json.Marshal(value)
}
