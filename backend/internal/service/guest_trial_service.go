package service

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrGuestTrialDisabled        = infraerrors.NotFound("GUEST_TRIAL_DISABLED", "网页版试用暂未开放")
	ErrGuestTrialInvalidDevice   = infraerrors.BadRequest("GUEST_TRIAL_INVALID_DEVICE", "试用设备标识无效，请刷新页面重试")
	ErrGuestTrialCaptchaRequired = infraerrors.Forbidden("GUEST_TRIAL_CAPTCHA_REQUIRED", "请先完成人机验证")
	ErrGuestTrialVisitorLimit    = infraerrors.TooManyRequests("GUEST_TRIAL_QUOTA_EXHAUSTED", "今天的试用次数已用完，注册后即可继续使用")
	ErrGuestTrialGlobalLimit     = infraerrors.TooManyRequests("GUEST_TRIAL_BUSY", "今天的试用名额已满，注册后即可继续使用")
	ErrGuestTrialUnavailable     = infraerrors.ServiceUnavailable("GUEST_TRIAL_UNAVAILABLE", "试用服务暂时不可用，请稍后再试")
)

// GuestTrialQuotaKeys 一次试用请求的计数维度：按「试用日 + 设备 / IP / 全站」三层计数。
type GuestTrialQuotaKeys struct {
	Day    string
	Device string
	IP     string
}

// GuestTrialQuotaLimits 三层计数各自的上限。
type GuestTrialQuotaLimits struct {
	PerVisitor int
	PerIP      int
	Global     int
}

// GuestTrialQuotaDenyReason 扣减失败时是哪一层先满。
type GuestTrialQuotaDenyReason int

const (
	GuestTrialQuotaAllowed GuestTrialQuotaDenyReason = iota
	GuestTrialQuotaVisitorFull
	GuestTrialQuotaIPFull
	GuestTrialQuotaGlobalFull
)

// GuestTrialQuota 试用额度与验证状态的存储（Redis 实现见 repository）。
// 实现必须保证 Consume 的「检查全部上限 + 一起加一」是原子的。
type GuestTrialQuota interface {
	Consume(ctx context.Context, keys GuestTrialQuotaKeys, limits GuestTrialQuotaLimits) (GuestTrialQuotaDenyReason, int, error)
	VisitorUsed(ctx context.Context, keys GuestTrialQuotaKeys) (int, error)
	MarkVerified(ctx context.Context, subject string, ttl time.Duration) error
	IsVerified(ctx context.Context, subject string) (bool, error)
}

// GuestTrialCaptchaVerifier 复用站点已启用的验证码服务商（Turnstile / 腾讯 / 阿里云）。
type GuestTrialCaptchaVerifier interface {
	VerifyCaptcha(ctx context.Context, proof CaptchaProof, remoteIP string) error
}

// GuestTrialCaptchaStatus 判断站点当前是否启用了任一验证码服务商。
type GuestTrialCaptchaStatus interface {
	GetCaptchaProviderConfig(ctx context.Context) (CaptchaProviderConfig, error)
}

// GuestTrialState 公开给试用页的状态（不含密钥等敏感信息）。
type GuestTrialState struct {
	Enabled         bool     `json:"enabled"`
	Models          []string `json:"models"`
	DefaultModel    string   `json:"default_model"`
	DailyLimit      int      `json:"daily_limit"`
	Remaining       int      `json:"remaining"`
	MaxInputChars   int      `json:"max_input_chars"`
	CaptchaRequired bool     `json:"captcha_required"`
}

// GuestTrialService 串起试用的全部校验：开关 → 人机验证 → 请求体白名单 → 三层额度。
type GuestTrialService struct {
	settings *SettingService
	quota    GuestTrialQuota
	captcha  GuestTrialCaptchaVerifier
	status   GuestTrialCaptchaStatus
	now      func() time.Time
	loadCfg  func(ctx context.Context) (*GuestTrialConfig, error)
	// backendMode 后端模式下站点不对外开放公开页面，试用也一并关闭。
	backendMode func(ctx context.Context) bool
}

// guestTrialVerifiedTTL 一次人机验证的有效期：同一设备 + IP 当天内不再重复验证。
const guestTrialVerifiedTTL = 24 * time.Hour

// guestTrialDayZone 试用日按北京时间零点切换，和用户感知的「每天」一致。
var guestTrialDayZone = time.FixedZone("UTC+8", 8*3600)

var guestTrialDevicePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

func NewGuestTrialService(settings *SettingService, quota GuestTrialQuota, captcha GuestTrialCaptchaVerifier) *GuestTrialService {
	svc := &GuestTrialService{settings: settings, quota: quota, captcha: captcha, now: time.Now}
	if settings != nil {
		svc.status = settings
		svc.loadCfg = settings.GetGuestTrialConfig
		svc.backendMode = settings.IsBackendModeEnabled
	}
	return svc
}

func (s *GuestTrialService) config(ctx context.Context) (*GuestTrialConfig, error) {
	if s == nil || s.loadCfg == nil {
		return DefaultGuestTrialConfig(), nil
	}
	cfg, err := s.loadCfg(ctx)
	if cfg == nil {
		cfg = DefaultGuestTrialConfig()
	}
	if s.backendMode != nil && s.backendMode(ctx) {
		closed := *cfg
		closed.Enabled = false
		return &closed, err
	}
	return cfg, err
}

// ValidDevice 设备标识由前端随机生成并存在本地；格式不对直接拒绝，避免拿任意字符串刷新计数。
func ValidGuestTrialDevice(device string) bool {
	return guestTrialDevicePattern.MatchString(device)
}

func (s *GuestTrialService) keys(device, ip string) GuestTrialQuotaKeys {
	return GuestTrialQuotaKeys{Day: s.now().In(guestTrialDayZone).Format("20060102"), Device: device, IP: strings.TrimSpace(ip)}
}

func guestTrialVerifySubject(device, ip string) string {
	return device + "|" + strings.TrimSpace(ip)
}

func limitsOf(cfg *GuestTrialConfig) GuestTrialQuotaLimits {
	return GuestTrialQuotaLimits{
		PerVisitor: cfg.DailyPerVisitor,
		PerIP:      cfg.DailyPerVisitor * GuestTrialIPMultiplier,
		Global:     cfg.DailyGlobal,
	}
}

// captchaRequired 配置要求验证且站点确实启用了验证码服务商时才需要验证。
func (s *GuestTrialService) captchaRequired(ctx context.Context, cfg *GuestTrialConfig) bool {
	if !cfg.RequireCaptcha || s.status == nil {
		return false
	}
	provider, err := s.status.GetCaptchaProviderConfig(ctx)
	if err != nil {
		// 读不到验证码配置时按「需要验证」处理，宁可多一步也不放开。
		return true
	}
	return provider.TurnstileEnabled || provider.Tencent.Enabled || provider.Aliyun.Enabled
}

// State 返回试用页需要的公开状态；未开放时只返回 enabled=false。
func (s *GuestTrialService) State(ctx context.Context, device, ip string) (*GuestTrialState, error) {
	cfg, _ := s.config(ctx)
	if !cfg.Usable() {
		return &GuestTrialState{Enabled: false, Models: []string{}}, nil
	}
	state := &GuestTrialState{
		Enabled:       true,
		Models:        append([]string(nil), cfg.Models...),
		DefaultModel:  cfg.DefaultModel(),
		DailyLimit:    cfg.DailyPerVisitor,
		Remaining:     cfg.DailyPerVisitor,
		MaxInputChars: cfg.MaxInputChars,
	}
	if !ValidGuestTrialDevice(device) || s.quota == nil {
		state.CaptchaRequired = s.captchaRequired(ctx, cfg)
		return state, nil
	}
	if used, err := s.quota.VisitorUsed(ctx, s.keys(device, ip)); err == nil {
		state.Remaining = max(cfg.DailyPerVisitor-used, 0)
	}
	if s.captchaRequired(ctx, cfg) {
		verified, err := s.quota.IsVerified(ctx, guestTrialVerifySubject(device, ip))
		state.CaptchaRequired = err != nil || !verified
	}
	return state, nil
}

// Verify 完成一次人机验证，之后 24 小时内同一设备 + IP 不再需要验证。
func (s *GuestTrialService) Verify(ctx context.Context, device, ip string, proof CaptchaProof) error {
	cfg, _ := s.config(ctx)
	if !cfg.Usable() {
		return ErrGuestTrialDisabled
	}
	if !ValidGuestTrialDevice(device) {
		return ErrGuestTrialInvalidDevice
	}
	if s.captcha == nil || s.quota == nil {
		return ErrGuestTrialUnavailable
	}
	if err := s.captcha.VerifyCaptcha(ctx, proof, ip); err != nil {
		return err
	}
	if err := s.quota.MarkVerified(ctx, guestTrialVerifySubject(device, ip), guestTrialVerifiedTTL); err != nil {
		return ErrGuestTrialUnavailable
	}
	return nil
}

// GuestTrialPrepared PrepareChat 的结果：清洗后的请求体和承担流量的密钥。
type GuestTrialPrepared struct {
	Body      []byte
	Model     string
	APIKeyID  int64
	Remaining int
}

// PrepareChat 依次做全部校验并扣减额度；返回的 body 可以直接交给网关聊天处理器。
// 扣减放在请求体校验之后：格式错误、超长等被拒的请求不消耗访客额度。
func (s *GuestTrialService) PrepareChat(ctx context.Context, device, ip string, body []byte) (*GuestTrialPrepared, error) {
	cfg, err := s.config(ctx)
	if err != nil && !cfg.Usable() {
		return nil, ErrGuestTrialUnavailable
	}
	if !cfg.Usable() {
		return nil, ErrGuestTrialDisabled
	}
	if !ValidGuestTrialDevice(device) {
		return nil, ErrGuestTrialInvalidDevice
	}
	if s.quota == nil {
		return nil, ErrGuestTrialUnavailable
	}
	if s.captchaRequired(ctx, cfg) {
		verified, verr := s.quota.IsVerified(ctx, guestTrialVerifySubject(device, ip))
		if verr != nil {
			return nil, ErrGuestTrialUnavailable
		}
		if !verified {
			return nil, ErrGuestTrialCaptchaRequired
		}
	}
	sanitized, model, err := SanitizeGuestTrialChatBody(body, cfg)
	if err != nil {
		return nil, err
	}
	reason, used, err := s.quota.Consume(ctx, s.keys(device, ip), limitsOf(cfg))
	if err != nil {
		// 额度是成本闸门：存储故障时关闭试用，而不是放行。
		return nil, ErrGuestTrialUnavailable
	}
	switch reason {
	case GuestTrialQuotaAllowed:
	case GuestTrialQuotaGlobalFull:
		return nil, ErrGuestTrialGlobalLimit
	default:
		return nil, ErrGuestTrialVisitorLimit
	}
	return &GuestTrialPrepared{
		Body:      sanitized,
		Model:     model,
		APIKeyID:  cfg.APIKeyID,
		Remaining: max(cfg.DailyPerVisitor-used, 0),
	}, nil
}

// IsGuestTrialError 判断错误是否是试用自身的业务错误（用于 handler 选择状态码）。
func IsGuestTrialError(err error) bool {
	for _, known := range []error{
		ErrGuestTrialDisabled, ErrGuestTrialInvalidDevice, ErrGuestTrialCaptchaRequired,
		ErrGuestTrialVisitorLimit, ErrGuestTrialGlobalLimit, ErrGuestTrialUnavailable,
		ErrGuestTrialTextOnly, ErrGuestTrialModelNotAllowed, ErrGuestTrialInputTooLong, ErrGuestTrialInvalidRequest,
	} {
		if errors.Is(err, known) {
			return true
		}
	}
	return false
}
