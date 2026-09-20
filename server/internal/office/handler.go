package office

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/infinite-canvas/server/internal/errs"
	"github.com/infinite-canvas/server/internal/httpx"
)

// abort 统一错误出口：业务错误按其自带状态码写出，其余一律按内部错误兜底。
func abort(c *gin.Context, err error) {
	if appErr, ok := err.(*errs.AppError); ok {
		errs.Abort(c, appErr)
		return
	}
	errs.Abort(c, errs.ErrInternal)
}

// OfficeHandler 契约一 HTTP 端点。鉴权中间件由调用方挂在分组上
// （M1 仅登录态；office.read/write 权限点接入是 M3 任务）。
type OfficeHandler struct {
	svc *Service
}

func NewOfficeHandler(svc *Service) *OfficeHandler { return &OfficeHandler{svc: svc} }

// MountOfficeRoutes 注册契约一 A1–A9 路由（M1 范围，A8/A9 仅读表），照 canvas 包模板。
func MountOfficeRoutes(g *gin.RouterGroup, h *OfficeHandler) {
	g.POST("/sessions", h.CreateSession)
	g.GET("/sessions", h.ListSessions)
	g.GET("/sessions/:id", h.GetSession)
	g.GET("/sessions/:id/messages", h.ListMessages)
	g.POST("/sessions/:id/messages", h.PostMessage)
	g.GET("/sessions/:id/runs/:runId/stream", h.StreamRun)
	g.GET("/sessions/:id/artifacts", h.ListArtifacts)
	g.GET("/artifacts/:id", h.GetArtifact)
	// A7 取消：路径挂在 run 域，不带 session 段（前端只有 runId 时也能取消）。
	g.POST("/runs/:id/cancel", h.CancelRun)
}

// sessionResp 是会话的对外形状（A1/A2/A3 共用）。
type sessionResp struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	AgentID       string `json:"agentId"`
	ActiveRunID   string `json:"activeRunId"`
	WorkspacePath string `json:"workspacePath"`
	Status        string `json:"status"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

func toSessionResp(s OfficeSession) sessionResp {
	activeRunID := ""
	if s.ActiveRunID != nil {
		activeRunID = *s.ActiveRunID
	}
	return sessionResp{
		ID: s.ID, Title: s.Title, AgentID: s.AgentID, ActiveRunID: activeRunID,
		WorkspacePath: s.WorkspacePath, Status: s.Status,
		CreatedAt: httpx.FormatTime(s.CreatedAt), UpdatedAt: httpx.FormatTime(s.UpdatedAt),
	}
}

// CreateSession A1：创建会话（body: {agentId?}，M1 无 agents 表仅透传存储）。
func (h *OfficeHandler) CreateSession(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	var body struct {
		AgentID string `json:"agentId"`
	}
	_ = c.ShouldBindJSON(&body) // body 可省略
	sess, err := h.svc.CreateSession(uid, body.AgentID)
	if err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	c.JSON(http.StatusCreated, toSessionResp(sess))
}

// officeCursorLayout 与 SQLite 驱动的落库文本格式一致（保留原时区偏移）：
// SQLite 按字符串比较时间列，绑定时必须产生与存储一致的文本；MySQL 8.0.19+
// 的 DATETIME 比较也能解析带偏移的字面量，双方言等价。
const officeCursorLayout = "2006-01-02 15:04:05.999999999-07:00"

// encodeOfficeCursor / decodeOfficeCursor：updated_at|id 的不透明游标（A2 分页）。
func encodeOfficeCursor(t time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.Format(officeCursorLayout) + "|" + id))
}

func decodeOfficeCursor(raw string) (time.Time, string, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, "", false
	}
	tRaw, idRaw, ok := strings.Cut(string(decoded), "|")
	if !ok {
		return time.Time{}, "", false
	}
	t, err := time.Parse(officeCursorLayout, tRaw)
	if err != nil {
		return time.Time{}, "", false
	}
	return t, idRaw, true
}

// ListSessions A2：会话列表，updated_at 倒序，?cursor=&limit=（默认 20 最大 100）。
func (h *OfficeHandler) ListSessions(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	limit := httpx.DefaultPageSize
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > httpx.MaxPageSize {
			errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"limit": "limit 必须是 1-100 的整数"}))
			return
		}
		limit = n
	}
	query := h.svc.db.Model(&OfficeSession{}).Where("user_id = ?", uid.String())
	if raw := c.Query("cursor"); raw != "" {
		cursorAt, cursorID, ok := decodeOfficeCursor(raw)
		if !ok {
			errs.Abort(c, errs.ErrValidation)
			return
		}
		query = query.Where("(updated_at < ?) OR (updated_at = ? AND id < ?)", cursorAt, cursorAt, cursorID)
	}
	var sessions []OfficeSession
	if err := query.Order("updated_at DESC, id DESC").Limit(limit + 1).Find(&sessions).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	nextCursor := ""
	if len(sessions) > limit {
		last := sessions[limit-1]
		nextCursor = encodeOfficeCursor(last.UpdatedAt, last.ID)
		sessions = sessions[:limit]
	}
	items := make([]sessionResp, 0, len(sessions))
	for _, s := range sessions {
		items = append(items, toSessionResp(s))
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": nextCursor})
}

// loadSession 校验会话存在且属于当前用户（归属不符按不存在处理，不泄露存在性）。
func (h *OfficeHandler) loadSession(c *gin.Context) (OfficeSession, bool) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return OfficeSession{}, false
	}
	var session OfficeSession
	err := h.svc.db.First(&session, "id = ? AND user_id = ?", c.Param("id"), uid.String()).Error
	if err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return OfficeSession{}, false
	}
	return session, true
}

// GetSession A3：会话详情（含 activeRunId）。
func (h *OfficeHandler) GetSession(c *gin.Context) {
	session, ok := h.loadSession(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, toSessionResp(session))
}

// messageResp 是消息快照条目（A4），content 按存储 JSON 原样返回。
type messageResp struct {
	ID        string          `json:"id"`
	SessionID string          `json:"sessionId"`
	RunID     string          `json:"runId"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	ThreadID  string          `json:"threadId"`
	TurnID    string          `json:"turnId"`
	CreatedAt string          `json:"createdAt"`
}

// ListMessages A4：消息快照（权威；?afterTurnId= 增量，按 turn_id 的时间可排序特性过滤）。
func (h *OfficeHandler) ListMessages(c *gin.Context) {
	session, ok := h.loadSession(c)
	if !ok {
		return
	}
	query := h.svc.db.Where("session_id = ?", session.ID)
	if after := c.Query("afterTurnId"); after != "" {
		query = query.Where("turn_id > ?", after)
	}
	var messages []OfficeMessage
	if err := query.Order("created_at ASC, id ASC").Find(&messages).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]messageResp, 0, len(messages))
	for _, m := range messages {
		items = append(items, messageResp{
			ID: m.ID, SessionID: m.SessionID, RunID: m.RunID, Role: m.Role,
			Content: json.RawMessage(m.Content), ThreadID: m.ThreadID, TurnID: m.TurnID,
			CreatedAt: httpx.FormatTime(m.CreatedAt),
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// PostMessage A5：发消息起 run。201 新建 / 200 幂等重放 / 409 run_conflict /
// 400 credits_exhausted（预检）。attachmentIds M1 忽略（brief 裁决）。
func (h *OfficeHandler) PostMessage(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	var body struct {
		Content       string   `json:"content"`
		ClientMsgID   string   `json:"clientMsgId"`
		AttachmentIDs []string `json:"attachmentIds"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		errs.Abort(c, errs.ErrValidation)
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		errs.Abort(c, errs.WithFields(errs.ErrValidation, map[string]string{"content": "content 不能为空"}))
		return
	}
	result, err := h.svc.PostMessage(c.Request.Context(), c.Param("id"), uid, body.Content, body.ClientMsgID)
	if err != nil {
		abort(c, err)
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	c.JSON(status, gin.H{
		"runId":       result.Run.ID,
		"sessionId":   result.Run.SessionID,
		"status":      result.Run.Status,
		"clientMsgId": result.Run.ClientMsgID,
	})
}

// CancelRun A7：取消（幂等）。queued/running 之外的终态直接 200。
func (h *OfficeHandler) CancelRun(c *gin.Context) {
	uid, ok := httpx.CurrentUserID(c)
	if !ok {
		return
	}
	run, err := h.svc.CancelRun(c.Request.Context(), uid, c.Param("id"))
	if err != nil {
		abort(c, err)
		return
	}
	resp := gin.H{"runId": run.ID, "sessionId": run.SessionID, "status": run.Status}
	if run.ErrorCode != nil {
		resp["errorCode"] = *run.ErrorCode
	}
	c.JSON(http.StatusOK, resp)
}

// artifactResp 是产物条目（A8/A9）；kind=html 时 sourceView=true（源码视图标记，不渲染富内容）。
type artifactResp struct {
	ID         string `json:"id"`
	SessionID  string `json:"sessionId"`
	RunID      string `json:"runId"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Mime       string `json:"mime"`
	Size       int64  `json:"size"`
	StorageKey string `json:"storageKey"`
	SourceView bool   `json:"sourceView"`
	CreatedAt  string `json:"createdAt"`
}

func toArtifactResp(a OfficeArtifact) artifactResp {
	return artifactResp{
		ID: a.ID, SessionID: a.SessionID, RunID: a.RunID, Kind: a.Kind, Name: a.Name,
		Mime: a.Mime, Size: a.Size, StorageKey: a.StorageKey, SourceView: a.Kind == "html",
		CreatedAt: httpx.FormatTime(a.CreatedAt),
	}
}

// ListArtifacts A8：会话产物列表。
func (h *OfficeHandler) ListArtifacts(c *gin.Context) {
	session, ok := h.loadSession(c)
	if !ok {
		return
	}
	var artifacts []OfficeArtifact
	if err := h.svc.db.Where("session_id = ?", session.ID).Order("created_at ASC, id ASC").
		Find(&artifacts).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}
	items := make([]artifactResp, 0, len(artifacts))
	for _, a := range artifacts {
		items = append(items, toArtifactResp(a))
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// GetArtifact A9：产物详情（kind=html 返回源码视图标记）。
func (h *OfficeHandler) GetArtifact(c *gin.Context) {
	session, ok := h.loadSession(c)
	if !ok {
		return
	}
	var artifact OfficeArtifact
	if err := h.svc.db.First(&artifact, "id = ? AND session_id = ?", c.Param("id"), session.ID).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}
	c.JSON(http.StatusOK, toArtifactResp(artifact))
}

// officeSSE 是 A6 的 SSE 写出器：id 行承载 seq（原生 Last-Event-ID 重连语义），
// event 行承载 type，data 行是完整信封 JSON。
type officeSSE struct {
	writer  gin.ResponseWriter
	flusher http.Flusher
}

func newOfficeSSE(c *gin.Context) *officeSSE {
	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// 应用层就地关闭 nginx 缓冲（与 /api/v1/ai/ 流式同口径）。
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	w.Flush()
	return &officeSSE{writer: w, flusher: w.(http.Flusher)}
}

func (s *officeSSE) writeEvent(env Envelope) error {
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.writer, "id: %d\nevent: %s\ndata: %s\n\n", env.Seq, env.Type, raw); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s *officeSSE) comment(text string) error {
	if _, err := fmt.Fprintf(s.writer, ": %s\n\n", text); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// sseHeartbeat 间隔（spec §6 nginx 600s 之外的保活节拍，沿用 /api/v1/ai/ 的 15s）。
const sseHeartbeat = 15 * time.Second

// StreamRun A6：SSE 事件流。先重放 seq > lastSeq（?lastSeq= 或 Last-Event-ID 头，
// 查询参数优先），再挂实时通道；lastSeq 落在已清理区间（小于现存最小 seq）返回 410；
// 终态事件送出或 run 已终态即结束；客户端断开不影响 run 继续（E7）。
func (h *OfficeHandler) StreamRun(c *gin.Context) {
	session, ok := h.loadSession(c)
	if !ok {
		return
	}
	var run OfficeRun
	if err := h.svc.db.First(&run, "id = ? AND session_id = ?", c.Param("runId"), session.ID).Error; err != nil {
		errs.Abort(c, errs.ErrNotFound)
		return
	}

	lastSeq := 0
	if raw := c.Query("lastSeq"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			abort(c, errLastSeqInvalid)
			return
		}
		lastSeq = n
	} else if raw := c.GetHeader("Last-Event-ID"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			abort(c, errLastSeqInvalid)
			return
		}
		lastSeq = n
	}

	// 重放空洞检测（E11）：M1 无事件清理，现存最小 seq 即水位代理；
	// lastSeq 严格小于最小 seq 说明客户端手里的事件已被清理，转 410 快照重建。
	if lastSeq > 0 {
		var minSeq *int
		if err := h.svc.db.Model(&OfficeEvent{}).Select("MIN(seq)").
			Where("run_id = ?", run.ID).Scan(&minSeq).Error; err != nil {
			errs.Abort(c, errs.ErrInternal)
			return
		}
		if minSeq != nil && lastSeq < *minSeq {
			abort(c, errEventsExpired)
			return
		}
	}

	events, unsub := h.svc.Subscribe(run.ID)
	defer unsub()

	// 先订阅后查询：查询期间新产生的事件会进缓冲通道，按 lastSent 去重不丢不重。
	var replayed []OfficeEvent
	if err := h.svc.db.Where("run_id = ? AND seq > ?", run.ID, lastSeq).
		Order("seq ASC").Find(&replayed).Error; err != nil {
		errs.Abort(c, errs.ErrInternal)
		return
	}

	stream := newOfficeSSE(c)
	lastSent := lastSeq
	for _, ev := range replayed {
		if ev.Seq <= lastSent {
			continue
		}
		if err := stream.writeEvent(envelopeOf(ev)); err != nil {
			return
		}
		lastSent = ev.Seq
	}
	// run 已终态：重放到末尾即补发了终态事件，直接收流。
	if run.Status == RunSucceeded || run.Status == RunFailed || run.Status == RunCancelled {
		return
	}

	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			if err := stream.comment("ping"); err != nil {
				return
			}
		case env := <-events:
			if env.Seq <= lastSent {
				continue
			}
			if err := stream.writeEvent(env); err != nil {
				return
			}
			lastSent = env.Seq
			if isTerminalEvent(env.Type) {
				return
			}
		}
	}
}

func envelopeOf(ev OfficeEvent) Envelope {
	env := newEnvelope(ev.RunID, ev.Type, json.RawMessage(ev.Payload))
	env.Seq = ev.Seq
	env.TS = ev.CreatedAt.UnixMilli()
	return env
}
