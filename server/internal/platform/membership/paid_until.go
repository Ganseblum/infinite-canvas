package membership

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
)

// LatestPaidUntil 返回用户最近一期订阅的 period_end；无订阅记录返回 nil。
// 账号面（/me）、点数面（档位展示）与管理面（用户列表）共用这一口径。
func LatestPaidUntil(db *gorm.DB, userID uuid.UUID) *time.Time {
	var sub model.MembershipSubscription
	if err := db.Where("user_id = ?", userID).Order("period_end DESC").First(&sub).Error; err != nil {
		return nil
	}
	return &sub.PeriodEnd
}
