package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port          string
	DatabaseURL   string
	JWTSecret     string
	CredentialKey string // 本期不用，但按规划一并校验
	AppBaseURL    string

	AdminEmail    string
	AdminPassword string

	// SeedTestData 为 true 时启动过程会幂等写入固定的测试账号与示例数据。
	// 只允许测试/预发布环境开启：测试账号密码是公开的，正式环境开启等于开了一个后门。
	SeedTestData         bool
	SeedTestDataEmail    string
	SeedTestDataPassword string

	CookieSecure bool
	LogLevel     string

	// TrustedProxies 是可信反向代理网段（CIDR）。为空表示不信任任何代理头，
	// ClientIP 直接使用 RemoteAddr；仅当来源命中这些网段时才解析 X-Real-IP / X-Forwarded-For。
	TrustedProxies []string

	// CORSAllowedOrigins 是允许跨源调用 API 的来源白名单（完整来源，如 https://sim-admin.youc.online）。
	// 为空表示完全不启用 CORS，行为与没有 CORS 处理时一致；仅管理后台独立部署时按环境显式开启。
	CORSAllowedOrigins []string

	MailDriver   string // smtp | log
	SMTPHost     string
	SMTPPort     string
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPSecurity string // ssl | starttls

	RegistrationEnabled bool

	FreeGrantEnabled           bool
	FreeGrantCampaignID        string
	FreeGrantDailyBudgetMicros int64
	FreeGrantRiskThreshold     int

	// 点数与支付（第三期）
	EntitlementDays     int
	PromotionEnabled    bool
	PaymentNotifyURL    string
	AlipayAppID         string
	AlipayPrivateKey    string
	AlipayPublicKey     string
	AlipayReturnURL     string
	AlipayProduction    bool
	WechatAppID         string
	WechatMchID         string
	WechatMchSerialNo   string
	WechatMchPrivateKey string
	WechatAPIv3Key      string

	// 内容审核（第五期）
	ModerationEnabled          bool
	ModerationProvider         string // fake | nsfwjs+detoxify
	ModerationNSFWJSEndpoint   string
	ModerationDetoxifyEndpoint string
	ModerationImageThreshold   float64
	ModerationTextThreshold    float64
	ModerationVideoSampleFPS   float64
	ModerationFailMode         string // reject | allow，无默认值
	ModerationTimeout          time.Duration
	ModerationPolicyVersion    string
	ModerationQuarantineTTL    time.Duration
	ModerationCacheTTL         time.Duration
	// ModerationFakeRejectTexts 仅在 MODERATION_PROVIDER=fake 时用于触发拒绝的文本片段，逗号分隔。
	ModerationFakeRejectTexts string

	// AI 转发与并发（第四期）
	AIImageTimeout         time.Duration
	AIAudioTimeout         time.Duration
	AIStreamTimeout        time.Duration
	AIStreamIdleTimeout    time.Duration
	AIVideoTaskTimeout     time.Duration
	AIAllowPrivateUpstream bool

	// 媒体存储（第二期）
	StorageDriver string // local | s3
	MediaRoot     string // local 驱动的落盘根目录

	S3Endpoint        string
	S3Region          string
	S3Bucket          string
	S3AccessKeyID     string
	S3SecretAccessKey string
	S3ForcePathStyle  bool
	S3PresignAlign    time.Duration
	S3PresignTTL      time.Duration
}

func Load() (*Config, error) {
	c := &Config{
		Port:                       getenv("PORT", "8080"),
		DatabaseURL:                os.Getenv("DATABASE_URL"),
		JWTSecret:                  os.Getenv("JWT_SECRET"),
		CredentialKey:              os.Getenv("CREDENTIAL_MASTER_KEY"),
		AppBaseURL:                 os.Getenv("APP_BASE_URL"),
		AdminEmail:                 os.Getenv("ADMIN_EMAIL"),
		SeedTestData:               getenvBool("SEED_TEST_DATA", false),
		SeedTestDataEmail:          getenv("SEED_TEST_DATA_EMAIL", "test@example.com"),
		SeedTestDataPassword:       getenv("SEED_TEST_DATA_PASSWORD", "test123456"),
		AdminPassword:              os.Getenv("ADMIN_PASSWORD"),
		CookieSecure:               getenvBool("COOKIE_SECURE", true),
		LogLevel:                   getenv("LOG_LEVEL", "info"),
		MailDriver:                 getenv("MAIL_DRIVER", "smtp"),
		SMTPHost:                   os.Getenv("SMTP_HOST"),
		SMTPPort:                   os.Getenv("SMTP_PORT"),
		SMTPUsername:               os.Getenv("SMTP_USERNAME"),
		SMTPPassword:               os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:                   os.Getenv("SMTP_FROM"),
		SMTPSecurity:               getenv("SMTP_SECURITY", "ssl"),
		RegistrationEnabled:        getenvBool("REGISTRATION_ENABLED", true),
		FreeGrantEnabled:           getenvBool("FREE_GRANT_ENABLED", true),
		FreeGrantCampaignID:        os.Getenv("FREE_GRANT_CAMPAIGN_ID"),
		FreeGrantDailyBudgetMicros: getenvInt64("FREE_GRANT_DAILY_BUDGET_MICROS", 0),
		FreeGrantRiskThreshold:     getenvInt("FREE_GRANT_RISK_THRESHOLD", 0),
		EntitlementDays:            getenvInt("ENTITLEMENT_DAYS", 30),
		PromotionEnabled:           getenvBool("PRICING_PROMOTION_ENABLED", true),
		PaymentNotifyURL:           os.Getenv("PAYMENT_NOTIFY_URL"),
		AlipayAppID:                os.Getenv("ALIPAY_APP_ID"),
		AlipayPrivateKey:           os.Getenv("ALIPAY_PRIVATE_KEY"),
		AlipayPublicKey:            os.Getenv("ALIPAY_PUBLIC_KEY"),
		AlipayReturnURL:            os.Getenv("ALIPAY_RETURN_URL"),
		AlipayProduction:           getenvBool("ALIPAY_PRODUCTION", false),
		WechatAppID:                os.Getenv("WECHATPAY_APP_ID"),
		WechatMchID:                os.Getenv("WECHATPAY_MCH_ID"),
		WechatMchSerialNo:          os.Getenv("WECHATPAY_MCH_SERIAL_NO"),
		WechatMchPrivateKey:        os.Getenv("WECHATPAY_MCH_PRIVATE_KEY"),
		WechatAPIv3Key:             os.Getenv("WECHATPAY_API_V3_KEY"),
		ModerationEnabled:          getenvBool("MODERATION_ENABLED", false),
		ModerationProvider:         getenv("MODERATION_PROVIDER", "fake"),
		ModerationNSFWJSEndpoint:   os.Getenv("MODERATION_NSFWJS_ENDPOINT"),
		ModerationDetoxifyEndpoint: os.Getenv("MODERATION_DETOXIFY_ENDPOINT"),
		ModerationImageThreshold:   getenvFloat("MODERATION_IMAGE_THRESHOLD", 0.6),
		ModerationTextThreshold:    getenvFloat("MODERATION_TEXT_THRESHOLD", 0.8),
		ModerationVideoSampleFPS:   getenvFloat("MODERATION_VIDEO_SAMPLE_FPS", 1),
		ModerationFailMode:         os.Getenv("MODERATION_FAIL_MODE"),
		ModerationTimeout:          getenvDurationOr("MODERATION_TIMEOUT", 5*time.Second),
		ModerationPolicyVersion:    getenv("MODERATION_POLICY_VERSION", "v1"),
		ModerationQuarantineTTL:    getenvDurationOr("MODERATION_QUARANTINE_TTL", 24*time.Hour),
		ModerationCacheTTL:         getenvDurationOr("MODERATION_CACHE_TTL", 10*time.Minute),
		ModerationFakeRejectTexts:  os.Getenv("MODERATION_FAKE_REJECT_TEXTS"),
		AIImageTimeout:             getenvDurationOr("AI_IMAGE_TIMEOUT", 180*time.Second),
		AIAudioTimeout:             getenvDurationOr("AI_AUDIO_TIMEOUT", 120*time.Second),
		AIStreamTimeout:            getenvDurationOr("AI_STREAM_TIMEOUT", 600*time.Second),
		AIStreamIdleTimeout:        getenvDurationOr("AI_STREAM_IDLE_TIMEOUT", 60*time.Second),
		AIVideoTaskTimeout:         getenvDurationOr("AI_VIDEO_TASK_TIMEOUT", 20*time.Minute),
		AIAllowPrivateUpstream:     getenvBool("AI_ALLOW_PRIVATE_UPSTREAM", false),
		StorageDriver:              getenv("STORAGE_DRIVER", "local"),
		MediaRoot:                  getenv("MEDIA_ROOT", "/data/media"),
		S3Endpoint:                 os.Getenv("S3_ENDPOINT"),
		S3Region:                   os.Getenv("S3_REGION"),
		S3Bucket:                   os.Getenv("S3_BUCKET"),
		S3AccessKeyID:              os.Getenv("S3_ACCESS_KEY_ID"),
		S3SecretAccessKey:          os.Getenv("S3_SECRET_ACCESS_KEY"),
		S3ForcePathStyle:           getenvBool("S3_FORCE_PATH_STYLE", false),
	}

	align, err := getenvDuration("S3_PRESIGN_ALIGN", time.Hour)
	if err != nil {
		return nil, err
	}
	c.S3PresignAlign = align
	ttl, err := getenvDuration("S3_PRESIGN_TTL", 2*time.Hour)
	if err != nil {
		return nil, err
	}
	c.S3PresignTTL = ttl

	trustedProxies, err := parseTrustedProxies(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		return nil, err
	}
	c.TrustedProxies = trustedProxies

	corsOrigins, err := parseCORSAllowedOrigins(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if err != nil {
		return nil, err
	}
	c.CORSAllowedOrigins = corsOrigins

	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// parseTrustedProxies 解析逗号分隔的 CIDR 列表，空值表示不信任任何代理头。
func parseTrustedProxies(raw string) ([]string, error) {
	var proxies []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(part); err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXIES 含非法 CIDR: %s", part)
		}
		proxies = append(proxies, part)
	}
	return proxies, nil
}

// parseCORSAllowedOrigins 解析逗号分隔的跨源来源白名单，空值表示不启用 CORS。
// 非法来源直接报错：CORS 配错只会由浏览器在运行期表现成随机失败，留到启动时暴露。
func parseCORSAllowedOrigins(raw string) ([]string, error) {
	var origins []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "*") {
			return nil, fmt.Errorf("CORS_ALLOWED_ORIGINS 不支持通配符: %s", part)
		}
		u, err := url.Parse(part)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("CORS_ALLOWED_ORIGINS 含非法来源: %s（必须形如 https://sim-admin.youc.online）", part)
		}
		// 来源必须是不带 path/query/锚点/用户信息、且无末尾点的 scheme://host[:port]。
		if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return nil, fmt.Errorf("CORS_ALLOWED_ORIGINS 含非法来源: %s（不能带路径、查询、锚点或用户信息）", part)
		}
		if strings.HasSuffix(u.Hostname(), ".") {
			return nil, fmt.Errorf("CORS_ALLOWED_ORIGINS 含非法来源: %s（主机名不能以点结尾）", part)
		}
		origins = append(origins, part)
	}
	return origins, nil
}

func (c *Config) validate() error {
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL 未配置")
	}
	if len(c.JWTSecret) < 32 {
		return errors.New("JWT_SECRET 未配置或长度不足 32 字符")
	}
	if len(c.CredentialKey) != 32 {
		return errors.New("CREDENTIAL_MASTER_KEY 必须为 32 字节（第四期渠道密钥复用，本期一并校验）")
	}
	if c.AppBaseURL == "" {
		return errors.New("APP_BASE_URL 未配置（用于拼接邮件里的验证与重置链接）")
	}
	if c.LogLevel != "debug" && c.LogLevel != "info" && c.LogLevel != "warn" && c.LogLevel != "error" {
		return fmt.Errorf("LOG_LEVEL 取值非法: %s", c.LogLevel)
	}
	if c.MailDriver != "smtp" && c.MailDriver != "log" {
		return fmt.Errorf("MAIL_DRIVER 取值非法: %s（只能是 smtp 或 log）", c.MailDriver)
	}
	if c.MailDriver == "smtp" {
		if c.SMTPHost == "" || c.SMTPPort == "" || c.SMTPUsername == "" || c.SMTPPassword == "" || c.SMTPFrom == "" {
			return errors.New("MAIL_DRIVER=smtp 时 SMTP_HOST/SMTP_PORT/SMTP_USERNAME/SMTP_PASSWORD/SMTP_FROM 不能为空")
		}
		if c.SMTPSecurity != "ssl" && c.SMTPSecurity != "starttls" {
			return fmt.Errorf("SMTP_SECURITY 取值非法: %s（只能是 ssl 或 starttls）", c.SMTPSecurity)
		}
	}
	if c.AdminEmail == "" || c.AdminPassword == "" {
		return errors.New("ADMIN_EMAIL 与 ADMIN_PASSWORD 必须配置")
	}
	// 支付回调地址默认由 APP_BASE_URL 推导，可显式覆盖以指向独立域名。
	if c.PaymentNotifyURL == "" {
		c.PaymentNotifyURL = strings.TrimRight(c.AppBaseURL, "/") + "/api/payments/webhook"
	}
	// 零值视同未配置，回落到默认 30 天，兼容直接构造 Config 的调用方。
	if c.EntitlementDays <= 0 {
		c.EntitlementDays = 30
	}
	if c.FreeGrantEnabled {
		if c.FreeGrantCampaignID == "" {
			return errors.New("FREE_GRANT_ENABLED=true 时 FREE_GRANT_CAMPAIGN_ID 必填（无默认值，需运营提供）")
		}
		if c.FreeGrantRiskThreshold <= 0 {
			return errors.New("FREE_GRANT_ENABLED=true 时 FREE_GRANT_RISK_THRESHOLD 必填且必须大于 0（无默认值，需运营提供）")
		}
		if c.FreeGrantDailyBudgetMicros <= 0 {
			return errors.New("FREE_GRANT_ENABLED=true 时 FREE_GRANT_DAILY_BUDGET_MICROS 必填且必须大于 0（无默认值，需运营提供）")
		}
	}
	if err := c.validateStorage(); err != nil {
		return err
	}
	if err := c.validateModeration(); err != nil {
		return err
	}
	return nil
}

// validateModeration 校验审核配置。fail mode 必须显式配置：
// 放行还是拒绝是业务与合规的取舍，不该由框架替部署方决定。
func (c *Config) validateModeration() error {
	if !c.ModerationEnabled {
		return nil
	}
	if c.ModerationFailMode != "reject" && c.ModerationFailMode != "allow" {
		return errors.New("MODERATION_ENABLED=true 时 MODERATION_FAIL_MODE 必须显式配置为 reject 或 allow")
	}
	if c.ModerationProvider != "fake" && c.ModerationProvider != "nsfwjs+detoxify" {
		return fmt.Errorf("MODERATION_PROVIDER 取值非法: %s（只能是 fake 或 nsfwjs+detoxify）", c.ModerationProvider)
	}
	if c.ModerationProvider == "nsfwjs+detoxify" {
		if c.ModerationNSFWJSEndpoint == "" || c.ModerationDetoxifyEndpoint == "" {
			return errors.New("MODERATION_PROVIDER=nsfwjs+detoxify 时 MODERATION_NSFWJS_ENDPOINT 与 MODERATION_DETOXIFY_ENDPOINT 不能为空")
		}
	}
	if c.ModerationImageThreshold <= 0 || c.ModerationImageThreshold > 1 {
		return errors.New("MODERATION_IMAGE_THRESHOLD 必须在 (0,1] 之间")
	}
	if c.ModerationTextThreshold <= 0 || c.ModerationTextThreshold > 1 {
		return errors.New("MODERATION_TEXT_THRESHOLD 必须在 (0,1] 之间")
	}
	if c.ModerationTimeout <= 0 {
		return errors.New("MODERATION_TIMEOUT 必须大于 0")
	}
	if c.ModerationQuarantineTTL <= 0 {
		return errors.New("MODERATION_QUARANTINE_TTL 必须大于 0")
	}
	return nil
}

// validateStorage 校验媒体存储配置。空 StorageDriver 视同 local，
// 兼容第一期直接构造 Config 的调用方与旧部署。
func (c *Config) validateStorage() error {
	driver := c.StorageDriver
	if driver == "" {
		driver = "local"
	}
	switch driver {
	case "local":
		return nil
	case "s3":
		if c.S3Endpoint == "" || c.S3Region == "" || c.S3Bucket == "" ||
			c.S3AccessKeyID == "" || c.S3SecretAccessKey == "" {
			return errors.New("STORAGE_DRIVER=s3 时 S3_ENDPOINT/S3_REGION/S3_BUCKET/S3_ACCESS_KEY_ID/S3_SECRET_ACCESS_KEY 不能为空")
		}
		u, err := url.Parse(c.S3Endpoint)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("S3_ENDPOINT 必须是带协议与主机的合法 URL: %s", c.S3Endpoint)
		}
		if c.S3PresignAlign <= 0 || c.S3PresignTTL <= 0 {
			return errors.New("S3_PRESIGN_ALIGN 与 S3_PRESIGN_TTL 必须大于 0")
		}
		if c.S3PresignTTL != 2*c.S3PresignAlign {
			return fmt.Errorf("S3_PRESIGN_TTL(%s) 必须等于 2 × S3_PRESIGN_ALIGN(%s)", c.S3PresignTTL, c.S3PresignAlign)
		}
		return nil
	default:
		return fmt.Errorf("STORAGE_DRIVER 取值非法: %s（只能是 local 或 s3）", c.StorageDriver)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return strings.EqualFold(v, "true") || v == "1"
}

func getenvFloat(key string, fallback float64) float64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n := 0
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return fallback
	}
	return n
}

func getenvInt64(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n := int64(0)
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return fallback
	}
	return n
}

// getenvDurationOr 读取时长配置，非法时回落到默认值（AI 超时允许用默认值兜底）。
func getenvDurationOr(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func getenvDuration(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s 取值非法（示例 1h、2h）: %s", key, v)
	}
	return d, nil
}
