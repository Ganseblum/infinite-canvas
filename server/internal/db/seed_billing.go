package db

import (
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/model"
)

// SeedBilling 幂等写入默认充值档位与示例平台模型目录。
// 只有在对应记录不存在时才插入，已存在的一行都不动，人工调整过的价格不会被重启打回默认值。
func SeedBilling(gormDB *gorm.DB) error {
	packages := []model.CreditPackage{
		{ID: "starter", Name: "体验包", PriceMicros: 10_000_000, BonusMicros: 0, EntitlementDays: 30, Currency: "CNY", Enabled: true, Sort: 1},
		{ID: "standard", Name: "标准包", PriceMicros: 30_000_000, BonusMicros: 3_000_000, EntitlementDays: 30, Currency: "CNY", Enabled: true, Sort: 2},
		{ID: "pro", Name: "专业包", PriceMicros: 100_000_000, BonusMicros: 15_000_000, EntitlementDays: 90, Currency: "CNY", Enabled: true, Sort: 3},
	}
	for _, p := range packages {
		var count int64
		if err := gormDB.Model(&model.CreditPackage{}).Where("id = ?", p.ID).Count(&count).Error; err != nil {
			return fmt.Errorf("查询充值档位 %s 失败: %w", p.ID, err)
		}
		if count == 0 {
			if err := gormDB.Create(&p).Error; err != nil {
				return fmt.Errorf("写入充值档位 %s 失败: %w", p.ID, err)
			}
			slog.Info("已写入默认充值档位", "id", p.ID)
		}
	}

	for _, m := range seedModels() {
		var count int64
		if err := gormDB.Model(&model.ModelCatalog{}).Where("name = ?", m.Name).Count(&count).Error; err != nil {
			return fmt.Errorf("查询模型 %s 失败: %w", m.Name, err)
		}
		if count == 0 {
			m.ID = uuid.New()
			if err := gormDB.Create(&m).Error; err != nil {
				return fmt.Errorf("写入模型 %s 失败: %w", m.Name, err)
			}
			slog.Info("已写入示例模型", "name", m.Name)
		}
	}
	return nil
}

// seedModels 返回示例模型目录。价格矩阵的键集必须与 constraints 的键逐个对应，
// 第四期转发接口消费同一份 constraints，两侧都不能单独改名。
func seedModels() []model.ModelCatalog {
	imageConstraints := `{
		"size": ["1024x1024", "1024x768", "768x1024", "1536x864", "864x1536"],
		"quality": ["low", "medium", "high"],
		"n": { "max": 4 },
		"features": ["referenceImage", "mask"]
	}`
	imagePrices := `{
		"version": 1,
		"dimensions": ["size", "quality"],
		"prices": [
			{ "params": { "size": "1024x1024", "quality": "low" }, "costMicros": 200000 },
			{ "params": { "size": "1024x1024", "quality": "medium" }, "costMicros": 350000 },
			{ "params": { "size": "1024x1024", "quality": "high" }, "costMicros": 500000 },
			{ "params": { "size": "1024x768", "quality": "low" }, "costMicros": 180000 },
			{ "params": { "size": "1024x768", "quality": "medium" }, "costMicros": 320000 },
			{ "params": { "size": "1024x768", "quality": "high" }, "costMicros": 450000 },
			{ "params": { "size": "768x1024", "quality": "low" }, "costMicros": 180000 },
			{ "params": { "size": "768x1024", "quality": "medium" }, "costMicros": 320000 },
			{ "params": { "size": "768x1024", "quality": "high" }, "costMicros": 450000 },
			{ "params": { "size": "1536x864", "quality": "low" }, "costMicros": 250000 },
			{ "params": { "size": "1536x864", "quality": "medium" }, "costMicros": 400000 },
			{ "params": { "size": "1536x864", "quality": "high" }, "costMicros": 600000 },
			{ "params": { "size": "864x1536", "quality": "low" }, "costMicros": 250000 },
			{ "params": { "size": "864x1536", "quality": "medium" }, "costMicros": 400000 },
			{ "params": { "size": "864x1536", "quality": "high" }, "costMicros": 600000 }
		]
	}`
	videoConstraints := `{
		"resolution": ["480p", "720p", "1080p"],
		"duration": [4, 6, 8, 12],
		"ratio": ["16:9", "9:16", "1:1"],
		"features": ["referenceImage"]
	}`
	videoPrices := `{
		"version": 1,
		"dimensions": ["resolution", "duration"],
		"prices": [
			{ "params": { "resolution": "480p", "duration": 4 }, "costMicros": 400000 },
			{ "params": { "resolution": "480p", "duration": 6 }, "costMicros": 600000 },
			{ "params": { "resolution": "480p", "duration": 8 }, "costMicros": 800000 },
			{ "params": { "resolution": "480p", "duration": 12 }, "costMicros": 1200000 },
			{ "params": { "resolution": "720p", "duration": 4 }, "costMicros": 800000 },
			{ "params": { "resolution": "720p", "duration": 6 }, "costMicros": 1200000 },
			{ "params": { "resolution": "720p", "duration": 8 }, "costMicros": 1600000 },
			{ "params": { "resolution": "720p", "duration": 12 }, "costMicros": 2400000 },
			{ "params": { "resolution": "1080p", "duration": 4 }, "costMicros": 1500000 },
			{ "params": { "resolution": "1080p", "duration": 6 }, "costMicros": 2250000 },
			{ "params": { "resolution": "1080p", "duration": 8 }, "costMicros": 3000000 },
			{ "params": { "resolution": "1080p", "duration": 12 }, "costMicros": 4500000 }
		]
	}`
	return []model.ModelCatalog{
		{
			Name:              "gpt-image-1",
			DisplayName:       "GPT Image",
			Capability:        "image",
			Provider:          "openai",
			Constraints:       datatypes.JSON([]byte(imageConstraints)),
			CreditCost:        datatypes.JSON([]byte(imagePrices)),
			FreeTrialEligible: true,
			Enabled:           true,
			Sort:              10,
		},
		{
			Name:              "sora-video",
			DisplayName:       "Sora Video",
			Capability:        "video",
			Provider:          "openai",
			Constraints:       datatypes.JSON([]byte(videoConstraints)),
			CreditCost:        datatypes.JSON([]byte(videoPrices)),
			FreeTrialEligible: false,
			Enabled:           true,
			Sort:              20,
		},
	}
}
