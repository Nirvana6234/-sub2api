package master

import "context"

// 主节点层面的审计动作（与具体节点无关，NodeID 为 0）。
const (
	AuditRelaySwitched        = "relay_switched"
	AuditGeneralConfigChanged = "general_config_changed"
	AuditKeyStaged            = "key_staged"
	AuditKeyActivated         = "key_activated"
	AuditKeyRetired           = "key_retired"
)

type sourceIPKey struct{}

// WithSourceIP 把管理员请求的来源 IP 放进 ctx，管理操作写审计时带上（设计 11.5：记审计）。
func WithSourceIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, sourceIPKey{}, ip)
}

func sourceIPFrom(ctx context.Context) string {
	ip, _ := ctx.Value(sourceIPKey{}).(string)
	return ip
}

// sourceIPAuditStore 给没写来源 IP 的审计补上 ctx 里的管理员来源 IP。
// 从节点发起的动作（注册、领证、续签）自己填了连接来源，不受影响。
type sourceIPAuditStore struct{ NodeStore }

func (s sourceIPAuditStore) Audit(ctx context.Context, e AuditEntry) error {
	if e.SourceIP == "" {
		e.SourceIP = sourceIPFrom(ctx)
	}
	return s.NodeStore.Audit(ctx, e)
}
