package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"golang.org/x/sync/singleflight"
)

// GuestTrialConfig 未注册访客的网页版试用配置（JSON 存在 settings 表单个键里）。
//
// 试用流量一律以管理员指定的 API 密钥身份走现有网关，用量和费用都记在这把密钥上；
// 只开放文字聊天：模型走白名单，请求体里的图片、文件、工具调用在入口就被拒绝。
type GuestTrialConfig struct {
	Enabled bool `json:"enabled"`
	// APIKeyID 承担试用流量的密钥；其分组决定路由到哪个上游。
	APIKeyID int64 `json:"api_key_id"`
	// Models 允许访客选择的模型，第一个是默认模型。
	Models []string `json:"models"`
	// DailyPerVisitor 每个访客（浏览器设备）每天可发的消息数；同一 IP 的上限是它的 IPMultiplier 倍，容忍 NAT 共享出口。
	DailyPerVisitor int `json:"daily_per_visitor"`
	// DailyGlobal 全站每天的试用请求总数上限，防止批量换 IP 刷爆。
	DailyGlobal int `json:"daily_global"`
	// MaxInputChars 单次请求所有消息文本的总字符数上限。
	MaxInputChars int `json:"max_input_chars"`
	// MaxOutputTokens 单次回复的输出 token 上限（强制写入请求体）。
	MaxOutputTokens int `json:"max_output_tokens"`
	// RequireCaptcha 首条消息前做一次人机验证（站点启用了验证码服务商时才生效）。
	RequireCaptcha bool `json:"require_captcha"`
}

const (
	GuestTrialDefaultDailyPerVisitor = 20
	GuestTrialDefaultDailyGlobal     = 1000
	GuestTrialDefaultMaxInputChars   = 6000
	GuestTrialDefaultMaxOutputTokens = 1024
	// GuestTrialIPMultiplier 同一 IP 的每日额度 = 每访客额度 × 该倍数。
	GuestTrialIPMultiplier = 3

	guestTrialMaxModels          = 20
	guestTrialMaxDailyPerVisitor = 1000
	guestTrialMaxDailyGlobal     = 1000000
	guestTrialMaxInputChars      = 100000
	guestTrialMaxOutputTokens    = 32000
)

// DefaultGuestTrialConfig 首次启用时的默认值（与产品确认的默认额度一致）。
func DefaultGuestTrialConfig() *GuestTrialConfig {
	return &GuestTrialConfig{
		Models:          []string{},
		DailyPerVisitor: GuestTrialDefaultDailyPerVisitor,
		DailyGlobal:     GuestTrialDefaultDailyGlobal,
		MaxInputChars:   GuestTrialDefaultMaxInputChars,
		MaxOutputTokens: GuestTrialDefaultMaxOutputTokens,
		RequireCaptcha:  true,
	}
}

// DefaultModel 返回默认试用模型；未配置时为空串。
func (c *GuestTrialConfig) DefaultModel() string {
	if c == nil || len(c.Models) == 0 {
		return ""
	}
	return c.Models[0]
}

// AllowsModel 判断模型是否在试用白名单内（大小写不敏感）。
func (c *GuestTrialConfig) AllowsModel(model string) bool {
	if c == nil {
		return false
	}
	model = strings.TrimSpace(model)
	for _, allowed := range c.Models {
		if strings.EqualFold(allowed, model) {
			return true
		}
	}
	return false
}

// Usable 配置是否足以对外开放：开关打开、指定了密钥、至少一个模型。
func (c *GuestTrialConfig) Usable() bool {
	return c != nil && c.Enabled && c.APIKeyID > 0 && len(c.Models) > 0
}

// normalizeGuestTrialConfig 去重、去空白，把越界数字拉回合理区间；缺省数字回落到默认值。
func normalizeGuestTrialConfig(cfg *GuestTrialConfig) *GuestTrialConfig {
	out := DefaultGuestTrialConfig()
	if cfg == nil {
		return out
	}
	out.Enabled = cfg.Enabled
	out.APIKeyID = cfg.APIKeyID
	out.RequireCaptcha = cfg.RequireCaptcha
	seen := make(map[string]bool, len(cfg.Models))
	for _, model := range cfg.Models {
		model = strings.TrimSpace(model)
		key := strings.ToLower(model)
		if model == "" || seen[key] || len(out.Models) >= guestTrialMaxModels {
			continue
		}
		seen[key] = true
		out.Models = append(out.Models, model)
	}
	out.DailyPerVisitor = clampGuestTrialInt(cfg.DailyPerVisitor, GuestTrialDefaultDailyPerVisitor, guestTrialMaxDailyPerVisitor)
	out.DailyGlobal = clampGuestTrialInt(cfg.DailyGlobal, GuestTrialDefaultDailyGlobal, guestTrialMaxDailyGlobal)
	out.MaxInputChars = clampGuestTrialInt(cfg.MaxInputChars, GuestTrialDefaultMaxInputChars, guestTrialMaxInputChars)
	out.MaxOutputTokens = clampGuestTrialInt(cfg.MaxOutputTokens, GuestTrialDefaultMaxOutputTokens, guestTrialMaxOutputTokens)
	return out
}

func clampGuestTrialInt(value, fallback, maximum int) int {
	if value <= 0 {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

func validateGuestTrialConfig(cfg *GuestTrialConfig) error {
	if cfg == nil || !cfg.Enabled {
		return nil
	}
	if cfg.APIKeyID <= 0 {
		return errors.New("启用试用前需要指定一把承担试用流量的 API 密钥")
	}
	if len(cfg.Models) == 0 {
		return errors.New("启用试用前需要至少配置一个试用模型")
	}
	return nil
}

// --- 进程内缓存（与 web search 配置同一套写法）---

const sfKeyGuestTrialConfig = "guest_trial_config"

type cachedGuestTrialConfig struct {
	config    *GuestTrialConfig
	expiresAt int64
}

var guestTrialConfigCache atomic.Value // *cachedGuestTrialConfig
var guestTrialConfigSF singleflight.Group

const (
	guestTrialConfigCacheTTL  = 30 * time.Second
	guestTrialConfigErrorTTL  = 5 * time.Second
	guestTrialConfigDBTimeout = 5 * time.Second
)

// GetGuestTrialConfig 读取试用配置（带 30s 进程内缓存）；读取失败时返回关闭态配置，保证不会误开放。
func (s *SettingService) GetGuestTrialConfig(ctx context.Context) (*GuestTrialConfig, error) {
	if cached := guestTrialConfigCache.Load(); cached != nil {
		if c, ok := cached.(*cachedGuestTrialConfig); ok && time.Now().UnixNano() < c.expiresAt {
			return c.config, nil
		}
	}
	result, err, _ := guestTrialConfigSF.Do(sfKeyGuestTrialConfig, func() (any, error) {
		return s.loadGuestTrialConfigFromDB()
	})
	if err != nil {
		return DefaultGuestTrialConfig(), err
	}
	if cfg, ok := result.(*GuestTrialConfig); ok {
		return cfg, nil
	}
	return DefaultGuestTrialConfig(), nil
}

func (s *SettingService) loadGuestTrialConfigFromDB() (*GuestTrialConfig, error) {
	dbCtx, cancel := context.WithTimeout(context.Background(), guestTrialConfigDBTimeout)
	defer cancel()

	raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyGuestTrialConfig)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			cfg := DefaultGuestTrialConfig()
			storeGuestTrialConfigCache(cfg, guestTrialConfigCacheTTL)
			return cfg, nil
		}
		storeGuestTrialConfigCache(DefaultGuestTrialConfig(), guestTrialConfigErrorTTL)
		return DefaultGuestTrialConfig(), err
	}
	cfg := parseGuestTrialConfigJSON(raw)
	storeGuestTrialConfigCache(cfg, guestTrialConfigCacheTTL)
	return cfg, nil
}

func storeGuestTrialConfigCache(cfg *GuestTrialConfig, ttl time.Duration) {
	guestTrialConfigCache.Store(&cachedGuestTrialConfig{config: cfg, expiresAt: time.Now().Add(ttl).UnixNano()})
}

func parseGuestTrialConfigJSON(raw string) *GuestTrialConfig {
	if strings.TrimSpace(raw) == "" {
		return DefaultGuestTrialConfig()
	}
	var cfg GuestTrialConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		slog.Warn("guest_trial: failed to parse config JSON", "error", err)
		return DefaultGuestTrialConfig()
	}
	return normalizeGuestTrialConfig(&cfg)
}

// SaveGuestTrialConfig 规范化、校验并落库，随后立即刷新缓存。
func (s *SettingService) SaveGuestTrialConfig(ctx context.Context, cfg *GuestTrialConfig) (*GuestTrialConfig, error) {
	normalized := normalizeGuestTrialConfig(cfg)
	if err := validateGuestTrialConfig(normalized); err != nil {
		return nil, infraerrors.BadRequest("INVALID_GUEST_TRIAL_CONFIG", err.Error())
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("guest_trial: marshal config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyGuestTrialConfig, string(data)); err != nil {
		return nil, fmt.Errorf("guest_trial: save config: %w", err)
	}
	guestTrialConfigSF.Forget(sfKeyGuestTrialConfig)
	storeGuestTrialConfigCache(normalized, guestTrialConfigCacheTTL)
	return normalized, nil
}

