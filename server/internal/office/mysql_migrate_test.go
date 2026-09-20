package office

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/infinite-canvas/server/internal/db"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/platform/billing"
)

// TestMySQLMigrate 在真实 MySQL 8.4 上执行 office 域迁移（仓库红线：SQLite 单测
// 覆盖不到字符串列长度与 MySQL 保留字坑，涉及新表必须真库跑一次）。
// 默认跳过；设 OFFICE_MYSQL_DSN（如
// root:pass@tcp(127.0.0.1:3306)/office_check?charset=utf8mb4&parseTime=True&loc=Local）时执行。
func TestMySQLMigrate(t *testing.T) {
	dsn := os.Getenv("OFFICE_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未设置 OFFICE_MYSQL_DSN，跳过真实 MySQL 迁移验证")
	}
	g, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("连接 MySQL 失败: %v", err)
	}
	// 与生产启动同序：先平台全量表，再 office 域表（main.go 的调用顺序）。
	if err := db.Migrate(g); err != nil {
		t.Fatalf("平台 AutoMigrate 失败: %v", err)
	}
	if err := Migrate(g); err != nil {
		t.Fatalf("office AutoMigrate 失败: %v", err)
	}
	// 迁移幂等：重复执行不再报错。
	if err := Migrate(g); err != nil {
		t.Fatalf("office AutoMigrate 重复执行失败: %v", err)
	}

	// 冒烟：点数账户 + 会话/run/事件/消息/产物各写一行，覆盖索引与 JSON 列。
	ctx := context.Background()
	userID := uuid.New()
	if err := billing.NewService(g, model.ProductCanvas).EnsureAccount(g, userID); err != nil {
		t.Fatalf("建点数账户失败: %v", err)
	}
	sessionID := newID("s")
	sess := OfficeSession{ID: sessionID, UserID: userID.String(), WorkspacePath: sessionID, Status: "active"}
	if err := g.WithContext(ctx).Create(&sess).Error; err != nil {
		t.Fatalf("写会话失败: %v", err)
	}
	runID := newID("r")
	run := OfficeRun{
		ID: runID, SessionID: sess.ID, Status: RunSucceeded, ClientMsgID: "smoke",
		StartedAt: ptrTime(time.Now()), FinishedAt: ptrTime(time.Now()),
	}
	if err := g.WithContext(ctx).Create(&run).Error; err != nil {
		t.Fatalf("写 run 失败: %v", err)
	}
	if err := g.WithContext(ctx).Create(&OfficeEvent{RunID: runID, Seq: 1, Type: EventDone, Payload: []byte(`{"inputTokens":1}`)}).Error; err != nil {
		t.Fatalf("写事件失败: %v", err)
	}
	if err := g.WithContext(ctx).Create(&OfficeMessage{
		ID: newID("m"), SessionID: sess.ID, RunID: runID, Role: "assistant",
		Content: []byte(`{"schema_version":1,"text":"ok"}`), ThreadID: sess.ID, TurnID: runID,
	}).Error; err != nil {
		t.Fatalf("写消息失败: %v", err)
	}
	if err := g.WithContext(ctx).Create(&OfficeArtifact{
		ID: newID("a"), SessionID: sess.ID, RunID: runID, Kind: "markdown", Name: "n.md", StorageKey: "k",
	}).Error; err != nil {
		t.Fatalf("写产物失败: %v", err)
	}

	for _, table := range []string{"office_sessions", "office_runs", "office_events", "office_messages", "office_artifacts"} {
		rows, err := g.Raw("SHOW CREATE TABLE " + table).Rows()
		if err != nil {
			t.Fatalf("SHOW CREATE TABLE %s 失败: %v", table, err)
		}
		for rows.Next() {
			var name, ddl string
			if err := rows.Scan(&name, &ddl); err != nil {
				t.Fatal(err)
			}
			t.Logf("SHOW CREATE TABLE %s:\n%s\n", table, ddl)
		}
		rows.Close()
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
