package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/infinite-canvas/server/internal/model"
)

// 站点设置键。管理后台可改的开关与边界值全部走这里，不重启容器。
const (
	SettingAnnouncement          = "announcement"
	SettingRegistrationEnabled   = "registration_enabled"
	SettingMaintenanceMode       = "maintenance_mode"
	SettingMaintenanceNotice     = "maintenance_notice"
	SettingCommunityEnabled      = "community_enabled"
	SettingCheckinEnabled        = "checkin_enabled"
	SettingCheckinRewardMicros   = "checkin_reward_micros"
	SettingInviteEnabled         = "invite_enabled"
	SettingInviteRewardMicros    = "invite_reward_micros"
	SettingInviteeRewardMicros   = "invitee_reward_micros"
	SettingMaxUploadBytes        = "max_upload_bytes"
	SettingGenerationConcurrency = "generation_concurrency"
)

// SiteSettingService 读写站点设置，并在进程内缓存，避免每个请求都查库。
type SiteSettingService struct {
	db *gorm.DB

	mu    sync.RWMutex
	cache map[string]json.RawMessage
}

func NewSiteSettingService(db *gorm.DB) *SiteSettingService {
	service := &SiteSettingService{db: db, cache: map[string]json.RawMessage{}}
	return service
}

// Load 启动时读全量设置进缓存。
func (s *SiteSettingService) Load(ctx context.Context) error {
	var rows []model.SiteSetting
	if err := s.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range rows {
		if row.Value == "" {
			continue
		}
		s.cache[row.Key] = json.RawMessage(row.Value)
	}
	return nil
}

// Set 写入并刷新缓存。
func (s *SiteSettingService) Set(ctx context.Context, key string, value any, actorID *uuid.UUID) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	row := model.SiteSetting{Key: key, Value: string(raw), UpdatedBy: actorID, UpdatedAt: time.Now()}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_by", "updated_at"}),
	}).Create(&row).Error; err != nil {
		return err
	}
	s.mu.Lock()
	s.cache[key] = raw
	s.mu.Unlock()
	return nil
}

// Bool 读取布尔设置；未配置时返回 fallback。
func (s *SiteSettingService) Bool(key string, fallback bool) bool {
	raw, ok := s.raw(key)
	if !ok {
		return fallback
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return fallback
	}
	return value
}

// Int64 读取整数设置；未配置或非法时返回 fallback。
func (s *SiteSettingService) Int64(key string, fallback int64) int64 {
	raw, ok := s.raw(key)
	if !ok {
		return fallback
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return fallback
	}
	return value
}

// String 读取字符串设置。
func (s *SiteSettingService) String(key, fallback string) string {
	raw, ok := s.raw(key)
	if !ok {
		return fallback
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fallback
	}
	return value
}

// Announcement 返回当前站点公告。
func (s *SiteSettingService) Announcement() string {
	return s.String(SettingAnnouncement, "")
}

// MaintenanceMode 返回维护模式开关与提示文案。
func (s *SiteSettingService) MaintenanceMode() (bool, string) {
	return s.Bool(SettingMaintenanceMode, false), s.String(SettingMaintenanceNotice, "")
}

// CommunityEnabled 社区总开关。
func (s *SiteSettingService) CommunityEnabled() bool { return s.Bool(SettingCommunityEnabled, true) }

// CheckinEnabled 签到开关与单次赠送点数。
func (s *SiteSettingService) CheckinEnabled() bool { return s.Bool(SettingCheckinEnabled, true) }

func (s *SiteSettingService) CheckinRewardMicros() int64 {
	return s.Int64(SettingCheckinRewardMicros, 20000)
}

// InviteEnabled 邀请返利开关与双方赠送点数。
func (s *SiteSettingService) InviteEnabled() bool { return s.Bool(SettingInviteEnabled, true) }

func (s *SiteSettingService) InviteRewardMicros() int64 {
	return s.Int64(SettingInviteRewardMicros, 100000)
}

func (s *SiteSettingService) InviteeRewardMicros() int64 {
	return s.Int64(SettingInviteeRewardMicros, 50000)
}

// GenerationConcurrency 单用户生成并发上限；未配置时用调用方默认值。
func (s *SiteSettingService) GenerationConcurrency() int64 {
	return s.Int64(SettingGenerationConcurrency, 0)
}

func (s *SiteSettingService) raw(key string) (json.RawMessage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.cache[key]
	return value, ok
}

// ErrSettingNotFound 表示设置项不存在。
var ErrSettingNotFound = errors.New("站点设置不存在")

// SettingsPayload 是管理后台与前端共用的设置快照。
type SettingsPayload struct {
	Announcement          string `json:"announcement"`
	RegistrationEnabled   bool   `json:"registrationEnabled"`
	MaintenanceMode       bool   `json:"maintenanceMode"`
	MaintenanceNotice     string `json:"maintenanceNotice"`
	CommunityEnabled      bool   `json:"communityEnabled"`
	CheckinEnabled        bool   `json:"checkinEnabled"`
	CheckinRewardMicros   int64  `json:"checkinRewardMicros"`
	InviteEnabled         bool   `json:"inviteEnabled"`
	InviteRewardMicros    int64  `json:"inviteRewardMicros"`
	InviteeRewardMicros   int64  `json:"inviteeRewardMicros"`
	MaxUploadBytes        int64  `json:"maxUploadBytes"`
	GenerationConcurrency int64  `json:"generationConcurrency"`
}

// Snapshot 返回当前设置快照。
func (s *SiteSettingService) Snapshot() SettingsPayload {
	return SettingsPayload{
		Announcement:          s.Announcement(),
		RegistrationEnabled:   s.Bool(SettingRegistrationEnabled, true),
		MaintenanceMode:       s.Bool(SettingMaintenanceMode, false),
		MaintenanceNotice:     s.String(SettingMaintenanceNotice, ""),
		CommunityEnabled:      s.CommunityEnabled(),
		CheckinEnabled:        s.CheckinEnabled(),
		CheckinRewardMicros:   s.CheckinRewardMicros(),
		InviteEnabled:         s.InviteEnabled(),
		InviteRewardMicros:    s.InviteRewardMicros(),
		InviteeRewardMicros:   s.InviteeRewardMicros(),
		MaxUploadBytes:        s.Int64(SettingMaxUploadBytes, 0),
		GenerationConcurrency: s.GenerationConcurrency(),
	}
}
