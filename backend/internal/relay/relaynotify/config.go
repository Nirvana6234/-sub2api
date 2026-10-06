// Package relaynotify 是主从分流的通知（设计第 13 节）：从节点相关事件接入现有运维告警（后台查看、确认、恢复），
// 另有飞书机器人渠道（签名校验、失败重试、仍失败改发邮件）；同一台一次离线只发一条、反复上下线合并成"不稳定"、
// 多台同时出事合并成一条。飞书 Webhook 地址和签名密钥只存在主节点、加密保存。
package relaynotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// SettingKeyConfig 是通知配置在系统设置里的键（只在主节点，不下发给从节点）。
const SettingKeyConfig = "relay_notification_config"

// 默认合并窗口：事件进来后等这么久，把同时发生的合成一条再发。
const defaultMergeWindowSeconds = 15

// ErrInvalidConfig：配置不合法。
var ErrInvalidConfig = errors.New("invalid relay notification config")

// 渠道。
const (
	ChannelFeishu = "feishu"
	ChannelEmail  = "email"
)

// eventSpec 是一类事件的默认设置（设计 13 的表）。
type eventSpec struct {
	Kind          string
	Title         string
	Severity      master.Severity
	DefaultOn     bool
	DefaultFeishu bool
	DefaultEmail  bool
	// Action 是"系统已采取的动作"一行（可空）。
	Action string
}

// eventSpecs 是设计 13 的事件表。严重和紧急的飞书和邮件都发，警告飞书和邮件都发，提示只发飞书。
var eventSpecs = []eventSpec{
	{master.EventNodeOffline, "从节点离线", master.SeverityCritical, true, true, true, "已停止给它分配新用户和新 Key；锁在它上面的额度等租约到期后收回"},
	{master.EventNodeOnline, "从节点恢复在线", master.SeverityInfo, true, true, false, ""},
	{master.EventNodeUnreachable, "从节点对外不可达", master.SeverityCritical, true, true, true, "已停止分配新用户和新 Key"},
	{master.EventNodeDegraded, "从节点错误率降级", master.SeverityCritical, true, true, true, "已停止分配新用户和新 Key，恢复后自动恢复"},
	{master.EventNodeUnreachableReports, "多名用户报告连不上同一台从节点", master.SeverityCritical, true, true, true, "已暂停给它分配新用户 5 分钟"},
	{master.EventNodeDNSMismatch, "从节点域名解析和预期不符", master.SeverityCritical, true, true, true, ""},
	{master.EventNodeCertFailed, "从节点 HTTPS 证书申请或续期失败", master.SeverityCritical, true, true, true, ""},
	{master.EventNodeCertExpiring, "从节点 HTTPS 证书即将到期", master.SeverityWarning, true, true, true, ""},
	{master.EventAllRelaysDownFallback, "所有从节点不可用，小白端已回退到主节点", master.SeverityEmergency, true, true, true, "新分配和需要换节点的小白端用户临时分给主节点"},
	{master.EventAllRelaysDownOutage, "所有从节点不可用且主节点分配比例为 0，服务中断", master.SeverityEmergency, true, true, true, "小白端分配查询返回暂无可用中转"},
	{master.EventRelayRecovered, "从节点已恢复，回退结束", master.SeverityInfo, true, true, false, "小白端下次询问时切回从节点"},
	{master.EventMasterRatioChanged, "主节点分配比例被修改", master.SeverityInfo, true, true, false, ""},
	{master.EventMasterCapReached, "主节点转发达到上限，开始拒绝请求", master.SeverityCritical, true, true, true, "超过上限的转发请求回服务繁忙"},
	{master.EventIdentityDuplicated, "同一身份出现两份，节点已退回待激活", master.SeverityCritical, true, true, true, "已吊销这台的证书并断开连接"},
	{master.EventCertificateRecovered, "证书过期后用长期密钥恢复", master.SeverityWarning, true, true, true, ""},
	{master.EventNodeRegistered, "有从节点注册，等待激活", master.SeverityInfo, true, true, false, ""},
	{master.EventPendingPurged, "待激活节点超时自动清除", master.SeverityInfo, true, true, false, ""},
	{master.EventUserPinReleased, "固定的用户因节点故障被自动解除固定", master.SeverityInfo, true, true, false, ""},
	{master.EventBillingBacklog, "本地扣费队列积压过多", master.SeverityWarning, true, true, true, ""},
	{master.EventNodeBlocked, "从节点停止接收新请求", master.SeverityCritical, true, true, true, "这台拒绝新请求（503），等待恢复"},
	{master.EventNodeClockSkew, "从节点与主节点时钟偏差超限", master.SeverityCritical, true, true, true, "这台拒绝新请求（503），请校准时间"},
	{master.EventNodeSelectFlood, "从节点选号频率异常", master.SeverityCritical, true, true, true, ""},
	{master.EventNodeUnreleased, "从节点选号后大量不上报释放", master.SeverityCritical, true, true, true, ""},
	{master.EventNodeVoucherShortfall, "从节点扣费记录迟迟没有入账", master.SeverityCritical, true, true, true, "已停止给这台选号和发额度"},
	{master.EventNodeIPChanged, "从节点连接 IP 变化", master.SeverityInfo, true, true, false, ""},
	{master.EventNodeUnstable, "从节点反复上下线（不稳定）", master.SeverityCritical, true, true, true, "10 分钟内不再单独通知它的上下线"},
	{master.EventNodeOverloaded, "从节点压力持续超过阈值", master.SeverityWarning, false, true, false, ""},
	{master.EventNodeStaleVersion, "从节点版本或配置版本落后", master.SeverityInfo, false, true, false, ""},
}

func specFor(kind string) (eventSpec, bool) {
	for _, s := range eventSpecs {
		if s.Kind == kind {
			return s, true
		}
	}
	return eventSpec{}, false
}

// EventSetting 是一类事件的用户设置；nil 表示用默认。
type EventSetting struct {
	Enabled *bool `json:"enabled,omitempty"`
	Feishu  *bool `json:"feishu,omitempty"`
	Email   *bool `json:"email,omitempty"`
}

// storedConfig 是存进系统设置的 JSON；飞书地址和密钥加密保存。
type storedConfig struct {
	FeishuEnabled      bool                    `json:"feishu_enabled"`
	WebhookURLEnc      string                  `json:"webhook_url_enc,omitempty"`
	SecretEnc          string                  `json:"secret_enc,omitempty"`
	EmailEnabled       *bool                   `json:"email_enabled,omitempty"`
	MergeWindowSeconds int                     `json:"merge_window_seconds,omitempty"`
	Events             map[string]EventSetting `json:"events,omitempty"`
}

// runtimeConfig 是解密后的配置（只在内存里）。
type runtimeConfig struct {
	feishuEnabled bool
	webhookURL    string
	secret        string
	emailEnabled  bool
	mergeWindow   int
	events        map[string]EventSetting
}

func (c *runtimeConfig) feishuConfigured() bool {
	return c.feishuEnabled && c.webhookURL != ""
}

// rule 返回一类事件现在的设置：是否通知、发哪些渠道。不认识的事件类型不通知。
func (c *runtimeConfig) rule(kind string) (enabled, feishu, email bool, spec eventSpec) {
	spec, ok := specFor(kind)
	if !ok {
		return false, false, false, spec
	}
	set := c.events[kind]
	enabled, feishu, email = spec.DefaultOn, spec.DefaultFeishu, spec.DefaultEmail
	if set.Enabled != nil {
		enabled = *set.Enabled
	}
	if set.Feishu != nil {
		feishu = *set.Feishu
	}
	if set.Email != nil {
		email = *set.Email
	}
	return enabled, feishu, email, spec
}

// PublicEvent 是管理页上一类事件的设置。
type PublicEvent struct {
	Kind           string `json:"kind"`
	Title          string `json:"title"`
	Severity       string `json:"severity"`
	DefaultEnabled bool   `json:"default_enabled"`
	Enabled        bool   `json:"enabled"`
	Feishu         bool   `json:"feishu"`
	Email          bool   `json:"email"`
}

// PublicConfig 是管理页看到的配置：地址和密钥不回传，只说配了没有（地址只给主机名）。
type PublicConfig struct {
	FeishuEnabled      bool          `json:"feishu_enabled"`
	WebhookConfigured  bool          `json:"webhook_configured"`
	WebhookHost        string        `json:"webhook_host,omitempty"`
	SecretConfigured   bool          `json:"secret_configured"`
	EmailEnabled       bool          `json:"email_enabled"`
	MergeWindowSeconds int           `json:"merge_window_seconds"`
	Events             []PublicEvent `json:"events"`
}

// ConfigUpdate 是一次修改；nil 的项不动。WebhookURL、Secret 传空串表示清除。
type ConfigUpdate struct {
	FeishuEnabled      *bool                   `json:"feishu_enabled,omitempty"`
	WebhookURL         *string                 `json:"webhook_url,omitempty"`
	Secret             *string                 `json:"secret,omitempty"`
	EmailEnabled       *bool                   `json:"email_enabled,omitempty"`
	MergeWindowSeconds *int                    `json:"merge_window_seconds,omitempty"`
	Events             map[string]EventSetting `json:"events,omitempty"`
}

func (n *Notifier) loadStored(ctx context.Context) (storedConfig, error) {
	var sc storedConfig
	raw, err := n.opts.Settings.GetValue(ctx, SettingKeyConfig)
	if err != nil && !errors.Is(err, service.ErrSettingNotFound) {
		return sc, err
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &sc); err != nil {
			return sc, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
	}
	return sc, nil
}

func (n *Notifier) decrypt(sc storedConfig) (*runtimeConfig, error) {
	rc := &runtimeConfig{feishuEnabled: sc.FeishuEnabled, emailEnabled: true, mergeWindow: sc.MergeWindowSeconds, events: sc.Events}
	if sc.EmailEnabled != nil {
		rc.emailEnabled = *sc.EmailEnabled
	}
	if rc.mergeWindow <= 0 {
		rc.mergeWindow = defaultMergeWindowSeconds
	}
	if rc.events == nil {
		rc.events = map[string]EventSetting{}
	}
	var err error
	if sc.WebhookURLEnc != "" {
		if rc.webhookURL, err = n.opts.Encryptor.Decrypt(sc.WebhookURLEnc); err != nil {
			return nil, fmt.Errorf("decrypt feishu webhook: %w", err)
		}
	}
	if sc.SecretEnc != "" {
		if rc.secret, err = n.opts.Encryptor.Decrypt(sc.SecretEnc); err != nil {
			return nil, fmt.Errorf("decrypt feishu secret: %w", err)
		}
	}
	return rc, nil
}

// runtime 读出当前配置（解密后）。
func (n *Notifier) runtime(ctx context.Context) (*runtimeConfig, error) {
	sc, err := n.loadStored(ctx)
	if err != nil {
		return nil, err
	}
	return n.decrypt(sc)
}

// Config 返回管理页看到的配置。
func (n *Notifier) Config(ctx context.Context) (PublicConfig, error) {
	rc, err := n.runtime(ctx)
	if err != nil {
		return PublicConfig{}, err
	}
	return publicConfig(rc), nil
}

func publicConfig(rc *runtimeConfig) PublicConfig {
	out := PublicConfig{FeishuEnabled: rc.feishuEnabled, WebhookConfigured: rc.webhookURL != "", SecretConfigured: rc.secret != "",
		EmailEnabled: rc.emailEnabled, MergeWindowSeconds: rc.mergeWindow, Events: make([]PublicEvent, 0, len(eventSpecs))}
	if u, err := url.Parse(rc.webhookURL); err == nil {
		out.WebhookHost = u.Host
	}
	for _, s := range eventSpecs {
		enabled, feishu, email, _ := rc.rule(s.Kind)
		out.Events = append(out.Events, PublicEvent{Kind: s.Kind, Title: s.Title, Severity: string(s.Severity), DefaultEnabled: s.DefaultOn,
			Enabled: enabled, Feishu: feishu, Email: email})
	}
	return out
}

// SetConfig 保存配置（调用方负责二次验证和审计）。飞书地址必须是飞书 / Lark 机器人的 Webhook（防止被拿去请求任意地址）。
func (n *Notifier) SetConfig(ctx context.Context, upd ConfigUpdate) (PublicConfig, error) {
	sc, err := n.loadStored(ctx)
	if err != nil {
		return PublicConfig{}, err
	}
	if upd.FeishuEnabled != nil {
		sc.FeishuEnabled = *upd.FeishuEnabled
	}
	if upd.WebhookURL != nil {
		if strings.TrimSpace(*upd.WebhookURL) == "" {
			sc.WebhookURLEnc = ""
		} else {
			u := strings.TrimSpace(*upd.WebhookURL)
			if err := n.validateWebhook(u); err != nil {
				return PublicConfig{}, err
			}
			if sc.WebhookURLEnc, err = n.opts.Encryptor.Encrypt(u); err != nil {
				return PublicConfig{}, err
			}
		}
	}
	if upd.Secret != nil {
		if strings.TrimSpace(*upd.Secret) == "" {
			sc.SecretEnc = ""
		} else if sc.SecretEnc, err = n.opts.Encryptor.Encrypt(strings.TrimSpace(*upd.Secret)); err != nil {
			return PublicConfig{}, err
		}
	}
	if upd.EmailEnabled != nil {
		v := *upd.EmailEnabled
		sc.EmailEnabled = &v
	}
	if upd.MergeWindowSeconds != nil {
		if *upd.MergeWindowSeconds < 0 || *upd.MergeWindowSeconds > 600 {
			return PublicConfig{}, fmt.Errorf("%w: merge_window_seconds must be between 0 and 600", ErrInvalidConfig)
		}
		sc.MergeWindowSeconds = *upd.MergeWindowSeconds
	}
	for kind, set := range upd.Events {
		if _, ok := specFor(kind); !ok {
			return PublicConfig{}, fmt.Errorf("%w: unknown event %q", ErrInvalidConfig, kind)
		}
		if sc.Events == nil {
			sc.Events = map[string]EventSetting{}
		}
		sc.Events[kind] = set
	}
	if sc.FeishuEnabled && sc.WebhookURLEnc == "" {
		return PublicConfig{}, fmt.Errorf("%w: the feishu channel needs a webhook address", ErrInvalidConfig)
	}
	raw, err := json.Marshal(sc)
	if err != nil {
		return PublicConfig{}, err
	}
	if err := n.opts.Settings.Set(ctx, SettingKeyConfig, string(raw)); err != nil {
		return PublicConfig{}, err
	}
	return n.Config(ctx)
}

// validateWebhook 只接受飞书 / Lark 自定义机器人的 Webhook 地址（https、官方域名、固定路径前缀）。
func (n *Notifier) validateWebhook(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%w: the webhook address must be an https URL", ErrInvalidConfig)
	}
	allowed := append([]string{"open.feishu.cn", "open.larksuite.com"}, n.opts.AllowedHosts...)
	ok := false
	for _, h := range allowed {
		if strings.EqualFold(u.Hostname(), h) {
			ok = true
		}
	}
	if !ok || !strings.HasPrefix(u.Path, "/open-apis/bot/v2/hook/") {
		return fmt.Errorf("%w: the webhook address must be a Feishu or Lark custom bot address", ErrInvalidConfig)
	}
	return nil
}
