package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
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
