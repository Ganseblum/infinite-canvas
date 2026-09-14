package db

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
)

// SeedTestData 写入固定的测试账号与示例数据，供测试/预发布环境和本地联调使用。
// 全部按「已存在就跳过」处理，可以重复执行；数据定义随代码入库，任何环境打开开关即可导入。
// 绝不要在正式环境启用：测试账号的密码是公开的。
func SeedTestData(gormDB *gorm.DB, email, password string) error {
	if email == "" || password == "" {
		return errors.New("测试账号邮箱与密码不能为空")
	}

	var user model.User
	err := gormDB.Where("email = ?", email).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if hashErr != nil {
			return fmt.Errorf("生成测试账号密码哈希失败: %w", hashErr)
		}
		now := time.Now()
		user = model.User{
			ID:              uuid.New(),
			Email:           email,
			Username:        "tester",
			PasswordHash:    string(hash),
			DisplayName:     "测试账号",
			Role:            "user",
			Status:          "active",
			EmailVerifiedAt: &now, // 直接标记已验证，便于测试媒体上传等需要验证邮箱的链路
		}
		if err := gormDB.Create(&user).Error; err != nil {
			return fmt.Errorf("创建测试账号失败: %w", err)
		}
		slog.Info("已创建测试账号", "email", email)
	} else if err != nil {
		return fmt.Errorf("查询测试账号失败: %w", err)
	}

	var canvasCount int64
	if err := gormDB.Model(&model.Canvas{}).Where("user_id = ?", user.ID).Count(&canvasCount).Error; err != nil {
		return fmt.Errorf("统计测试画布失败: %w", err)
	}
	if canvasCount == 0 {
		canvas := model.Canvas{
			ID:              uuid.New(),
			UserID:          user.ID,
			Title:           "示例画布（测试数据）",
			Data:            datatypes.JSON([]byte(testCanvasData)),
			Revision:        1,
			NodeCount:       2,
			ConnectionCount: 1,
			CoverKey:        "",
		}
		if err := gormDB.Create(&canvas).Error; err != nil {
			return fmt.Errorf("创建测试画布失败: %w", err)
		}
		slog.Info("已创建测试画布", "title", canvas.Title)
	}

	var assetCount int64
	if err := gormDB.Model(&model.Asset{}).Where("user_id = ?", user.ID).Count(&assetCount).Error; err != nil {
		return fmt.Errorf("统计测试素材失败: %w", err)
	}
	if assetCount == 0 {
		asset := model.Asset{
			ID:               uuid.New(),
			UserID:           user.ID,
			Kind:             "text",
			Title:            "示例素材（测试数据）",
			Data:             datatypes.JSON([]byte(`{"content":"这是一条测试素材，用于验收素材列表与标签筛选。","source":"seed"}`)),
			ModerationStatus: "skipped",
		}
		if err := gormDB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&asset).Error; err != nil {
				return err
			}
			return tx.Create(&model.AssetTag{AssetID: asset.ID, Tag: "测试数据"}).Error
		}); err != nil {
			return fmt.Errorf("创建测试素材失败: %w", err)
		}
		slog.Info("已创建测试素材", "title", asset.Title)
	}

	return nil
}

// testCanvasData 是一张最小可用的画布：两个文本节点加一条连线。
const testCanvasData = `{"nodes":[{"id":"seed-node-1","type":"text","title":"提示词草稿","position":{"x":-360,"y":-40},"width":320,"height":220,"metadata":{"content":"测试数据：这条文本节点用于验证画布加载与自动保存。","status":"success"}},{"id":"seed-node-2","type":"text","title":"结果","position":{"x":40,"y":-40},"width":320,"height":220,"metadata":{"content":"测试数据：与上游节点相连，用于验证连线恢复。","status":"success"}}],"connections":[{"id":"seed-connection-1","fromNodeId":"seed-node-1","toNodeId":"seed-node-2"}],"chatSessions":[],"activeChatId":null,"backgroundMode":"lines","showImageInfo":false,"viewport":{"x":0,"y":0,"k":1}}`
