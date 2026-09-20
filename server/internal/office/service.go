package office

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/platform/billing"
)

// 契约一错误（spec §5 错误码；形状沿用平台 errs 统一响应）。
var (
	errRunConflict      = errs.New(409, CodeRunConflict, "上一条消息还在执行中，请等它结束后再发")
	errCreditsExhausted = errs.New(400, CodeCreditsExhausted, "点数余额不足，请充值后再试")
	errEventsExpired    = errs.New(410, CodeEventsExpired, "事件流已过期，请刷新后从消息快照重建视图")
	errLastSeqInvalid   = errs.WithFields(errs.ErrValidation, map[string]string{"lastSeq": "lastSeq 必须是非负整数"})

	// errRunTerminal 是 ingest 的内部信号：run 已终态，按 spec §2.2「此后无事件」丢弃。
	errRunTerminal = errors.New("run 已终态，忽略后续事件")
)

// 孤儿回收边界值（spec §6，待报批默认值）：扫描间隔 60s、TTL 10min（E13）。
const (
	DefaultOrphanScanInterval = 60 * time.Second
	DefaultOrphanTTL          = 10 * time.Minute
)

// idAlphabet 采用 Crockford base32 小写子集：同前缀 ID 按字典序即按时间序（ULID 特性），
// A4 的 afterTurnId 增量直接用 turn_id 字符串比较实现。
const idAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// newID 生成「前缀 + 10 位毫秒时间 + 12 位随机」共 24 字符 id（spec §4 varchar(24)）。
func newID(prefix string) string {
	var chars [22]byte
	ms := time.Now().UnixMilli()
	for i := 9; i >= 0; i-- {
		chars[i] = idAlphabet[ms%32]
		ms /= 32
	}
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		panic(fmt.Sprintf("生成随机 id 失败: %v", err))
	}
	for i := 10; i < 22; i++ {
		chars[i] = idAlphabet[raw[i-10]%32]
	}
	return prefix + string(chars[:])
}

// hub 是 A6 实时订阅表：run id → 订阅通道集合。事件先落库再广播（design KP-1 步 5），
// 慢订阅者缓冲满即丢弃——客户端按 lastSeq 重连重放补齐，广播永远不阻塞编排路径。
type hub struct {
	mu      sync.Mutex
	entries map[string]*runSubs
}

type runSubs struct {
	mu   sync.Mutex
	subs map[chan Envelope]struct{}
}

func (h *hub) entry(runID string) *runSubs {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.entries == nil {
		h.entries = map[string]*runSubs{}
	}
	e, ok := h.entries[runID]
	if !ok {
		e = &runSubs{subs: map[chan Envelope]struct{}{}}
		h.entries[runID] = e
	}
	return e
}

func (h *hub) remove(runID string, e *runSubs) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(e.subs) == 0 {
		delete(h.entries, runID)
	}
}

func (e *runSubs) subscribe() (<-chan Envelope, func()) {
	ch := make(chan Envelope, 256)
	e.mu.Lock()
	e.subs[ch] = struct{}{}
	e.mu.Unlock()
	return ch, func() {
		e.mu.Lock()
		delete(e.subs, ch)
		e.mu.Unlock()
	}
}

// broadcast 非阻塞投递，调用方必须持有 entry.mu（与 ingest/finalize 相同的锁）。
func (e *runSubs) broadcast(env Envelope) {
	for ch := range e.subs {
		select {
		case ch <- env:
		default:
		}
	}
}

// Service 是 office 编排域服务：串行控制、run 生命周期、事件落库与转发、孤儿回收。
type Service struct {
	db      *gorm.DB
	billing *billing.Service
	runtime *RuntimeClient

	OrphanTTL          time.Duration
	OrphanScanInterval time.Duration

	hub hub
}

// NewService 组装编排域。共享密钥为空时随机生成并打日志（部署侧必须把同一值注入
// office-agent，否则全部契约二调用 401）。
func NewService(gormDB *gorm.DB, bill *billing.Service, cfg RuntimeConfig) *Service {
	if cfg.Token == "" {
		cfg.Token = newID("") + newID("")
		slog.Warn("OFFICE_INTERNAL_TOKEN 未配置，已随机生成（仅本进程生效，需同步注入 office-agent）")
	}
	return &Service{
		db:                 gormDB,
		billing:            bill,
		runtime:            NewRuntimeClient(cfg),
		OrphanTTL:          DefaultOrphanTTL,
		OrphanScanInterval: DefaultOrphanScanInterval,
	}
}

// CreateSession A1：创建会话。M1 无 agents 表，agentId 仅透传存储。
func (s *Service) CreateSession(userID uuid.UUID, agentID string) (OfficeSession, error) {
	id := newID("s")
	sess := OfficeSession{
		ID:            id,
		UserID:        userID.String(),
		AgentID:       agentID,
		WorkspacePath: id, // 相对段即 {sessionId}，卷内绝对路径由部署配置拼装
		Status:        "active",
	}
	if err := s.db.Create(&sess).Error; err != nil {
		return OfficeSession{}, err
	}
	return sess, nil
}

// PostMessageResult 是 A5 的结果：Replayed=true 表示同 clientMsgId 幂等命中既有 run。
type PostMessageResult struct {
	Run      OfficeRun
	Replayed bool
}

// PostMessage A5 发消息起 run（design KP-1 步 2）：幂等快路径 → 点数预检 → 事务内
// 锁会话行、幂等复检、活动 run 冲突检查（409）、落 user 消息、建 run(queued)、
// 回填 active_run_id；提交后异步调契约二（起跑失败按 E3 收敛，不阻塞响应）。
func (s *Service) PostMessage(ctx context.Context, sessionID string, userID uuid.UUID, content, clientMsgID string) (PostMessageResult, error) {
	if clientMsgID == "" {
		clientMsgID = newID("c")
	}
	// 幂等快路径：命中直接返回，不再做会话锁与点数预检（E1 幂等命中无感）。
	// 归属校验照做：别人会话的 clientMsgId 查不到任何东西（404，不泄露存在性）。
	var existing OfficeRun
	err := s.db.First(&existing, "session_id = ? AND client_msg_id = ?", sessionID, clientMsgID).Error
	if err == nil {
		var count int64
		if err := s.db.Model(&OfficeSession{}).
			Where("id = ? AND user_id = ?", sessionID, userID.String()).Count(&count).Error; err != nil {
			return PostMessageResult{}, err
		}
		if count == 0 {
			return PostMessageResult{}, errs.ErrNotFound
		}
		return PostMessageResult{Run: existing, Replayed: true}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return PostMessageResult{}, err
	}
	if err := s.PrecheckCredits(ctx, userID); err != nil {
		return PostMessageResult{}, err
	}

	var run OfficeRun
	created := false
	txErr := s.db.Transaction(func(tx *gorm.DB) error {
		var session OfficeSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&session, "id = ? AND user_id = ?", sessionID, userID.String()).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errs.ErrNotFound
			}
			return err
		}
		// 幂等复检：并发同键请求在会话行锁下收敛到同一条 run（E1，UNIQUE 兜底）。
		if err := tx.First(&run, "session_id = ? AND client_msg_id = ?", sessionID, clientMsgID).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if session.ActiveRunID != nil {
			return errRunConflict
		}

		runID := newID("r")
		run = OfficeRun{ID: runID, SessionID: sessionID, Status: RunQueued, ClientMsgID: clientMsgID}
		userMsg := OfficeMessage{
			ID:        newID("m"),
			SessionID: sessionID,
			RunID:     runID,
			Role:      "user",
			Content:   mustJSON(messageContent{SchemaVersion: messageSchemaVersion, Text: content}),
			ThreadID:  sessionID,
			TurnID:    runID,
		}
		if err := tx.Create(&userMsg).Error; err != nil {
			return err
		}
		if err := tx.Create(&run).Error; err != nil {
			return err
		}
		if err := tx.Model(&OfficeSession{}).Where("id = ?", sessionID).
			Update("active_run_id", runID).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	if txErr != nil {
		return PostMessageResult{}, txErr
	}
	if created {
		// 编排协程用独立 context：请求断开不影响 run 继续（E7）。
		go s.pumpRun(run.ID)
	}
	return PostMessageResult{Run: run, Replayed: !created}, nil
}

// pumpRun 单个 run 的编排协程：调契约二起跑，消费归一化事件流，逐事件落库分配 seq
// 并转发；run_started 迁移 queued→running（迁移主体是 Go）；done/error/流中断收敛终态。
func (s *Service) pumpRun(runID string) {
	var run OfficeRun
	if err := s.db.First(&run, "id = ?", runID).Error; err != nil {
		slog.Error("office pump 读取 run 失败", "run_id", runID, "err", err)
		return
	}
	var session OfficeSession
	if err := s.db.First(&session, "id = ?", run.SessionID).Error; err != nil {
		slog.Error("office pump 读取会话失败", "run_id", runID, "err", err)
		return
	}
	var userMsg OfficeMessage
	if err := s.db.First(&userMsg, "run_id = ? AND role = ?", runID, "user").Error; err != nil {
		slog.Error("office pump 读取用户消息失败", "run_id", runID, "err", err)
		return
	}
	var content messageContent
	_ = json.Unmarshal(userMsg.Content, &content)

	req := StartRunRequest{RunID: run.ID, SessionID: session.ID}
	req.Message.Role = "user"
	req.Message.Content = content.Text // attachmentIds M1 忽略（brief 裁决），附件清单发空

	events, stop, err := s.runtime.Start(context.Background(), req)
	if err != nil {
		// E3：起跑超时/失败 → run failed(runtime_unreachable)，清 active_run_id。
		slog.Warn("office 起跑失败", "run_id", runID, "err", err)
		s.finalizeRun(run, session, RunFailed, CodeRuntimeUnreachable, 0, 0, syntheticErrorEnvelope(runID, CodeRuntimeUnreachable))
		return
	}
	defer stop()

	for env := range events {
		switch env.Type {
		case EventRunStarted:
			// queued→running 的迁移主体是 Go（spec §7-2）；条件更新防重放。
			// 同时回写 model（来自事件 payload），管理面按模型统计依赖该列。
			var sp startPayload
			_ = json.Unmarshal(env.Payload, &sp)
			if err := s.db.Model(&OfficeRun{}).Where("id = ? AND status = ?", runID, RunQueued).
				Updates(map[string]any{"status": RunRunning, "started_at": time.Now(), "model": sp.Model}).Error; err != nil {
				slog.Error("office run 迁移 running 失败", "run_id", runID, "err", err)
			}
			if err := s.ingestEvent(runID, env); err != nil {
				slog.Warn("office 事件落库失败", "run_id", runID, "type", env.Type, "err", err)
			}
		case EventDone:
			var dp donePayload
			_ = json.Unmarshal(env.Payload, &dp)
			if err := s.ingestEvent(runID, env); err != nil {
				slog.Warn("office 事件落库失败", "run_id", runID, "type", env.Type, "err", err)
			}
			s.finalizeRun(run, session, RunSucceeded, "", int(dp.InputTokens), int(dp.OutputTokens), nil)
			return
		case EventError:
			var ep errorPayload
			_ = json.Unmarshal(env.Payload, &ep)
			code := ep.Code
			if code == "" {
				code = CodeRuntimeLost // 无码失败按契约二断连语义收敛
			}
			if err := s.ingestEvent(runID, env); err != nil {
				slog.Warn("office 事件落库失败", "run_id", runID, "type", env.Type, "err", err)
			}
			s.finalizeRun(run, session, RunFailed, code, 0, 0, nil)
			return
		default:
			// delta/notice/tool_call/... 及未知类型：透传落库（未知类型客户端忽略，向前兼容）。
			if err := s.ingestEvent(runID, env); err != nil {
				if errors.Is(err, errRunTerminal) {
					return // 终态后无事件：run 已被 cancel/回收收敛，停止消费
				}
				slog.Warn("office 事件落库失败", "run_id", runID, "type", env.Type, "err", err)
			}
		}
	}
	// 流结束但没有终态事件：E4 契约二断连 → failed(runtime_lost)。
	s.finalizeRun(run, session, RunFailed, CodeRuntimeLost, 0, 0, syntheticErrorEnvelope(runID, CodeRuntimeLost))
}

// ingestEvent 事件落库：run 内分配单调 seq（UNIQUE(run_id, seq) 兜底）后广播给订阅连接。
// run 已终态时丢弃（errRunTerminal）。同一 run 的落库/终态收敛共用 run 级互斥锁。
func (s *Service) ingestEvent(runID string, env Envelope) error {
	e := s.hub.entry(runID)
	e.mu.Lock()
	defer e.mu.Unlock()

	var seq int
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var run OfficeRun
		if err := tx.Select("status").First(&run, "id = ?", runID).Error; err != nil {
			return err
		}
		switch run.Status {
		case RunSucceeded, RunFailed, RunCancelled:
			return errRunTerminal
		}
		var maxSeq int
		if err := tx.Model(&OfficeEvent{}).Select("COALESCE(MAX(seq), 0)").
			Where("run_id = ?", runID).Scan(&maxSeq).Error; err != nil {
			return err
		}
		ev := OfficeEvent{RunID: runID, Seq: maxSeq + 1, Type: env.Type, Payload: datatypes.JSON(env.Payload)}
		if err := tx.Create(&ev).Error; err != nil {
			return err
		}
		seq = ev.Seq
		return nil
	})
	if err != nil {
		return err
	}
	out := newEnvelope(runID, env.Type, env.Payload)
	out.Seq = seq
	e.broadcast(out)
	return nil
}

// finalizeRun 终态收敛（design KP-1 步 7）：条件更新 run 终态（先落库者胜，spec §8
// just-succeeded）、清 active_run_id、落合成终态事件（被动收敛路径）、物化 assistant
// 消息（无文本跳过）；提交后照实扣点（幂等闸在 office_runs.credits_charged）。
func (s *Service) finalizeRun(run OfficeRun, session OfficeSession, status, errorCode string, tokensIn, tokensOut int, synthetic *Envelope) {
	e := s.hub.entry(run.ID)
	e.mu.Lock()
	defer e.mu.Unlock()

	won := false
	var broadcastEnv *Envelope
	txErr := s.db.Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{
			"status":      status,
			"finished_at": time.Now(),
			"tokens_in":   tokensIn,
			"tokens_out":  tokensOut,
			"credits":     float64(chargeMicros(int64(tokensOut))),
		}
		if errorCode != "" {
			updates["error_code"] = errorCode
		}
		res := tx.Model(&OfficeRun{}).
			Where("id = ? AND status IN ?", run.ID, []string{RunQueued, RunRunning}).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil // 已终态：终态取先落库者（spec §8 just-succeeded）
		}
		won = true
		if err := tx.Model(&OfficeSession{}).Where("active_run_id = ?", run.ID).
			Update("active_run_id", nil).Error; err != nil {
			return err
		}
		if synthetic != nil {
			var maxSeq int
			if err := tx.Model(&OfficeEvent{}).Select("COALESCE(MAX(seq), 0)").
				Where("run_id = ?", run.ID).Scan(&maxSeq).Error; err != nil {
				return err
			}
			ev := OfficeEvent{RunID: run.ID, Seq: maxSeq + 1, Type: synthetic.Type, Payload: datatypes.JSON(synthetic.Payload)}
			if err := tx.Create(&ev).Error; err != nil {
				return err
			}
			out := *synthetic
			out.Seq = ev.Seq
			out.TS = time.Now().UnixMilli()
			broadcastEnv = &out
		}
		return materializeAssistant(tx, run.ID, run.SessionID)
	})
	if txErr != nil {
		slog.Error("office 终态收敛失败", "run_id", run.ID, "status", status, "err", txErr)
		return
	}
	if !won {
		return
	}
	if broadcastEnv != nil {
		e.broadcast(*broadcastEnv)
	}
	userID, err := uuid.Parse(session.UserID)
	if err == nil {
		s.chargeCredits(run.ID, userID, int64(tokensOut))
	}
	s.hub.remove(run.ID, e)
}

// materializeAssistant 把 delta 事件按 seq 拼接物化为 assistant 消息（spec §7-1/7-5：
// 快照权威、物化不可逆；无文本则跳过）。事件是文本的唯一来源，cancel/孤儿回收的
// 「物化已有部分」同样走这里。
func materializeAssistant(tx *gorm.DB, runID, sessionID string) error {
	var events []OfficeEvent
	if err := tx.Where("run_id = ? AND type = ?", runID, EventDelta).Order("seq ASC").Find(&events).Error; err != nil {
		return err
	}
	var text strings.Builder
	for _, ev := range events {
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			continue // 坏帧跳过，不中断拼接
		}
		text.WriteString(payload.Text)
	}
	if text.Len() == 0 {
		return nil
	}
	return tx.Create(&OfficeMessage{
		ID:        newID("m"),
		SessionID: sessionID,
		RunID:     runID,
		Role:      "assistant",
		Content:   mustJSON(messageContent{SchemaVersion: messageSchemaVersion, Text: text.String()}),
		ThreadID:  sessionID,
		TurnID:    runID,
	}).Error
}

// syntheticErrorEnvelope 构造被动收敛路径（起跑失败/断连/强制取消/孤儿回收）的 error 事件。
func syntheticErrorEnvelope(runID, code string) *Envelope {
	payload, _ := json.Marshal(errorPayload{Code: code})
	env := newEnvelope(runID, EventError, payload)
	return &env
}

// CancelRun A7 取消（KP-3）：终态幂等 200；queued/running 转发契约二 cancel 后强制
// 置 cancelled（转发失败/超时也置，E12）。终态竞态由 finalizeRun 的条件更新裁决。
func (s *Service) CancelRun(ctx context.Context, userID uuid.UUID, runID string) (OfficeRun, error) {
	var run OfficeRun
	var session OfficeSession
	if err := s.db.First(&run, "id = ?", runID).Error; err != nil {
		return OfficeRun{}, errs.ErrNotFound
	}
	if err := s.db.First(&session, "id = ? AND user_id = ?", run.SessionID, userID.String()).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return OfficeRun{}, errs.ErrNotFound
		}
		return OfficeRun{}, err
	}
	switch run.Status {
	case RunSucceeded, RunFailed, RunCancelled:
		return run, nil // 幂等：终态直接 200
	}
	// 契约二 cancel 幂等（未知 runId 也 200）；失败仅记日志，强制收敛在 Go 侧兜底。
	cancelCtx, cancel := context.WithTimeout(ctx, runtimeCallTimeout)
	defer cancel()
	if err := s.runtime.Cancel(cancelCtx, runID); err != nil {
		slog.Warn("office 转发取消失败（已本地强制置 cancelled）", "run_id", runID, "err", err)
	}
	s.finalizeRun(run, session, RunCancelled, CodeCancelled, run.TokensIn, run.TokensOut, syntheticErrorEnvelope(runID, CodeCancelled))
	if err := s.db.First(&run, "id = ?", runID).Error; err != nil {
		return OfficeRun{}, err
	}
	return run, nil
}

// Subscribe A6 实时订阅入口：handler 先查库重放、再消费本通道。
func (s *Service) Subscribe(runID string) (<-chan Envelope, func()) {
	return s.hub.entry(runID).subscribe()
}

// ScanOrphans 孤儿回收（E13）：扫描 queued/running 且 updated_at 超 TTL 的 run，
// 对 Runtime 对账——无记录或查询失败即回收（置 failed(orphan_reclaimed)、清
// active_run_id、物化已有部分）；Runtime 仍有记录则跳过（M1 裁决）。
func (s *Service) ScanOrphans(ctx context.Context) (int, error) {
	cutoff := time.Now().Add(-s.OrphanTTL)
	var runs []OfficeRun
	if err := s.db.Where("status IN ? AND updated_at < ?", []string{RunQueued, RunRunning}, cutoff).
		Find(&runs).Error; err != nil {
		return 0, err
	}
	reclaimed := 0
	for _, run := range runs {
		if ctx.Err() != nil {
			return reclaimed, ctx.Err()
		}
		status, err := s.runtime.Status(ctx, run.ID)
		if err == nil && status.Exists {
			continue // Runtime 仍在跑，不动（M1 取舍：续转留 M4+）
		}
		var session OfficeSession
		if err := s.db.First(&session, "id = ?", run.SessionID).Error; err != nil {
			slog.Error("office 孤儿回收读取会话失败", "run_id", run.ID, "err", err)
			continue
		}
		slog.Warn("office 孤儿回收", "run_id", run.ID, "session_id", run.SessionID, "status", run.Status, "runtime_err", err)
		s.finalizeRun(run, session, RunFailed, CodeOrphanReclaimed, run.TokensIn, run.TokensOut, syntheticErrorEnvelope(run.ID, CodeOrphanReclaimed))
		reclaimed++
	}
	return reclaimed, nil
}

// StartOrphanScan 周期扫描协程：立即执行一次后按固定间隔重复，ctx 取消即停止。
func (s *Service) StartOrphanScan(ctx context.Context) {
	ticker := time.NewTicker(s.OrphanScanInterval)
	defer ticker.Stop()
	for {
		if n, err := s.ScanOrphans(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("office 孤儿扫描失败", "err", err)
		} else if n > 0 {
			slog.Warn("office 孤儿 run 已回收", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// mustJSON 序列化辅助：内容形态固定，序列化失败属程序错误。
func mustJSON(v any) datatypes.JSON {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("序列化 office 内容失败: %v", err))
	}
	return raw
}
