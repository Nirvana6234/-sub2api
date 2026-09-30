package securityaudit

import (
	"context"
	"errors"
	"time"
)

// RelayPromptConfig 是主从分流时下发给从节点的提示词审计配置（设计 3.4、6 第二类）：主节点解密后的生效配置
// （含接口凭据，所以走按节点加密下发的部分；加密这些凭据的密钥不下发）和降级状态。
type RelayPromptConfig struct {
	// Active 是主节点当前生效的配置；主节点没有可用配置时为 nil。
	Active *ActiveConfig `json:"active,omitempty"`
	// Degraded：该开阻断模式却没有可用配置（主节点的 BlockingActivationDegraded）。从节点照样拒绝放行。
	Degraded bool `json:"degraded,omitempty"`
}

// RelayConfig 返回下发给从节点的提示词审计配置（主节点用）。
func (s *PromptService) RelayConfig() RelayPromptConfig {
	if s == nil || s.config == nil {
		return RelayPromptConfig{}
	}
	out := RelayPromptConfig{Degraded: s.config.BlockingActivationDegraded()}
	if active, ok := s.config.Active(); ok {
		out.Active = &active
	}
	return out
}

// SetConfigChangeListener 设置配置变化的回调（主节点的主从分流运行时用：当场把新配置推给从节点）。
func (s *PromptService) SetConfigChangeListener(fn func()) {
	if s == nil {
		return
	}
	if l, ok := s.config.(interface{ SetChangeListener(func()) }); ok {
		l.SetChangeListener(fn)
	}
}

var errRelayConfigReadOnly = errors.New("prompt audit config is managed on the master")

// RelayConfigStore 是从节点上的提示词审计配置（ConfigStore）：只读，来自主节点下发的 RelayPromptConfig。
// 生效模式、降级判断与主节点的 ConfigManager 相同：该阻断却没有可用配置时按阻断处理（审计不可用，拒绝放行）。
type RelayConfigStore struct {
	load func() (RelayPromptConfig, bool)
}

var _ ConfigStore = (*RelayConfigStore)(nil)

// NewRelayConfigStore 创建配置存储；load 返回当前下发的配置（还没拿到配置快照时 ok 为 false）。
func NewRelayConfigStore(load func() (RelayPromptConfig, bool)) *RelayConfigStore {
	return &RelayConfigStore{load: load}
}

func (r *RelayConfigStore) Start(context.Context) error    { return nil }
func (r *RelayConfigStore) Shutdown(context.Context) error { return nil }

func (r *RelayConfigStore) current() RelayPromptConfig {
	if r == nil || r.load == nil {
		return RelayPromptConfig{}
	}
	cfg, _ := r.load()
	return cfg
}

func (r *RelayConfigStore) Active() (ActiveConfig, bool) {
	cfg := r.current()
	if cfg.Active == nil {
		return ActiveConfig{}, false
	}
	return cloneActiveConfig(*cfg.Active), true
}

func (r *RelayConfigStore) BlockingActivationDegraded() bool { return r.current().Degraded }

func (r *RelayConfigStore) EffectiveMode() Mode {
	cfg := r.current()
	if cfg.Degraded {
		return ModeBlocking
	}
	if cfg.Active == nil {
		return ModeOff
	}
	return cfg.Active.EffectiveMode()
}

func (r *RelayConfigStore) Public() (PublicConfig, error) {
	return PublicConfig{}, errRelayConfigReadOnly
}

func (r *RelayConfigStore) Save(context.Context, UpdateConfigRequest, int64) (PublicConfig, error) {
	return PublicConfig{}, errRelayConfigReadOnly
}

func (r *RelayConfigStore) RuntimeState() (expected int64, active int64, loadedAt *time.Time, loadError string) {
	cfg := r.current()
	if cfg.Active != nil {
		active = cfg.Active.ConfigVersion
	}
	return active, active, nil, ""
}

func (r *RelayConfigStore) Encrypt(string) (string, error) { return "", errRelayConfigReadOnly }
func (r *RelayConfigStore) Decrypt(string) (string, error) { return "", errRelayConfigReadOnly }

// BuildEvent 用一次审计结果生成审计事件（与 PostgreSQL 写入的内容相同：证据按同样的长度脱敏、问题摘要），
// 从节点把它写进本机存储。
func BuildEvent(id, jobID int64, snapshot PromptSnapshot, configVersion int64, result *NormalizedResult, now time.Time) *Event {
	evidence := make(map[string]string, len(result.ScannerEvidence))
	for key, value := range result.ScannerEvidence {
		evidence[key] = RedactPreview(value, 160)
	}
	snapshot.Stage = normalizeStage(snapshot.Stage)
	snapshot.ScanText = ""
	event := &Event{
		ID: id, JobID: jobID, Snapshot: snapshot, Decision: result.Decision, RiskLevel: result.RiskLevel,
		Action: result.Action, Categories: result.Categories, MatchedScanners: result.MatchedScanners,
		ScannerScores: result.ScannerScores, ScannerEvidence: evidence, ScannerBackend: result.ScannerBackend,
		ScannerVersion: result.ScannerVersion, GuardEndpointID: result.GuardEndpointID, PolicyID: result.PolicyID,
		PolicyVersion: result.PolicyVersion, ConfigVersion: configVersion, ChunkTotal: result.ChunkTotal,
		LatencyMS: result.LatencyMS, CreatedAt: now,
	}
	event.IssueSummaries = BuildIssueSummaries(NormalizedResult{Decision: event.Decision, RiskLevel: event.RiskLevel,
		Action: event.Action, Categories: event.Categories, MatchedScanners: event.MatchedScanners,
		ScannerScores: event.ScannerScores, ScannerEvidence: event.ScannerEvidence})
	return event
}

// ShouldStoreEvent 报告这次结果要不要记审计事件（与 PostgreSQL 实现同一条规则：有风险的一律记，通过的按配置）。
func ShouldStoreEvent(decision EventDecision, storePassEvents bool) bool {
	return shouldStorePromptAuditEvent(decision, storePassEvents)
}
