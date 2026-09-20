package blog

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Revalidator 按需再验证通知器：内容变更后回调前台 blog-web 的 /api/revalidate，
// 失效对应缓存标签与路径。Endpoint 未配置（本地单跑 Go）时为 no-op。
// 回调异步执行，失败仅记日志——兜底是前台缓存的固定过期窗口。
type Revalidator struct {
	Endpoint string // blog-web 基址，如 http://blog-web:3000；空串表示关闭
	Secret   string // 与前台共享的 REVALIDATE_SECRET
	client   *http.Client
}

// NewRevalidator 构造再验证器；endpoint 为空表示前台未部署（本地单跑 Go），
// 返回 nil，调用方对 nil 判空跳过即可。
func NewRevalidator(endpoint, secret string) *Revalidator {
	if endpoint == "" {
		return nil
	}
	return &Revalidator{Endpoint: endpoint, Secret: secret, client: &http.Client{Timeout: 3 * time.Second}}
}

type revalidatePayload struct {
	Tags  []string `json:"tags"`
	Paths []string `json:"paths,omitempty"`
}

// NotifyList 失效文章列表缓存（首页/栏目/收藏等列表页共用 posts 标签）。
func (r *Revalidator) NotifyList() { r.send(revalidatePayload{Tags: []string{"posts"}}) }

// NotifyPost 失效某篇文章详情页（发布/下架/删除/评论管理动作后调用）。
func (r *Revalidator) NotifyPost(slug string) {
	r.send(revalidatePayload{Tags: []string{"posts"}, Paths: []string{"/posts/" + slug}})
}

// NotifyTopics 失效栏目缓存。
func (r *Revalidator) NotifyTopics() { r.send(revalidatePayload{Tags: []string{"topics"}}) }

// send 异步投递；网络失败只记日志，不影响主流程。
func (r *Revalidator) send(payload revalidatePayload) {
	if r == nil || r.Endpoint == "" {
		return
	}
	go func() {
		body, err := json.Marshal(payload)
		if err != nil {
			slog.Error("再验证载荷序列化失败", "err", err)
			return
		}
		req, err := http.NewRequest(http.MethodPost,
			strings.TrimRight(r.Endpoint, "/")+"/api/revalidate", bytes.NewReader(body))
		if err != nil {
			slog.Error("再验证请求构建失败", "err", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Revalidate-Secret", r.Secret)
		resp, err := r.client.Do(req)
		if err != nil {
			slog.Warn("前台再验证回调失败（缓存将在兜底窗口后自动过期）", "err", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			slog.Warn("前台再验证回调返回异常状态", "status", resp.StatusCode)
		}
	}()
}
