package master

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 小白端用户的分配（设计 10.1、10.4、10.5、10.8、10.9）：
//   - 新用户先按"主节点分配比例"掷骰子，分到从节点的按剩余容量加权随机（不是每次挑最空的，最近几秒刚分出去的人数计入预估）；
//   - 已分配的保持不动，直到原节点不可用（离线、排空、停用、客户端报告连不上、被判定对外不可达）、比例改成 0（分到主节点的移走）、
//     或管理员移动 / 重新平衡；固定（带到期时间）的在原节点可用时不动，原节点不可用时自动解除固定并通知管理员；
//   - 从节点都不可用时由主节点判断：比例大于 0 临时分给主节点（原因 fallback，从节点恢复后下次询问分回去），比例为 0 返回"暂无可用中转"；
//     "可用但压力都很高"不算不可用，仍分给从节点。

const (
	// EventNodeUnreachableReports：很多用户报告连不上同一台节点，停止分配并告警（设计 10.3 第 2 条）。
	EventNodeUnreachableReports = "node_unreachable_reports"
	// EventUserPinReleased：固定的节点不可用，自动解除固定（设计 10.4）。
	EventUserPinReleased = "user_pin_released"

	// unreachableReportUsers 个不同用户在 unreachableReportWindow 内报告连不上同一台，这台停止分配 unreachableSuppress。
	unreachableReportUsers  = 5
	unreachableReportWindow = 2 * time.Minute
	unreachableSuppress     = 5 * time.Minute
	// recentAssignWindow：最近这么久刚分到某台的人数计入负载预估。
	recentAssignWindow = 10 * time.Second
	// recentAssignLoad：每个刚分到的人先按这个负载比例估（按带宽上限的比例）。
	recentAssignLoad = 0.01
	// maxPinDuration：固定必须设到期时间，最长这么久（设计 10.4）。
	maxPinDuration = 30 * 24 * time.Hour
)

// ErrInvalidPin：固定的到期时间不合法。
var ErrInvalidPin = errors.New("a pin needs an expiry time in the future, at most 30 days away")

type userAssigner struct {
	r     *Runtime
	store UserAssignmentStore
	rnd   func() float64

	mu         sync.Mutex
	recent     map[int64][]time.Time
	reports    map[int64]map[int64]time.Time // 节点 -> 用户 -> 报告时间
	suppressed map[int64]time.Time           // 节点 -> 停止分配到什么时候
	// availability 是"从节点是否可用"的当前状态（"" 还没判断过、ok、fallback、outage），状态变化时发通知。
	availability string
}

func newUserAssigner(r *Runtime, store UserAssignmentStore) *userAssigner {
	return &userAssigner{r: r, store: store, rnd: rand.Float64, recent: map[int64][]time.Time{}, reports: map[int64]map[int64]time.Time{}, suppressed: map[int64]time.Time{}}
}

var _ service.RelayUserAssigner = (*Runtime)(nil)

// AssignUser 实现 service.RelayUserAssigner。
func (r *Runtime) AssignUser(ctx context.Context, userID, unreachableNodeID int64) (*service.RelayUserAssignment, error) {
	rr, err := r.runningRelay()
	if err != nil || rr.users == nil {
		return nil, service.ErrRelayNotEnabled
	}
	return rr.users.assign(ctx, userID, unreachableNodeID)
}

func (u *userAssigner) now() time.Time { return u.r.now() }

// usable 报告一台从节点现在能不能接用户：已激活、有域名、心跳在线、没有因客户端报告被暂停分配、没有少报嫌疑。
func (u *userAssigner) usable(n *Node, rr *runningRelay) bool {
	if n.Status != NodeActive || n.PublicDomain == "" {
		return false
	}
	if rr.heartbeats != nil && (!rr.heartbeats.Online(n.ID) || rr.heartbeats.SuspectedUnderReporting(n.ID)) {
		return false
	}
	if rr.health != nil && !rr.health.Assignable(context.Background(), n.ID) {
		return false
	}
	u.mu.Lock()
	until, ok := u.suppressed[n.ID]
	u.mu.Unlock()
	return !(ok && until.After(u.now()))
}

// noteUnreachable 记一次客户端的"连不上"报告；够多时暂停分配这台。
func (u *userAssigner) noteUnreachable(ctx context.Context, nodeID, userID int64) {
	now := u.now()
	u.mu.Lock()
	m := u.reports[nodeID]
	if m == nil {
		m = map[int64]time.Time{}
		u.reports[nodeID] = m
	}
	m[userID] = now
	count := 0
	for id, at := range m {
		if now.Sub(at) > unreachableReportWindow {
			delete(m, id)
			continue
		}
		count++
	}
	fresh := false
	if count >= unreachableReportUsers && !u.suppressed[nodeID].After(now) {
		u.suppressed[nodeID] = now.Add(unreachableSuppress)
		fresh = true
	}
	u.mu.Unlock()
	if fresh && u.r.deps.Notifier != nil {
		u.r.deps.Notifier.Notify(ctx, Event{Kind: EventNodeUnreachableReports, Severity: SeverityCritical, NodeID: nodeID,
			Detail: map[string]any{"users": count, "suppressed_minutes": int(unreachableSuppress.Minutes())}})
	}
}

func (u *userAssigner) noteAssigned(nodeID int64) {
	now := u.now()
	u.mu.Lock()
	defer u.mu.Unlock()
	list := u.recent[nodeID][:0]
	for _, at := range u.recent[nodeID] {
		if now.Sub(at) < recentAssignWindow {
			list = append(list, at)
		}
	}
	u.recent[nodeID] = append(list, now)
}

func (u *userAssigner) recentCount(nodeID int64) int {
	now := u.now()
	u.mu.Lock()
	defer u.mu.Unlock()
	n := 0
	for _, at := range u.recent[nodeID] {
		if now.Sub(at) < recentAssignWindow {
			n++
		}
	}
	return n
}

func (u *userAssigner) assign(ctx context.Context, userID, unreachable int64) (*service.RelayUserAssignment, error) {
	if u.r.deps.Users == nil {
		return nil, service.ErrRelayNotEnabled
	}
	user, err := u.r.deps.Users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !user.IsActive() {
		return nil, service.ErrUserNotActive
	}
	rr, err := u.r.runningRelay()
	if err != nil {
		return nil, service.ErrRelayNotEnabled
	}
	cfg := u.r.cachedGeneralConfig(ctx).WithDefaults()
	nodes, err := u.r.deps.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	if unreachable > 0 {
		u.noteUnreachable(ctx, unreachable, userID)
	}

	cur, err := u.store.Get(ctx, userID)
	if err != nil && !errors.Is(err, ErrAssignmentNotFound) {
		return nil, err
	}
	if errors.Is(err, ErrAssignmentNotFound) {
		cur = nil
	}
	node, reason, err := u.decide(ctx, rr, cfg, byID, nodes, cur, unreachable)
	u.trackAvailability(err, node, reason)
	if err != nil {
		return nil, err
	}
	if cur == nil || cur.NodeID != node || cur.Reason != reason {
		row := &UserAssignment{UserID: userID, NodeID: node, Reason: reason}
		if cur != nil && cur.Pinned(u.now()) && cur.NodeID == node {
			row.PinnedUntil, row.Reason = cur.PinnedUntil, cur.Reason
		}
		if err := u.store.Put(ctx, row); err != nil {
			return nil, err
		}
		u.noteAssigned(node)
	}
	return u.result(ctx, rr, cfg, user, node)
}

// decide 定这次的节点：能留在原节点就留，否则重新选。返回节点（0 为主节点）和写进库的原因。
func (u *userAssigner) decide(ctx context.Context, rr *runningRelay, cfg GeneralConfig, byID map[int64]*Node, nodes []*Node, cur *UserAssignment, unreachable int64) (int64, string, error) {
	ratio := *cfg.MasterRatioPercent
	now := u.now()
	if cur != nil {
		switch {
		case cur.NodeID == 0:
			// 分到主节点的：比例为 0 时移走；因为从节点不可用而临时分过来的（fallback），有可用的从节点了就分回去。
			if ratio > 0 && !(cur.Reason == AssignReasonFallback && u.anyUsable(rr, nodes)) {
				return 0, cur.Reason, nil
			}
		default:
			n := byID[cur.NodeID]
			ok := n != nil && u.usable(n, rr) && cur.NodeID != unreachable
			if ok {
				return cur.NodeID, cur.Reason, nil
			}
			if cur.Pinned(now) {
				// 固定的节点不可用：自动解除固定并通知管理员（设计 10.4）。
				if u.r.deps.Notifier != nil {
					u.r.deps.Notifier.Notify(ctx, Event{Kind: EventUserPinReleased, Severity: SeverityWarning, NodeID: cur.NodeID,
						Detail: map[string]any{"user_id": cur.UserID}})
				}
			}
		}
		// 固定在主节点（比例大于 0 时有效）：保持。
		if cur.NodeID == 0 && cur.Pinned(now) && ratio > 0 {
			return 0, cur.Reason, nil
		}
	}
	exclude := map[int64]bool{}
	if unreachable > 0 {
		exclude[unreachable] = true
	}
	if cur != nil && cur.NodeID > 0 {
		exclude[cur.NodeID] = true
	}
	return u.pick(rr, cfg, nodes, exclude, cur == nil)
}

func (u *userAssigner) anyUsable(rr *runningRelay, nodes []*Node) bool {
	for _, n := range nodes {
		if u.usable(n, rr) {
			return true
		}
	}
	return false
}

// pick 选一个节点（设计 10.1、10.5）。firstTime 为 true 时先按比例掷骰子决定给不给主节点。
func (u *userAssigner) pick(rr *runningRelay, cfg GeneralConfig, nodes []*Node, exclude map[int64]bool, firstTime bool) (int64, string, error) {
	ratio := *cfg.MasterRatioPercent
	threshold := float64(cfg.LoadThresholdPercent) / 100
	load := func(n *Node) float64 {
		if rr.heartbeats == nil {
			return 0
		}
		l := rr.heartbeats.Load(n.ID, n.BandwidthLimitMbps)
		return l + float64(u.recentCount(n.ID))*recentAssignLoad
	}
	var pool, overloaded []*Node
	for _, n := range nodes {
		if exclude[n.ID] || !u.usable(n, rr) {
			continue
		}
		if load(n) > threshold {
			overloaded = append(overloaded, n)
			continue
		}
		pool = append(pool, n)
	}
	masterOK := ratio > 0 && !exclude[0]
	if firstTime && masterOK && u.masterLoad() <= threshold && u.rnd()*100 < float64(ratio) {
		return 0, AssignReasonNew, nil
	}
	reason := AssignReasonFailover
	if firstTime {
		reason = AssignReasonNew
	}
	if len(pool) > 0 {
		return weightedByCapacity(pool, load, u.rnd).ID, reason, nil
	}
	if len(overloaded) > 0 {
		// 可用但压力都很高：仍分到从节点（设计 10.5），挑负载最低的。
		best := overloaded[0]
		for _, n := range overloaded[1:] {
			if load(n) < load(best) {
				best = n
			}
		}
		return best.ID, reason, nil
	}
	if masterOK {
		return 0, AssignReasonFallback, nil
	}
	return 0, "", service.ErrRelayUnavailable
}

// masterLoad 是主节点的转发负载（转发上限用满的比例）；没设上限时为 0。
func (u *userAssigner) masterLoad() float64 { return u.r.masterLoad() }

// weightedByCapacity 按剩余容量加权随机：权重 = 带宽上限 × (1 − 负载)，至少留 2%，不是每次挑最空的。
func weightedByCapacity(pool []*Node, load func(*Node) float64, rnd func() float64) *Node {
	weights := make([]float64, len(pool))
	var sum float64
	for i, n := range pool {
		bw := float64(n.BandwidthLimitMbps)
		if bw <= 0 {
			bw = DefaultNodeBandwidthMbps
		}
		free := 1 - load(n)
		if free < 0.02 {
			free = 0.02
		}
		weights[i] = bw * free
		sum += weights[i]
	}
	r := rnd() * sum
	for i, w := range weights {
		if r < w {
			return pool[i]
		}
		r -= w
	}
	return pool[len(pool)-1]
}

// result 组装返回：从节点带中转票据（带用户当前的 token_version），主节点沿用登录态。
func (u *userAssigner) result(ctx context.Context, rr *runningRelay, cfg GeneralConfig, user *service.User, nodeID int64) (*service.RelayUserAssignment, error) {
	out := &service.RelayUserAssignment{NodeID: nodeID, RefreshAfter: cfg.AssignmentRefreshSeconds}
	addr, ok := u.r.ResolveRelayAddress(ctx, nodeID)
	if !ok {
		return nil, service.ErrRelayUnavailable
	}
	out.BaseURL = addr.BaseURL
	if nodeID == 0 {
		out.Role = service.RelayRoleMaster
		return out, nil
	}
	out.Role = service.RelayRoleRelay
	ticket, t, err := u.r.IssueTicket(user.ID, nodeID, service.ResolvedTokenVersion(user))
	if err != nil {
		return nil, err
	}
	exp := time.UnixMilli(t.GetExpiresAtUnixMs())
	out.Ticket, out.TicketExpiresAt = ticket, &exp
	// 票据到期前续签：建议的询问间隔不超过票据有效期的一半。
	if half := int(sign.TicketLifetime.Seconds() / 2); out.RefreshAfter <= 0 || out.RefreshAfter > half {
		out.RefreshAfter = half
	}
	return out, nil
}

// trackAvailability 记录"所有从节点不可用"的状态变化（设计 13 的三条事件）：进入回退（比例大于 0，临时分给主节点）、
// 进入服务中断（比例为 0）、恢复（又有从节点可用）；只在状态变化时通知一次。
func (u *userAssigner) trackAvailability(err error, node int64, reason string) {
	state := "ok"
	switch {
	case errors.Is(err, service.ErrRelayUnavailable):
		state = "outage"
	case err == nil && node == 0 && reason == AssignReasonFallback:
		state = "fallback"
	case err != nil:
		return
	}
	u.mu.Lock()
	prev := u.availability
	u.availability = state
	u.mu.Unlock()
	if prev == state || (prev == "" && state == "ok") || u.r.deps.Notifier == nil {
		return
	}
	kind, sev := EventRelayRecovered, SeverityInfo
	switch state {
	case "fallback":
		kind, sev = EventAllRelaysDownFallback, SeverityEmergency
	case "outage":
		kind, sev = EventAllRelaysDownOutage, SeverityEmergency
	}
	go u.r.deps.Notifier.Notify(context.Background(), Event{Kind: kind, Severity: sev})
}

// ---- 管理员操作（设计 10.4、10.8）----

// PinUser 把小白端用户固定在一台节点（0 为主节点）上，必须设到期时间；节点必须现在能接用户。
func (r *Runtime) PinUser(ctx context.Context, actor, userID, nodeID int64, until time.Time) error {
	rr, err := r.runningRelay()
	if err != nil || rr.users == nil {
		return ErrKeyAssignmentUnavailable
	}
	now := r.now()
	if !until.After(now) || until.After(now.Add(maxPinDuration)) {
		return ErrInvalidPin
	}
	if err := r.validateUserTarget(ctx, rr, nodeID); err != nil {
		return err
	}
	if err := rr.users.store.Put(ctx, &UserAssignment{UserID: userID, NodeID: nodeID, Reason: AssignReasonPinned, PinnedUntil: &until}); err != nil {
		return err
	}
	r.audit(ctx, actor, AuditUserPinned, map[string]any{"user_id": userID, "node_id": nodeID, "until": until})
	return nil
}

// MoveUser 把用户移到指定节点（不固定：之后节点不可用时照常换）。下一次询问时生效。
func (r *Runtime) MoveUser(ctx context.Context, actor, userID, nodeID int64) error {
	rr, err := r.runningRelay()
	if err != nil || rr.users == nil {
		return ErrKeyAssignmentUnavailable
	}
	if err := r.validateUserTarget(ctx, rr, nodeID); err != nil {
		return err
	}
	if err := rr.users.store.Put(ctx, &UserAssignment{UserID: userID, NodeID: nodeID, Reason: AssignReasonManual}); err != nil {
		return err
	}
	r.audit(ctx, actor, AuditUserMoved, map[string]any{"user_id": userID, "node_id": nodeID})
	return nil
}

// UnpinUser 解除固定（保留当前分配）。
func (r *Runtime) UnpinUser(ctx context.Context, actor, userID int64) error {
	rr, err := r.runningRelay()
	if err != nil || rr.users == nil {
		return ErrKeyAssignmentUnavailable
	}
	cur, err := rr.users.store.Get(ctx, userID)
	if err != nil {
		return err
	}
	cur.PinnedUntil, cur.Reason = nil, AssignReasonManual
	if err := rr.users.store.Put(ctx, cur); err != nil {
		return err
	}
	r.audit(ctx, actor, AuditUserUnpinned, map[string]any{"user_id": userID})
	return nil
}

// RebalanceUsers 重新平衡（设计 10.8）：删掉所有没固定的分配，小白端下一次询问时按当前比例和容量重新分配。返回受影响的用户数。
func (r *Runtime) RebalanceUsers(ctx context.Context, actor int64) (int64, error) {
	rr, err := r.runningRelay()
	if err != nil || rr.users == nil {
		return 0, ErrKeyAssignmentUnavailable
	}
	n, err := rr.users.store.DeleteUnpinned(ctx, r.now())
	if err != nil {
		return 0, err
	}
	r.audit(ctx, actor, AuditUsersRebalanced, map[string]any{"users": n})
	return n, nil
}

// UserAssignmentSummary 返回各节点上分到的小白端用户数（改比例、停用节点前看影响）。
func (r *Runtime) UserAssignmentSummary(ctx context.Context) (map[int64]int64, error) {
	rr, err := r.runningRelay()
	if err != nil || rr.users == nil {
		return nil, ErrKeyAssignmentUnavailable
	}
	return rr.users.store.CountByNode(ctx)
}

func (r *Runtime) validateUserTarget(ctx context.Context, rr *runningRelay, nodeID int64) error {
	if nodeID == 0 {
		if *r.cachedGeneralConfig(ctx).WithDefaults().MasterRatioPercent <= 0 {
			return ErrMasterRatioZero
		}
		return nil
	}
	n, err := r.deps.Store.GetByID(ctx, nodeID)
	if err != nil {
		return err
	}
	if !rr.users.usable(n, rr) {
		return ErrKeyTargetUnavailable
	}
	return nil
}
