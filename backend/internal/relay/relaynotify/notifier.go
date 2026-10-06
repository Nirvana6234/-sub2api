package relaynotify

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
)

// AlertSink 把事件记进现有运维告警（后台查看、确认、恢复）。
type AlertSink interface {
	// CreateAlert 新建一条告警事件，severity 是运维告警的严重程度（P0 / P1 / P2）。
	CreateAlert(ctx context.Context, severity, title, description string, dimensions map[string]any) (int64, error)
	// ResolveAlert 把这条告警标为已恢复。
	ResolveAlert(ctx context.Context, id int64) error
}

// EmailSender 发邮件：收件人沿用运维告警的邮件配置。
type EmailSender interface {
	Recipients(ctx context.Context) []string
	Send(ctx context.Context, to, subject, body string) error
}

// NodeContext 是事件内容里的节点信息（设计 13：不含用户信息、密钥、凭据）。
type NodeContext = master.NodeContext

// ContextSource 提供事件内容里的节点信息（主从分流运行时）。
type ContextSource interface {
	NodeContext(ctx context.Context, nodeID int64) (NodeContext, bool)
}

// Options 是通知器的依赖。
type Options struct {
	Settings  Settings
	Encryptor Encryptor
	Alerts    AlertSink
	Email     EmailSender
	// SiteURL 返回后台地址（事件里的管理页链接）；nil 或空时不带链接。
	SiteURL func(ctx context.Context) string
	// AllowedHosts 是除飞书 / Lark 官方域名之外允许的 Webhook 主机（测试用）。
	AllowedHosts []string
	HTTPClient   *http.Client
	Now          func() time.Time
	// Sleep 是重试之间的等待（测试里换成不等）。
	Sleep func(time.Duration)
}

// Encryptor 加解密密钥字段（service.SecretEncryptor 满足）。
type Encryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// 反复上下线的判定：unstableWindow 内离线达到 unstableOfflines 次，合并成一条"不稳定"，之后 unstableMute 内不再单独通知它的上下线。
const (
	unstableWindow   = 10 * time.Minute
	unstableOfflines = 3
	unstableMute     = 10 * time.Minute
	// 飞书发送：最多 3 次，间隔 2 秒、6 秒。
	feishuAttempts = 3
)

type pendingEvent struct {
	event   master.Event
	spec    eventSpec
	feishu  bool
	email   bool
	context NodeContext
	hasCtx  bool
	at      time.Time
}

// Notifier 实现 master.Notifier。
type Notifier struct {
	opts Options
	now  func() time.Time
	src  ContextSource

	mu      sync.Mutex
	pending []pendingEvent
	// 反复上下线检测。
	offlineAt map[int64][]time.Time
	mutedTill map[int64]time.Time
	// open 是每个（事件类型, 节点）还开着的运维告警 ID，恢复事件来时标为已恢复。
	open map[openKey]int64
}

type openKey struct {
	kind   string
	nodeID int64
}

var _ master.Notifier = (*Notifier)(nil)

// New 创建通知器。
func New(o Options) *Notifier {
	n := &Notifier{opts: o, now: o.Now, offlineAt: map[int64][]time.Time{}, mutedTill: map[int64]time.Time{}, open: map[openKey]int64{}}
	if n.now == nil {
		n.now = time.Now
	}
	if o.Sleep == nil {
		n.opts.Sleep = time.Sleep
	}
	if o.HTTPClient == nil {
		n.opts.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return n
}

// SetContextSource 接上节点信息来源（运行时创建之后）。
func (n *Notifier) SetContextSource(s ContextSource) {
	n.mu.Lock()
	n.src = s
	n.mu.Unlock()
}

// 恢复事件对应的告警事件类型（来了就把对应的告警标为已恢复）。
var resolves = map[string][]string{
	master.EventNodeOnline:     {master.EventNodeOffline, master.EventNodeUnreachable, master.EventNodeDegraded},
	master.EventRelayRecovered: {master.EventAllRelaysDownFallback, master.EventAllRelaysDownOutage},
}

// opsSeverity 把通知严重程度转成运维告警的严重程度。
func opsSeverity(s master.Severity) string {
	switch s {
	case master.SeverityEmergency, master.SeverityCritical:
		return "P0"
	case master.SeverityWarning:
		return "P1"
	default:
		return "P2"
	}
}

// Notify 实现 master.Notifier：记进运维告警，按合并规则排队，由 Flush 发出。
func (n *Notifier) Notify(ctx context.Context, e master.Event) {
	cfg, err := n.runtime(ctx)
	if err != nil {
		slog.Warn("relay notification config could not be read", "error", err)
		return
	}
	enabled, feishu, email, spec := cfg.rule(e.Kind)
	if !enabled {
		return
	}
	now := n.now()

	// 反复上下线：窗口内离线次数够多，合成一条"不稳定"，之后一段时间不再单独通知它的上下线（运维告警仍然记）。
	var unstable *master.Event
	if e.Kind == master.EventNodeOffline {
		n.mu.Lock()
		list := n.offlineAt[e.NodeID][:0]
		for _, at := range n.offlineAt[e.NodeID] {
			if now.Sub(at) < unstableWindow {
				list = append(list, at)
			}
		}
		list = append(list, now)
		n.offlineAt[e.NodeID] = list
		if len(list) >= unstableOfflines && !n.mutedTill[e.NodeID].After(now) {
			n.mutedTill[e.NodeID] = now.Add(unstableMute)
			ev := master.Event{Kind: master.EventNodeUnstable, Severity: master.SeverityCritical, NodeID: e.NodeID, Detail: map[string]any{"offlines_10m": len(list)}}
			unstable = &ev
		}
		n.mu.Unlock()
	}
	n.mu.Lock()
	muted := (e.Kind == master.EventNodeOffline || e.Kind == master.EventNodeOnline) && n.mutedTill[e.NodeID].After(now)
	n.mu.Unlock()

	n.recordAlert(ctx, e, spec)

	if unstable != nil {
		if _, uf, ue, uspec := cfg.rule(unstable.Kind); true {
			n.enqueue(ctx, *unstable, uspec, uf, ue, now)
		}
	}
	if muted {
		return
	}
	n.enqueue(ctx, e, spec, feishu, email, now)
}

func (n *Notifier) enqueue(ctx context.Context, e master.Event, spec eventSpec, feishu, email bool, now time.Time) {
	pe := pendingEvent{event: e, spec: spec, feishu: feishu, email: email, at: now}
	n.mu.Lock()
	src := n.src
	n.mu.Unlock()
	if src != nil && e.NodeID > 0 {
		pe.context, pe.hasCtx = src.NodeContext(ctx, e.NodeID)
	}
	n.mu.Lock()
	n.pending = append(n.pending, pe)
	n.mu.Unlock()
}

// recordAlert 把事件记进运维告警：故障类事件新建一条（firing），恢复类事件把对应的告警标为已恢复。
func (n *Notifier) recordAlert(ctx context.Context, e master.Event, spec eventSpec) {
	if n.opts.Alerts == nil {
		return
	}
	if kinds, ok := resolves[e.Kind]; ok {
		for _, k := range kinds {
			key := openKey{k, e.NodeID}
			n.mu.Lock()
			id, open := n.open[key]
			delete(n.open, key)
			n.mu.Unlock()
			if open {
				if err := n.opts.Alerts.ResolveAlert(ctx, id); err != nil {
					slog.Warn("relay alert could not be resolved", "error", err)
				}
			}
		}
		return
	}
	if spec.Severity == master.SeverityInfo {
		return // 提示类只通知，不占运维告警
	}
	dims := map[string]any{"kind": e.Kind, "node_id": e.NodeID}
	for k, v := range e.Detail {
		dims[k] = v
	}
	id, err := n.opts.Alerts.CreateAlert(ctx, opsSeverity(spec.Severity), truncate(spec.Title, 190), n.describe(ctx, e), dims)
	if err != nil {
		slog.Warn("relay alert could not be recorded", "error", err)
		return
	}
	n.mu.Lock()
	n.open[openKey{e.Kind, e.NodeID}] = id
	n.mu.Unlock()
}

func (n *Notifier) describe(ctx context.Context, e master.Event) string {
	n.mu.Lock()
	src := n.src
	n.mu.Unlock()
	var b strings.Builder
	if src != nil && e.NodeID > 0 {
		if nc, ok := src.NodeContext(ctx, e.NodeID); ok {
			fmt.Fprintf(&b, "节点 %s", nc.Name)
		}
	}
	for _, k := range sortedKeys(e.Detail) {
		fmt.Fprintf(&b, " %s=%v", k, e.Detail[k])
	}
	return strings.TrimSpace(b.String())
}

// Flush 把排队的事件合成一条发出去（多台同时出事、大量注册都合并成一条）。成功发出的算完成；飞书失败时改发邮件。
func (n *Notifier) Flush(ctx context.Context) {
	n.mu.Lock()
	batch := n.pending
	n.pending = nil
	n.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	cfg, err := n.runtime(ctx)
	if err != nil {
		slog.Warn("relay notification config could not be read", "error", err)
		return
	}
	subject, body := n.compose(ctx, batch)
	wantFeishu, wantEmail := false, false
	for _, p := range batch {
		wantFeishu = wantFeishu || p.feishu
		wantEmail = wantEmail || p.email
	}
	feishuFailed := false
	if wantFeishu && cfg.feishuConfigured() {
		if err := n.sendFeishu(ctx, cfg, body); err != nil {
			feishuFailed = true
			slog.Warn("relay feishu notification failed; falling back to email", "error", err)
		}
	} else if wantFeishu {
		feishuFailed = true // 没配飞书：想发飞书的事件改发邮件
	}
	if (wantEmail || feishuFailed) && cfg.emailEnabled {
		n.sendEmail(ctx, subject, body)
	}
}

// Run 按合并窗口定时发出排队的事件，直到 ctx 结束。
func (n *Notifier) Run(ctx context.Context) {
	for {
		window := time.Duration(defaultMergeWindowSeconds) * time.Second
		if cfg, err := n.runtime(ctx); err == nil {
			window = time.Duration(cfg.mergeWindow) * time.Second
		}
		select {
		case <-ctx.Done():
			n.Flush(context.Background())
			return
		case <-time.After(window):
			n.Flush(ctx)
		}
	}
}

// compose 组装合并后的消息：最高严重程度做标题，按事件类型分组，每台一行；同一类超过 3 条时只列前 3 台。
func (n *Notifier) compose(ctx context.Context, batch []pendingEvent) (subject, body string) {
	rank := func(s master.Severity) int {
		switch s {
		case master.SeverityEmergency:
			return 4
		case master.SeverityCritical:
			return 3
		case master.SeverityWarning:
			return 2
		default:
			return 1
		}
	}
	top := batch[0].spec.Severity
	groups := map[string][]pendingEvent{}
	var order []string
	for _, p := range batch {
		if rank(p.spec.Severity) > rank(top) {
			top = p.spec.Severity
		}
		if _, seen := groups[p.event.Kind]; !seen {
			order = append(order, p.event.Kind)
		}
		groups[p.event.Kind] = append(groups[p.event.Kind], p)
	}
	label := map[master.Severity]string{master.SeverityEmergency: "紧急", master.SeverityCritical: "严重", master.SeverityWarning: "警告", master.SeverityInfo: "提示"}
	var b strings.Builder
	if len(batch) == 1 {
		subject = fmt.Sprintf("[%s] %s", label[top], batch[0].spec.Title)
	} else {
		subject = fmt.Sprintf("[%s] 主从分流：%d 条事件", label[top], len(batch))
	}
	b.WriteString(subject + "\n")
	for _, kind := range order {
		ps := groups[kind]
		spec := ps[0].spec
		if len(ps) > 1 || len(order) > 1 {
			fmt.Fprintf(&b, "\n▍%s（%s）", spec.Title, label[spec.Severity])
			if len(ps) > 1 {
				fmt.Fprintf(&b, " ×%d", len(ps))
			}
			b.WriteString("\n")
		}
		shown := ps
		if len(shown) > 3 {
			shown = shown[:3]
		}
		for _, p := range shown {
			b.WriteString("- " + n.line(p) + "\n")
		}
		if len(ps) > len(shown) {
			fmt.Fprintf(&b, "- 等共 %d 条\n", len(ps))
		}
		if spec.Action != "" {
			b.WriteString("  系统已采取：" + spec.Action + "\n")
		}
	}
	if n.opts.SiteURL != nil {
		if site := strings.TrimRight(n.opts.SiteURL(ctx), "/"); site != "" {
			b.WriteString("\n管理页：" + site + "/admin/relay\n")
		}
	}
	return subject, truncate(b.String(), 3800)
}

// line 是一条事件的内容：节点名称、IP、事件时间、最后一次心跳、受影响的分配人数、活跃 Key 数、进行中的请求数。
func (n *Notifier) line(p pendingEvent) string {
	var parts []string
	if p.hasCtx {
		who := p.context.Name
		if p.context.IP != "" {
			who += "（" + p.context.IP + "）"
		}
		parts = append(parts, who)
	} else if p.event.NodeID > 0 {
		parts = append(parts, fmt.Sprintf("节点 %d", p.event.NodeID))
	}
	parts = append(parts, "时间 "+p.at.Format("01-02 15:04:05"))
	if p.hasCtx {
		if p.context.LastHeartbeat != nil {
			parts = append(parts, "最后心跳 "+p.context.LastHeartbeat.Format("15:04:05"))
		}
		parts = append(parts, fmt.Sprintf("分配用户 %d、活跃 Key %d、进行中请求 %d", p.context.AssignedUsers, p.context.ActiveKeys, p.context.Inflight))
	}
	for _, k := range sortedKeys(p.event.Detail) {
		parts = append(parts, fmt.Sprintf("%s=%v", k, p.event.Detail[k]))
	}
	return strings.Join(parts, "，")
}

func (n *Notifier) sendEmail(ctx context.Context, subject, body string) {
	if n.opts.Email == nil {
		return
	}
	for _, to := range n.opts.Email.Recipients(ctx) {
		if err := n.opts.Email.Send(ctx, to, subject, body); err != nil {
			slog.Warn("relay notification email failed", "error", err)
		}
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
