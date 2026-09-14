package config

import (
	"strings"
	"testing"
	"time"
)

func storageBase() *Config {
	return &Config{
		DatabaseURL:                "dsn",
		JWTSecret:                  strings.Repeat("s", 32),
		CredentialKey:              strings.Repeat("k", 32),
		AppBaseURL:                 "http://localhost:3000",
		LogLevel:                   "info",
		MailDriver:                 "log",
		AdminEmail:                 "admin@example.com",
		AdminPassword:              "password",
		FreeGrantEnabled:           true,
		FreeGrantCampaignID:        "signup",
		FreeGrantDailyBudgetMicros: 1,
		FreeGrantRiskThreshold:     1,
	}
}

func TestStorageConfigValidation(t *testing.T) {
	validS3 := func() *Config {
		c := storageBase()
		c.StorageDriver = "s3"
		c.S3Endpoint = "https://s3.example.com"
		c.S3Region = "auto"
		c.S3Bucket = "media"
		c.S3AccessKeyID = "key"
		c.S3SecretAccessKey = "secret"
		c.S3PresignAlign = time.Hour
		c.S3PresignTTL = 2 * time.Hour
		return c
	}

	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"s3 配置完整", func(c *Config) {}, false},
		{"local 驱动只需默认值", func(c *Config) { c.StorageDriver = "local" }, false},
		{"空驱动视同 local", func(c *Config) { c.StorageDriver = "" }, false},
		{"未知驱动", func(c *Config) { c.StorageDriver = "oss" }, true},
		{"s3 缺少桶", func(c *Config) { c.S3Bucket = "" }, true},
		{"s3 缺少密钥", func(c *Config) { c.S3SecretAccessKey = "" }, true},
		{"s3 端点非法", func(c *Config) { c.S3Endpoint = "not-a-url" }, true},
		{"ttl 不是 align 的两倍", func(c *Config) { c.S3PresignTTL = 90 * time.Minute }, true},
		{"align 为 0", func(c *Config) { c.S3PresignAlign = 0; c.S3PresignTTL = 0 }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validS3()
			tc.mutate(cfg)
			err := cfg.validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("validate() err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestFreeGrantRequiredWhenEnabled(t *testing.T) {
	base := func() *Config {
		return &Config{
			DatabaseURL:                "dsn",
			JWTSecret:                  strings.Repeat("s", 32),
			CredentialKey:              strings.Repeat("k", 32),
			AppBaseURL:                 "http://localhost:3000",
			LogLevel:                   "info",
			MailDriver:                 "log",
			AdminEmail:                 "admin@example.com",
			AdminPassword:              "password",
			FreeGrantEnabled:           true,
			FreeGrantCampaignID:        "signup",
			FreeGrantDailyBudgetMicros: 1,
			FreeGrantRiskThreshold:     1,
		}
	}
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"启用时配置完整", func(c *Config) {}, false},
		{"缺少活动 ID", func(c *Config) { c.FreeGrantCampaignID = "" }, true},
		{"风险阈值为 0", func(c *Config) { c.FreeGrantRiskThreshold = 0 }, true},
		{"每日预算为 0", func(c *Config) { c.FreeGrantDailyBudgetMicros = 0 }, true},
		{"关闭赠送时可不配置", func(c *Config) {
			c.FreeGrantEnabled = false
			c.FreeGrantCampaignID = ""
			c.FreeGrantDailyBudgetMicros = 0
			c.FreeGrantRiskThreshold = 0
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.mutate(cfg)
			err := cfg.validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("validate() err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestParseTrustedProxies(t *testing.T) {
	if got, err := parseTrustedProxies(""); err != nil || got != nil {
		t.Fatalf("空值应返回 nil, got=%v err=%v", got, err)
	}
	got, err := parseTrustedProxies(" 10.0.0.0/8 , 172.16.0.0/12 ")
	if err != nil {
		t.Fatalf("合法 CIDR 不应报错: %v", err)
	}
	if len(got) != 2 || got[0] != "10.0.0.0/8" || got[1] != "172.16.0.0/12" {
		t.Fatalf("解析结果不符: %v", got)
	}
	if _, err := parseTrustedProxies("not-a-cidr"); err == nil {
		t.Fatal("非法 CIDR 应报错")
	}
}
