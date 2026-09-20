package office

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// 契约二边界值（spec §6，待报批默认值）：起跑响应头 10s（E3）、cancel 转发 5s（E12）。
const (
	runtimeStartHeaderTimeout = 10 * time.Second
	runtimeCallTimeout        = 5 * time.Second
	internalTokenHeader       = "X-Office-Internal-Token"
)

// RuntimeConfig 是契约二客户端配置：office-agent 内网地址与共享密钥。
type RuntimeConfig struct {
	BaseURL string
	Token   string
}

// StartRunRequest 是 B1 POST /v1/runs 的请求体（spec §3）。M1 无 agents/files 表，
// agent 配置与附件清单发空值，由 Runtime 侧默认兜底。
type StartRunRequest struct {
	RunID     string `json:"runId"`
	SessionID string `json:"sessionId"`
	Message   struct {
		Role        string                   `json:"role"`
		Content     string                   `json:"content"`
		Attachments []map[string]interface{} `json:"attachments,omitempty"`
	} `json:"message"`
	Agent struct {
		SystemPrompt string   `json:"systemPrompt,omitempty"`
		Model        string   `json:"model,omitempty"`
		Tools        []string `json:"tools,omitempty"`
		Skills       []string `json:"skills,omitempty"`
	} `json:"agent"`
}

// RuntimeStatus 是 B3 GET /v1/runs/:id 的响应体（E13 对账用）。
type RuntimeStatus struct {
	Exists    bool   `json:"exists"`
	Phase     string `json:"phase"`
	SessionID string `json:"sessionId"`
}

// RuntimeClient 契约二客户端：密钥头、POST /v1/runs（收 SSE）、cancel、status。
// SSE 流读取不设整体超时（run 可长跑），仅对响应头与短请求设边界超时。
type RuntimeClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewRuntimeClient(cfg RuntimeConfig) *RuntimeClient {
	return &RuntimeClient{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		token:   cfg.Token,
		http: &http.Client{
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: runtimeCallTimeout}).DialContext,
				ResponseHeaderTimeout: runtimeStartHeaderTimeout,
			},
		},
	}
}

// Start 调 B1 起 run，返回归一化事件通道。Runtime 的 seq 是临时编号，落库时重编。
// 通道关闭即流结束；调用方必须调用 stop 释放连接。响应头 10s 内未返回按 E3 报错。
func (c *RuntimeClient) Start(ctx context.Context, req StartRunRequest) (<-chan Envelope, func(), error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/runs", bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set(internalTokenHeader, c.token)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("runtime /v1/runs 返回 %d", resp.StatusCode)
	}

	events := make(chan Envelope, 64)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		parseSSE(resp.Body, func(env Envelope) { events <- env })
	}()
	stop := func() {
		// 关闭底层连接令读循环退出，进而关闭 events 通道（重复 Close 无害）。
		_ = resp.Body.Close()
	}
	return events, stop, nil
}

// Cancel 调 B2 取消。Runtime 对未知 runId 幂等返回 200，这里非 2xx 才算失败。
func (c *RuntimeClient) Cancel(ctx context.Context, runID string) error {
	ctx, cancel := context.WithTimeout(ctx, runtimeCallTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/runs/"+runID+"/cancel", nil)
	if err != nil {
		return err
	}
	req.Header.Set(internalTokenHeader, c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("runtime cancel 返回 %d", resp.StatusCode)
	}
	return nil
}

// Status 调 B3 查询 run 是否仍在 Runtime 上（E13 对账）。
func (c *RuntimeClient) Status(ctx context.Context, runID string) (RuntimeStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, runtimeCallTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/runs/"+runID, nil)
	if err != nil {
		return RuntimeStatus{}, err
	}
	req.Header.Set(internalTokenHeader, c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return RuntimeStatus{}, err
	}
	defer resp.Body.Close()
	var status RuntimeStatus
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return RuntimeStatus{}, fmt.Errorf("runtime status 返回 %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return RuntimeStatus{}, err
	}
	return status, nil
}

// parseSSE 逐块解析 SSE 数据行：只消费 data 行（信封 JSON 在 data 里，契约二不用
// event: 命名行），忽略注释与空行；单帧解析失败跳过（坏帧不中断整个 run）。
func parseSSE(body io.Reader, handle func(Envelope)) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var dataLines []string
	flush := func() {
		defer func() { dataLines = dataLines[:0] }()
		if len(dataLines) == 0 {
			return
		}
		var env Envelope
		if err := json.Unmarshal([]byte(strings.Join(dataLines, "\n")), &env); err != nil {
			return
		}
		handle(env)
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case line == "":
			flush()
		}
	}
	flush()
}
