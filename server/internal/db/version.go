package db

import (
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"gorm.io/gorm"
)

// schemaMigration 是 schema_migrations 表的轻量记录。AutoMigrate 没有结构回滚，
// 先用该表留下「哪个代码版本建出了当前结构」的痕迹；真正的版本化迁移是后续工程。
type schemaMigration struct {
	Version     string    `gorm:"column:version;size:64;index"`
	AppliedAt   time.Time `gorm:"column:applied_at"`
	TableCount  int       `gorm:"column:table_count"`
	ColumnCount int       `gorm:"column:column_count"`
}

func (schemaMigration) TableName() string { return "schema_migrations" }

// RecordSchemaVersion 在 AutoMigrate 成功后调用：向 schema_migrations 写入一行
// 当前版本记录（版本号、时间与库内表/列数量统计，表不存在则先建），并打印当前 schema 版本。
// 只记录不回滚；记录失败不应阻断启动，调用方仅记日志即可。
func RecordSchemaVersion(gormDB *gorm.DB) error {
	version := buildVersion()
	if err := gormDB.AutoMigrate(&schemaMigration{}); err != nil {
		return err
	}
	tableCount, columnCount, err := schemaStats(gormDB)
	if err != nil {
		return err
	}
	record := schemaMigration{Version: version, AppliedAt: time.Now(), TableCount: tableCount, ColumnCount: columnCount}
	if err := gormDB.Create(&record).Error; err != nil {
		return err
	}
	slog.Info("当前 schema 版本", "version", version, "tables", tableCount, "columns", columnCount)
	return nil
}

// buildVersion 优先读 VERSION 文件（本地 go run 在工作目录或上级目录）；
// 容器镜像内没有该文件，退回构建注入的 git 提交号，再退回 dev。
func buildVersion() string {
	for _, path := range []string{"VERSION", "../VERSION"} {
		if data, err := os.ReadFile(path); err == nil {
			if v := strings.TrimSpace(string(data)); v != "" {
				return v
			}
		}
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value
			}
		}
	}
	return "dev"
}

// schemaStats 统计当前库的表数量与列总数量；跨 MySQL / SQLite 走 gorm Migrator。
func schemaStats(gormDB *gorm.DB) (tables, columns int, err error) {
	tableList, err := gormDB.Migrator().GetTables()
	if err != nil {
		return 0, 0, err
	}
	for _, table := range tableList {
		columnTypes, err := gormDB.Migrator().ColumnTypes(table)
		if err != nil {
			return 0, 0, err
		}
		columns += len(columnTypes)
	}
	return len(tableList), columns, nil
}

// CurrentVersion 返回当前代码版本：VERSION 文件优先，其次构建信息里的提交号。
// 供 /admin/meta 等需要展示产品版本的地方使用。
func CurrentVersion() string {
	return buildVersion()
}
