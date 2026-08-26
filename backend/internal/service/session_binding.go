package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ErrSessionBindingMismatch 会话绑定的 IP/UA 发生变化，会话已失效。
var ErrSessionBindingMismatch = infraerrors.Unauthorized("SESSION_BINDING_MISMATCH", "session network fingerprint changed, please login again")

// SessionBinding 会话指纹：登录时的客户端 IP 与 User-Agent。
// 会话绑定开启时，两者任一变化即导致会话失效（防止凭证被盗后异地重放）。
type SessionBinding struct {
	IP        string
	UserAgent string
}

// Hash 计算绑定指纹哈希（IP 网段与 UA 合并，任一变化哈希即变化）。
//
// IP 只取到网段而不是精确地址：精确匹配会把正常用户当成攻击者踢下线 ——
// 家宽动态 IP 续租、运营商 NAT 出口漂移、Wi-Fi 与移动网络切换都会换 IP，
// 而这些场景里用户根本没动。实测表现为「挂着页面过一阵子就提示登录失效」，
// 且因为撤销的是整个 token family，所有设备会一起登出。
// 网段粒度保留了「凭证被盗后异地重放」的拦截能力：跨机房、跨地域的重放
// 几乎不可能落在同一个 /24 或 /48 里。
func (b *SessionBinding) Hash() string {
	return b.hashWith(normalizeIPForBinding)
}

// LegacyHash 用精确 IP 计算哈希，即本功能最初的算法。
//
// 只用于过渡：改算法会让所有已签发会话的哈希对不上，若直接生效，等于把全体
// 在线用户登出一次 —— 而这正是本次要消除的症状。校验侧先比新哈希，不匹配时
// 再比一次旧哈希，命中即认定为旧会话并放行，轮转时自然写入新哈希。
func (b *SessionBinding) LegacyHash() string {
	return b.hashWith(func(ip string) string { return ip })
}

func (b *SessionBinding) hashWith(normalizeIP func(string) string) string {
	if b == nil {
		return ""
	}
	ip := strings.TrimSpace(b.IP)
	ua := strings.TrimSpace(b.UserAgent)
	if ip == "" && ua == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(normalizeIP(ip) + "\n" + ua))
	return hex.EncodeToString(sum[:16])
}

// normalizeIPForBinding 把地址归一到网段：IPv4 取 /24，IPv6 取 /48。
//
// 解析不出来的输入原样返回，不做任何放宽 —— 宁可保持严格，也好过因为格式
// 意外就把绑定校验变成一句空话。
func normalizeIPForBinding(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// 入口偶尔会带上端口（例如 RemoteAddr 直传），先剥掉再解析。
	if host, _, err := net.SplitHostPort(raw); err == nil && host != "" {
		raw = host
	}
	addr := net.ParseIP(raw)
	if addr == nil {
		return raw
	}
	if v4 := addr.To4(); v4 != nil {
		return v4.Mask(net.CIDRMask(24, 32)).String()
	}
	return addr.Mask(net.CIDRMask(48, 128)).String()
}

type sessionBindingCtxKey struct{}

// WithSessionBinding 将会话指纹注入 context（由 HTTP 入口中间件调用）。
func WithSessionBinding(ctx context.Context, binding *SessionBinding) context.Context {
	if binding == nil {
		return ctx
	}
	return context.WithValue(ctx, sessionBindingCtxKey{}, binding)
}

// SessionBindingFromContext 从 context 提取会话指纹；不存在时返回 nil。
func SessionBindingFromContext(ctx context.Context) *SessionBinding {
	if ctx == nil {
		return nil
	}
	binding, _ := ctx.Value(sessionBindingCtxKey{}).(*SessionBinding)
	return binding
}

// sessionBindingHashFromContext 提取指纹哈希，缺失时返回空串。
func sessionBindingHashFromContext(ctx context.Context) string {
	return SessionBindingFromContext(ctx).Hash()
}

// sessionBindingLegacyHashFromContext 提取旧算法（精确 IP）的指纹哈希，
// 仅供校验侧识别改算法之前签发的会话，缺失时返回空串。
func sessionBindingLegacyHashFromContext(ctx context.Context) string {
	return SessionBindingFromContext(ctx).LegacyHash()
}
