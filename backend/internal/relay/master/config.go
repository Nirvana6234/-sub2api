package master

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 主从分流的后台设置（存在 settings 表里）。
const (
	// SettingKeyRelayEnabled 是主从分流总开关（"true"/"false"）。改动当场生效（动态启停）。
	SettingKeyRelayEnabled = "relay_enabled"
	// SettingKeyRelayGeneralConfig 是主从分流通用配置（JSON，见 GeneralConfig）。
	SettingKeyRelayGeneralConfig = "relay_general_config"
)

// ForwardingSettingKeys 是按白名单下发给从节点的系统设置（设计 6 第二类）。
//
// 只放转发要用、且不含任何密钥的项。新增项必须在代码评审时确认不是密钥；
// config_test.go 会检查名字，生成快照时还会检查 JSON 值里的字段名。
// 不在这里的（联网搜索配置里有服务密钥、面板限流只给网页、账号健康熔断在选号时由主节点处理等）
// 从节点读到空值。WP9 的依赖扫描会补全这张表。
var ForwardingSettingKeys = []string{
	// 客户端版本与身份
	service.SettingKeyMinClaudeCodeVersion,
	service.SettingKeyMaxClaudeCodeVersion,
	service.SettingKeyMinCodexVersion,
	service.SettingKeyMaxCodexVersion,
	service.SettingKeyOpenAICodexUserAgent,
	service.SettingKeyOpenAICodexClientVersion,
	service.SettingKeyOpenAICodexClientVersionSynced,
	service.SettingKeyAntigravityUserAgentVersion,
	service.SettingKeyCodexCLIOnlyWhitelist,
	service.SettingKeyCodexCLIOnlyBlacklist,
	service.SettingKeyCodexCLIOnlyEngineFingerprintSignals,
	service.SettingKeyCodexCLIOnlyAllowAppServerClients,
	service.SettingKeyCodexCLIOnlyAllowBodyEngineFingerprint,
	service.SettingKeyOpenAIAllowClaudeCodeCodexPlugin,
	// 转发改写
	service.SettingKeyOpenAITTFTMode,
	service.SettingKeyEnableFingerprintUnification,
	service.SettingKeyEnableMetadataPassthrough,
	service.SettingKeyEnableCCHSigning,
	service.SettingKeyEnableClaudeOAuthSystemPromptInjection,
	service.SettingKeyClaudeOAuthSystemPrompt,
	service.SettingKeyClaudeOAuthSystemPromptBlocks,
	service.SettingKeyEnableAnthropicCacheTTL1hInjection,
	service.SettingKeyRewriteMessageCacheControl,
	service.SettingKeyEnableClientDatelineNormalization,
	// Anthropic 转发路径：beta 策略、整流（签名/预算）开关。
	service.SettingKeyBetaPolicySettings,
	service.SettingKeyRectifierSettings,
	// Antigravity 转发路径：身份补丁开关与提示词。
	service.SettingKeyEnableIdentityPatch,
	service.SettingKeyIdentityPatchPrompt,
	service.SettingKeyGrokDefaultTextModel,
	// 风控：全局 IP 黑名单在从节点本地检查；cyber 会话屏蔽开关
	service.SettingKeyGlobalBlacklist,
	service.SettingKeyCyberSessionBlockEnabled,
	service.SettingKeyCyberSessionBlockTTLSeconds,
	// 风控中心开关：内容审核在从节点本地判定（设计 3.4；审核配置本身在加密下发的部分）
	service.SettingKeyRiskControlEnabled,
}

// secretName 匹配看起来像密钥的名字（设置名、JSON 字段名）。
var secretName = regexp.MustCompile(`(?i)(secret|password|passwd|api_?key|private_?key|token|credential)`)

// GeneralConfig 是主从分流通用配置（设计 11.3、19）。零值字段取默认值。
type GeneralConfig struct {
	// MasterRatioPercent：新分配里给主节点的比例（0~100，默认 10）。为 0 时主节点不做任何中转（设计 10.8）。
	MasterRatioPercent *int `json:"master_ratio_percent,omitempty"`
	// APIKeyNodeRule："assigned"（默认，仅分配的从节点）或 "any"（全部从节点，设计 10.2）。
	APIKeyNodeRule string `json:"api_key_node_rule,omitempty"`
	// HeartbeatIntervalSeconds / OfflineAfterSeconds：心跳与离线判定（默认 5 / 15）。
	HeartbeatIntervalSeconds int `json:"heartbeat_interval_seconds,omitempty"`
	OfflineAfterSeconds      int `json:"offline_after_seconds,omitempty"`
	// DrainMaxWaitMinutes：排空最长等待（默认 30）。
	DrainMaxWaitMinutes int `json:"drain_max_wait_minutes,omitempty"`
	// LoadThresholdPercent：超过就不再分配新用户和新 Key（默认 85）。
	LoadThresholdPercent int `json:"load_threshold_percent,omitempty"`
	// AssignmentRefreshSeconds：小白端分配查询间隔（默认 60）。
	AssignmentRefreshSeconds int `json:"assignment_refresh_seconds,omitempty"`
	// MasterMaxConcurrent：主节点同时转发的请求数上限（0 = 不限，设计 10.5）；超过时回"服务繁忙"。
	MasterMaxConcurrent int `json:"master_max_concurrent,omitempty"`
	// ProbeEnabledFlag：外部探测（设计 10.3）；默认开。本机开发、内网部署没有公网 HTTPS 时关掉（关了就不看探测结果）。
	ProbeEnabledFlag *bool `json:"probe_enabled,omitempty"`
	// ProbeIntervalSeconds：外部探测和域名解析检查的间隔（默认 60）。ProbePort：探测的端口（默认 443）。
	ProbeIntervalSeconds int `json:"probe_interval_seconds,omitempty"`
	ProbePort            int `json:"probe_port,omitempty"`
	// MasterMaxBandwidthMbps：主节点转发的带宽上限（0 = 不限）；负载 = 近 1 分钟收发速率较大者 ÷ 它，和并发占比取较大者。
	MasterMaxBandwidthMbps int `json:"master_max_bandwidth_mbps,omitempty"`
}

const (
	APIKeyNodeRuleAssigned = "assigned"
	APIKeyNodeRuleAny      = "any"
)

// WithDefaults 返回填好默认值的副本。
func (g GeneralConfig) WithDefaults() GeneralConfig {
	if g.MasterRatioPercent == nil {
		v := 10
		g.MasterRatioPercent = &v
	}
	if g.APIKeyNodeRule != APIKeyNodeRuleAny {
		g.APIKeyNodeRule = APIKeyNodeRuleAssigned
	}
	if g.HeartbeatIntervalSeconds <= 0 {
		g.HeartbeatIntervalSeconds = 5
	}
	if g.OfflineAfterSeconds <= 0 {
		g.OfflineAfterSeconds = 15
	}
	if g.DrainMaxWaitMinutes <= 0 {
		g.DrainMaxWaitMinutes = 30
	}
	if g.LoadThresholdPercent <= 0 {
		g.LoadThresholdPercent = 85
	}
	if g.AssignmentRefreshSeconds <= 0 {
		g.AssignmentRefreshSeconds = 60
	}
	if g.ProbeIntervalSeconds <= 0 {
		g.ProbeIntervalSeconds = 60
	}
	return g
}

// ProbeEnabled 报告外部探测是否打开（默认开）。
func (g GeneralConfig) ProbeEnabled() bool { return g.ProbeEnabledFlag == nil || *g.ProbeEnabledFlag }

// ProbePortOrDefault 返回探测端口（默认 443）。
func (g GeneralConfig) ProbePortOrDefault() int {
	if g.ProbePort <= 0 {
		return 443
	}
	return g.ProbePort
}

// Validate 检查取值范围。
func (g GeneralConfig) Validate() error {
	if g.MasterRatioPercent != nil && (*g.MasterRatioPercent < 0 || *g.MasterRatioPercent > 100) {
		return fmt.Errorf("master_ratio_percent must be between 0 and 100")
	}
	if g.APIKeyNodeRule != "" && g.APIKeyNodeRule != APIKeyNodeRuleAssigned && g.APIKeyNodeRule != APIKeyNodeRuleAny {
		return fmt.Errorf("api_key_node_rule must be %q or %q", APIKeyNodeRuleAssigned, APIKeyNodeRuleAny)
	}
	if g.LoadThresholdPercent > 100 {
		return fmt.Errorf("load_threshold_percent must be at most 100")
	}
	if g.ProbePort < 0 || g.ProbePort > 65535 || g.ProbeIntervalSeconds < 0 {
		return fmt.Errorf("probe settings are out of range")
	}
	if g.MasterMaxConcurrent < 0 || g.MasterMaxBandwidthMbps < 0 {
		return fmt.Errorf("master forwarding limits must not be negative")
	}
	return nil
}

// SettingsReader 是读系统设置用的接口（service.SettingRepository 满足）。
type SettingsReader interface {
	GetValue(ctx context.Context, key string) (string, error)
	GetMultiple(ctx context.Context, keys []string) (map[string]string, error)
}

// LoadGeneralConfig 读取通用配置（没配过时是默认值）。
func LoadGeneralConfig(ctx context.Context, settings SettingsReader) (GeneralConfig, error) {
	raw, err := settings.GetValue(ctx, SettingKeyRelayGeneralConfig)
	if err != nil && err != service.ErrSettingNotFound {
		return GeneralConfig{}, err
	}
	var g GeneralConfig
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &g); err != nil {
			return GeneralConfig{}, fmt.Errorf("relay general config is not valid JSON: %w", err)
		}
	}
	return g.WithDefaults(), nil
}

// SectionProvider 生成一个配置分段（JSON）。分段内容同样不能含密钥。
type SectionProvider func(ctx context.Context) ([]byte, error)

// NodeConfig 是下发给某台从节点的配置（设计 11.3）。
type NodeConfig struct {
	NodeID             int64         `json:"node_id"`
	Name               string        `json:"name"`
	Region             string        `json:"region"`
	PublicDomain       string        `json:"public_domain"`
	Status             NodeStatus    `json:"status"`
	BandwidthLimitMbps int           `json:"bandwidth_limit_mbps"`
	General            GeneralConfig `json:"general"`
}

// globalSnapshot 是所有节点共用的部分。
type globalSnapshot struct {
	settings map[string]string
	sections map[string][]byte
	general  GeneralConfig
	trust    Trust
	hash     string
	// sealed 是按节点加密下发的部分（sealed.go）；nil 表示没有。
	sealed *sealedState
}

// buildGlobal 读取设置和各分段，生成共用部分。含密钥字段的 JSON 值被剔除并报错。
func buildGlobal(ctx context.Context, settings SettingsReader, sections map[string]SectionProvider, trust Trust, onSecret func(key string)) (*globalSnapshot, error) {
	values, err := settings.GetMultiple(ctx, ForwardingSettingKeys)
	if err != nil {
		return nil, fmt.Errorf("read forwarding settings: %w", err)
	}
	g := &globalSnapshot{settings: map[string]string{}, sections: map[string][]byte{}, trust: trust}
	for _, key := range ForwardingSettingKeys {
		v, ok := values[key]
		if !ok {
			continue
		}
		if jsonHasSecretField([]byte(v)) {
			if onSecret != nil {
				onSecret(key)
			}
			continue
		}
		g.settings[key] = v
	}
	for name, provide := range sections {
		raw, err := provide(ctx)
		if err != nil {
			return nil, fmt.Errorf("build config section %s: %w", name, err)
		}
		if jsonHasSecretField(raw) {
			if onSecret != nil {
				onSecret("section:" + name)
			}
			continue
		}
		g.sections[name] = raw
	}
	g.general, err = LoadGeneralConfig(ctx, settings)
	if err != nil {
		return nil, err
	}
	g.hash = hashParts(canonicalMap(g.settings), canonicalBytesMap(g.sections), mustJSON(g.general), mustJSON(g.trust.deliveryIDs()))
	return g, nil
}

// snapshotFor 给某台节点拼出完整快照；版本 = 共用部分哈希 + 本节点配置哈希。
func (g *globalSnapshot) snapshotFor(node *Node) *relayv1.ConfigSnapshot {
	nc := NodeConfig{General: g.general}
	if node != nil {
		nc.NodeID, nc.Name, nc.Region, nc.PublicDomain, nc.Status, nc.BandwidthLimitMbps =
			node.ID, node.Name, node.Region, node.PublicDomain, node.Status, node.BandwidthLimitMbps
	}
	nodeJSON := mustJSON(nc)
	settings := make(map[string]string, len(g.settings))
	for k, v := range g.settings {
		settings[k] = v
	}
	sections := make(map[string][]byte, len(g.sections))
	for k, v := range g.sections {
		sections[k] = v
	}
	return &relayv1.ConfigSnapshot{
		Version:          hashParts([]byte(g.hash), nodeJSON),
		Settings:         settings,
		Sections:         sections,
		NodeConfig:       nodeJSON,
		RootFingerprints: append([]string(nil), g.trust.RootFingerprints...),
		TicketPublicKeys: cloneSigningKeys(g.trust.TicketPublicKeys),
	}
}

// jsonHasSecretField 报告 JSON 值（对象或数组，任意深度）里有没有像密钥的字段名。
// 不是 JSON 的纯文本值返回 false。
func jsonHasSecretField(raw []byte) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	return anySecretKey(v)
}

func anySecretKey(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if secretName.MatchString(k) || anySecretKey(child) {
				return true
			}
		}
	case []any:
		for _, child := range t {
			if anySecretKey(child) {
				return true
			}
		}
	}
	return false
}

func hashParts(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = fmt.Fprintf(h, "%d:", len(p))
		_, _ = h.Write(p)
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func canonicalMap(m map[string]string) []byte {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([][2]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, [2]string{k, m[k]})
	}
	return mustJSON(out)
}

func canonicalBytesMap(m map[string][]byte) []byte {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, []any{k, m[k]})
	}
	return mustJSON(out)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// configRebuildDelay 是配置改动后合并的等待时间（开发计划 3.2）。
const configRebuildDelay = 200 * time.Millisecond

// configRecheckInterval 是定时重新生成快照的间隔：兜住绕开设置仓储直接改库的情况。
const configRecheckInterval = 30 * time.Second

func cloneSigningKeys(in []*relayv1.SigningPublicKey) []*relayv1.SigningPublicKey {
	out := make([]*relayv1.SigningPublicKey, 0, len(in))
	for _, k := range in {
		out = append(out, &relayv1.SigningPublicKey{Version: k.Version, PublicKey: append([]byte(nil), k.PublicKey...)})
	}
	return out
}

// SectionErrorPassthroughRules 是配置快照里错误透传规则的分段名（JSON：[]model.ErrorPassthroughRule）。
const SectionErrorPassthroughRules = "error_passthrough_rules"

// SectionTLSFingerprintProfiles 是配置快照里 TLS 指纹模板的分段名（JSON：[]model.TLSFingerprintProfile）。
// Anthropic OAuth 账号开了 TLS 指纹伪装时转发按它握手；模板里没有密钥。
const SectionTLSFingerprintProfiles = "tls_fingerprint_profiles"

// SectionChangedKey 是分段内容改了时发给 SettingChangeHub 的键：以 relay_ 开头，发布器据此当场重新生成。
func SectionChangedKey(section string) string { return "relay_section_" + section }
