package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/infinite-canvas/server/internal/crypto"
	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/provider"
)

// UpstreamTimeouts 描述各能力的超时。生图与生视频的耗时差一个数量级，必须分开设置。
type UpstreamTimeouts struct {
	ImageConnect   time.Duration
	ImageFirstByte time.Duration
	ImageTotal     time.Duration
	SpeechTotal    time.Duration
	StreamTotal    time.Duration
	StreamIdle     time.Duration
	VideoCreate    time.Duration
	VideoPoll      time.Duration
	VideoTask      time.Duration
}

// DefaultUpstreamTimeouts 返回各能力的默认超时配置。
func DefaultUpstreamTimeouts() UpstreamTimeouts {
	return UpstreamTimeouts{
		ImageConnect:   10 * time.Second,
		ImageFirstByte: 60 * time.Second,
		ImageTotal:     180 * time.Second,
		SpeechTotal:    120 * time.Second,
		StreamTotal:    600 * time.Second,
		StreamIdle:     60 * time.Second,
		VideoCreate:    60 * time.Second,
		VideoPoll:      30 * time.Second,
		VideoTask:      20 * time.Minute,
	}
}

// UpstreamService 负责渠道选择、故障转移、超时配置与结果下载的出站防护。
// 服务端主动发起的外部请求只有两类：平台上游与平台上游返回的结果 URL。
type UpstreamService struct {
	db      *gorm.DB
	cipher  *crypto.Cipher
	http    *http.Client
	timeout UpstreamTimeouts
	// AllowPrivate 仅供本地调试：允许上游地址落在内网。
	AllowPrivate bool
}

func NewUpstreamService(db *gorm.DB, cipher *crypto.Cipher, timeout UpstreamTimeouts) *UpstreamService {
	service := &UpstreamService{db: db, cipher: cipher, timeout: timeout}
	service.http = &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return service.dialContext(ctx, network, address)
			},
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("重定向次数过多")
			}
			return service.checkURL(req.Context(), req.URL.String())
		},
	}
	return service
}

func (s *UpstreamService) SetAllowPrivate(allow bool) { s.AllowPrivate = allow }

func (s *UpstreamService) Timeouts() UpstreamTimeouts { return s.timeout }

// TimeoutFor 返回能力的整体超时，用于启动收敛判断「running 是否超时」。
func (s *UpstreamService) TimeoutFor(capability string) time.Duration {
	switch capability {
	case "image":
		return s.timeout.ImageTotal
	case "audio":
		return s.timeout.SpeechTotal
	case "text":
		return s.timeout.StreamTotal
	case "video":
		return s.timeout.VideoTask
	default:
		return s.timeout.ImageTotal
	}
}

// ChannelWithProvider 是一次渠道解析结果：渠道行与组装好的 provider。
type ChannelWithProvider struct {
	Channel  model.PlatformChannel
	Provider provider.Provider
}

// LoadChannels 按 model_catalog.channel_ids 的顺序取 enabled 的渠道并组装 provider。
func (s *UpstreamService) LoadChannels(ctx context.Context, channelIDs []uuid.UUID) ([]ChannelWithProvider, error) {
	if len(channelIDs) == 0 {
		return nil, errors.New("该模型没有绑定平台渠道")
	}
	var channels []model.PlatformChannel
	if err := s.db.WithContext(ctx).Where("id IN ?", channelIDs).Find(&channels).Error; err != nil {
		return nil, err
	}
	byID := map[uuid.UUID]model.PlatformChannel{}
	for _, channel := range channels {
		byID[channel.ID] = channel
	}
	ordered := make([]ChannelWithProvider, 0, len(channelIDs))
	for _, id := range channelIDs {
		channel, ok := byID[id]
		if !ok || !channel.Enabled {
			continue
		}
		built, err := s.ProviderFor(channel)
		if err != nil {
			slog.Error("组装平台渠道失败", "channel", channel.ID, "err", err)
			continue
		}
		ordered = append(ordered, ChannelWithProvider{Channel: channel, Provider: built})
	}
	if len(ordered) == 0 {
		return nil, errors.New("没有可用的平台渠道")
	}
	return ordered, nil
}

// ProviderFor 解密渠道 Key 并构造对应格式的 provider。解密后的明文不进缓存、不进日志。
func (s *UpstreamService) ProviderFor(channel model.PlatformChannel) (provider.Provider, error) {
	plaintext, err := s.cipher.Decrypt(channel.Nonce, channel.Payload)
	if err != nil {
		return nil, err
	}
	apiKey := string(plaintext)
	switch channel.APIFormat {
	case "openai":
		return provider.NewOpenAI(channel.BaseURL, apiKey, s.http), nil
	case "gemini":
		return provider.NewGemini(channel.BaseURL, apiKey, s.http), nil
	case "ark":
		return provider.NewArk(channel.BaseURL, apiKey, s.http), nil
	default:
		return nil, fmt.Errorf("未知的渠道格式: %s", channel.APIFormat)
	}
}

// WithTimeout 返回带整体超时的 context。
func (s *UpstreamService) WithTimeout(ctx context.Context, capability string) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.TimeoutFor(capability))
}

// Download 下载上游返回的结果 URL，带出站防护、大小限制与超时。
// 每一跳都会重新校验地址，防止重定向绕过与 DNS rebinding。
func (s *UpstreamService) Download(ctx context.Context, rawURL string, maxBytes int64, timeout time.Duration) ([]byte, string, error) {
	if err := s.checkURL(ctx, rawURL); err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("结果下载失败: HTTP %d", resp.StatusCode)
	}
	limit := maxBytes
	if limit <= 0 {
		// 未指定上限时的兜底：512MB，防止异常响应把内存读爆。
		limit = 512 << 20
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > limit {
		return nil, "", errors.New("结果超过单文件大小限制")
	}
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return data, mimeType, nil
}

// checkURL 校验目标 URL：只允许 http/https，且解析出的 IP 不能是回环、私有或链路本地地址。
func (s *UpstreamService) checkURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("结果地址不合法: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("结果地址协议不被允许")
	}
	if s.AllowPrivate {
		return nil
	}
	host := parsed.Hostname()
	if host == "" {
		return errors.New("结果地址缺少主机名")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("解析结果地址失败: %w", err)
	}
	for _, address := range addresses {
		if isBlockedIP(address.IP) {
			return errors.New("结果地址指向内网，已拒绝下载")
		}
	}
	return nil
}

// dialContext 在建立连接时校验实际 IP，防止 DNS rebinding 绕过解析期校验。
func (s *UpstreamService) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if s.AllowPrivate {
		return dialer.DialContext(ctx, network, address)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, resolved := range addresses {
		if isBlockedIP(resolved.IP) {
			lastErr = errors.New("目标地址解析到内网，已拒绝连接")
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("目标地址不可达")
	}
	return nil, lastErr
}

func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	// 云元数据地址与运营商级 NAT 地址一并拒绝。
	if octets := ip.To4(); octets != nil {
		if octets[0] == 169 && octets[1] == 254 {
			return true
		}
		if octets[0] == 100 && octets[1] >= 64 && octets[1] <= 127 {
			return true
		}
	}
	return false
}

// ShouldFailover 判断上游错误是否值得切到下一个渠道：只在尚未产生输出时，
// 由 handler 在调用侧保证。能力不支持与未打标的本地错误都不切。
func ShouldFailover(err error) bool {
	if errors.Is(err, provider.ErrCapabilityUnsupported) {
		return false
	}
	return IsRetryableUpstream(err)
}

// IsRetryableUpstream 按规划的重试表分类：连接层失败（ErrUpstream{Status:0}，连接拒绝、
// DNS 失败等）与上游 5xx 可原渠道重试一次；429、400、422、401、403 是上游明确拒绝，
// 重试还是错，一律不重试；ctx 取消与未打标的本地错误也不重试。
func IsRetryableUpstream(err error) bool {
	var up *provider.ErrUpstream
	if !errors.As(err, &up) {
		return false
	}
	switch up.Status {
	case 0, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// UpstreamStatus 从错误里取出上游状态码，供错误响应与日志使用。
func UpstreamStatus(err error) int {
	var upstream *provider.ErrUpstream
	if errors.As(err, &upstream) {
		return upstream.Status
	}
	return 0
}

// Cipher 返回加解密器，管理后台写 Key 时复用。
func (s *UpstreamService) Cipher() *crypto.Cipher { return s.cipher }

// HTTPClient 供 provider 复用（带出站防护的传输层）。
func (s *UpstreamService) HTTPClient() *http.Client { return s.http }

// TrimBaseURL 统一去掉末尾斜杠，避免拼出双斜杠路径。
func TrimBaseURL(raw string) string { return strings.TrimRight(raw, "/") }
