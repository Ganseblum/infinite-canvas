package db

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/infinite-canvas/server/internal/authz"
	"github.com/infinite-canvas/server/internal/model"
)

// Connect 打开数据库连接。DATABASE_URL 以 sqlite: 前缀时使用 SQLite 文件，
// 便于本地快速起一个不依赖 MySQL 的验证实例；其余情况一律走 MySQL。
func Connect(databaseURL string) (*gorm.DB, error) {
	var dialector gorm.Dialector
	if strings.HasPrefix(databaseURL, "sqlite:") {
		dialector = sqlite.Open(strings.TrimPrefix(databaseURL, "sqlite:"))
	} else {
		dialector = mysql.Open(databaseURL)
	}
	gormDB, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, fmt.Errorf("获取底层连接失败: %w", err)
	}
	if strings.HasPrefix(databaseURL, "sqlite:") {
		// SQLite 只允许单写连接，避免并发写入锁冲突。
		sqlDB.SetMaxOpenConns(1)
	} else {
		sqlDB.SetMaxOpenConns(20)
		sqlDB.SetMaxIdleConns(5)
	}
	sqlDB.SetConnMaxLifetime(time.Hour)
	return gormDB, nil
}

func Migrate(gormDB *gorm.DB) error {
	return gormDB.AutoMigrate(
		&model.PlatformUser{},
		&model.Session{},
		&model.EmailToken{},
		&model.FreeGrantClaim{},
		&model.MembershipPlan{},
		&model.MembershipSubscription{},
		&model.StorageAccount{},
		&model.StorageUsage{},
		&model.Canvas{},
		&model.Asset{},
		&model.AssetTag{},
		&model.Generation{},
		&model.MediaFile{},
		&model.CreditAccount{},
		&model.CreditTransaction{},
		&model.UsageRecord{},
		&model.CreditPackage{},
		&model.Order{},
		&model.ModelCatalog{},
		&model.ModelPricePromotion{},
		&model.AdminAuditLog{},
		&model.PlatformChannel{},
		&model.AIRequest{},
		&model.AITask{},
		&model.ModerationRecord{},
		&model.SiteSetting{},
		&model.CommunityWork{},
		&model.CommunityLike{},
		&model.CommunityReport{},
		&model.CheckinRecord{},
		&model.UserInvite{},
		&model.Role{},
		&model.Permission{},
		&model.RolePermission{},
		&model.OAuthClient{},
	)
}

// SeedMembershipPlans 幂等写入三档会员默认数据（plans 表 M2 退役后的继任者）。
// 只有 membership_plans 表里没有对应 id 时才插入，已存在的记录一行都不动，
// 保证人工调整过的档位额度不被重启打回默认值。
func SeedMembershipPlans(gormDB *gorm.DB) error {
	plans := []model.MembershipPlan{
		{ID: "free", Name: "免费", StorageBytes: 50 * 1024 * 1024, MaxFileBytes: 20 * 1024 * 1024, RetentionDays: 7},
		{ID: "paid", Name: "付费", StorageBytes: 1 * 1024 * 1024 * 1024, MaxFileBytes: 200 * 1024 * 1024, RetentionDays: 15, PriceMicros: 30_000_000, DurationDays: 30},
		{ID: "sunset", Name: "日落", StorageBytes: 200 * 1024 * 1024, MaxFileBytes: 200 * 1024 * 1024, RetentionDays: 7},
	}
	for _, p := range plans {
		var count int64
		if err := gormDB.Model(&model.MembershipPlan{}).Where("id = ?", p.ID).Count(&count).Error; err != nil {
			return fmt.Errorf("查询会员档位 %s 失败: %w", p.ID, err)
		}
		if count == 0 {
			if err := gormDB.Create(&p).Error; err != nil {
				return fmt.Errorf("写入会员档位 %s 失败: %w", p.ID, err)
			}
		}
	}
	return nil
}

// EnsureAdmin 依据 ADMIN_EMAIL 创建或提升首个管理员，写入的是系统角色 admin。
// 只在用户不存在时创建；用户已存在但角色不是系统角色时提升；绝不重置已有管理员的密码。
func EnsureAdmin(gormDB *gorm.DB, email, password string) error {
	if email == "" || password == "" {
		return errors.New("ADMIN_EMAIL 与 ADMIN_PASSWORD 不能为空")
	}
	roleKey := authz.SystemRoleKey
	var user model.PlatformUser
	err := gormDB.Where("email = ?", email).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if hashErr != nil {
			return fmt.Errorf("生成管理员密码哈希失败: %w", hashErr)
		}
		admin := model.PlatformUser{
			ID:           uuid.New(),
			Email:        email,
			Username:     "admin",
			PasswordHash: string(hash),
			DisplayName:  "管理员",
			Role:         authz.RoleProjection(&roleKey),
			RoleKey:      &roleKey,
			Status:       "active",
		}
		if err := gormDB.Create(&admin).Error; err != nil {
			return fmt.Errorf("创建管理员失败: %w", err)
		}
		slog.Info("已创建首个管理员", "email", email)
		return nil
	}
	if err != nil {
		return fmt.Errorf("查询管理员失败: %w", err)
	}
	if user.RoleKey == nil || *user.RoleKey != authz.SystemRoleKey {
		if err := authz.AssignRole(gormDB, user.ID, &roleKey); err != nil {
			return fmt.Errorf("提升管理员失败: %w", err)
		}
		slog.Info("已将用户提升为管理员", "email", email)
	}
	return nil
}
