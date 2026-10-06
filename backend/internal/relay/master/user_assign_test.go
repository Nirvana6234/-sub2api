package master_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type memUsers struct{ users map[int64]*service.User }

func (m memUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	if u, ok := m.users[id]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, service.ErrUserNotFound
}

type userAssignHarness struct {
	*runtimeHarness
	nodes    []*master.Node
	notifier *captureNotifier
	clock    *hbClock
	rnd      float64
	users    memUsers
}

// newUserAssignHarness 起一个运行中的主节点和两台已激活、心跳在线的节点（a、b，带宽 100 / 100），用户 1~20 都是启用的。
func newUserAssignHarness(t *testing.T, ratio int) *userAssignHarness {
	t.Helper()
	ctx := context.Background()
	n := &captureNotifier{}
	users := memUsers{users: map[int64]*service.User{}}
	for i := int64(1); i <= 20; i++ {
		users.users[i] = &service.User{ID: i, Email: "u@test", Status: service.StatusActive, PasswordHash: "h", TokenVersion: 3}
	}
	h := newRuntimeWith(t, nil, func(d *master.RuntimeDeps) { d.Users, d.Notifier = users, n })
	_, err := h.runtime.SetGeneralConfig(ctx, 1, master.GeneralConfig{MasterRatioPercent: &ratio})
	require.NoError(t, err)
	st, err := h.runtime.SetEnabled(ctx, 1, true)
	require.NoError(t, err)
	require.Equal(t, master.StateRunning, st.State, st.Reason)

	out := &userAssignHarness{runtimeHarness: h, notifier: n, users: users}
	for _, d := range []string{"a.example.com", "b.example.com"} {
		node, err := h.store.CreatePending(ctx, &master.Node{IdentityFingerprint: "fp-" + d, IdentityPublicKey: []byte{1}}, 20)
		require.NoError(t, err)
		require.NoError(t, h.store.Activate(ctx, node.ID, master.Activation{PublicDomain: d, BandwidthLimitMbps: 100, At: time.Now()}))
		giveEncryptionKey(t, h, node.ID)
		out.nodes = append(out.nodes, node)
	}
	out.clock = &hbClock{t: time.Now()}
	master.SetRuntimeClock(h.runtime, out.clock.now)
	master.SetUserAssignRand(h.runtime, func() float64 { return out.rnd })
	for _, node := range out.nodes {
		out.beat(node.ID, 0)
	}
	return out
}

// setLoad 让一台节点近 1 分钟的下行速率稳定在 rxBytesPerSec（先发很多个同样的样本，把之前的平均掉）。
func (u *userAssignHarness) setLoad(nodeID int64, rxBytesPerSec uint64) {
	for i := 0; i < 200; i++ {
		u.beat(nodeID, rxBytesPerSec)
	}
}

// beat 模拟一台节点的心跳（rxBytesPerSec 用来造负载）。
func (u *userAssignHarness) beat(nodeID int64, rxBytesPerSec uint64) {
	hb := master.HeartbeatsOf(u.runtime)
	hb.Record(context.Background(), nodeID, &relayv1.HeartbeatRequest{StartedAtUnixMs: 1, SentAtUnixMs: u.clock.now().UnixMilli(), RxBytesPerSec: rxBytesPerSec})
}

// advance 让时间过去 d：在线的节点（keep）按 5 秒一次继续发心跳，其余的就掉线了。
func (u *userAssignHarness) advance(d time.Duration, keep ...int64) {
	for elapsed := time.Duration(0); elapsed < d; elapsed += 5 * time.Second {
		u.clock.advance(5 * time.Second)
		for _, id := range keep {
			u.beat(id, 0)
		}
		master.HeartbeatsOf(u.runtime).Tick(context.Background())
	}
}

func (u *userAssignHarness) assign(t *testing.T, userID, unreachable int64) *service.RelayUserAssignment {
	t.Helper()
	res, err := u.runtime.AssignUser(context.Background(), userID, unreachable)
	require.NoError(t, err)
	return res
}

func mustGet(t *testing.T, h *userAssignHarness, userID int64) *master.UserAssignment {
	t.Helper()
	a, err := master.UserAssignmentStoreOf(h.runtime).Get(context.Background(), userID)
	require.NoError(t, err)
	return a
}

// 设计 10.1 / 10.8：新用户先按主节点分配比例决定给不给主节点，其余分到从节点并带中转票据（带用户当前的 token_version）。
func TestAssignUserPicksMasterByRatioAndOtherwiseARelayWithATicket(t *testing.T) {
	h := newUserAssignHarness(t, 10)

	h.rnd = 0.05
	res := h.assign(t, 1, 0)
	require.Equal(t, service.RelayRoleMaster, res.Role)
	require.Zero(t, res.NodeID)
	require.Empty(t, res.Ticket, "the master uses the existing login session")
	require.Positive(t, res.RefreshAfter)

	h.rnd = 0.5
	res = h.assign(t, 2, 0)
	require.Equal(t, service.RelayRoleRelay, res.Role)
	require.Contains(t, []int64{h.nodes[0].ID, h.nodes[1].ID}, res.NodeID)
	require.Contains(t, res.BaseURL, ".example.com")
	require.NotEmpty(t, res.Ticket)
	require.NotNil(t, res.TicketExpiresAt)
	ticket, err := h.runtime.VerifyTicket(res.Ticket, res.NodeID)
	require.NoError(t, err)
	require.Equal(t, int64(2), ticket.GetUserId())
	require.Equal(t, res.NodeID, ticket.GetNodeId())
	require.Equal(t, service.ResolvedTokenVersion(h.users.users[2]), ticket.GetTokenVersion(), "the same value the login session carries")
	require.LessOrEqual(t, res.RefreshAfter, 300, "renewed well before the ticket expires")
	_, err = h.runtime.VerifyTicket(res.Ticket, res.NodeID+100)
	require.Error(t, err, "a ticket works only on the node it was issued for")

	// 同一用户再问：留在原节点（多台设备都分到同一台）。
	h.rnd = 0.01
	again := h.assign(t, 2, 0)
	require.Equal(t, res.NodeID, again.NodeID)
	again = h.assign(t, 1, 0)
	require.Zero(t, again.NodeID, "the user on the master stays there while the ratio is above 0")
}

// 设计 10.1：分到从节点的按剩余容量加权随机，最近几秒刚分出去的人数计入预估；超过负载阈值的不再分。
func TestAssignUserWeightsByRemainingCapacity(t *testing.T) {
	h := newUserAssignHarness(t, 0)
	a, b := h.nodes[0].ID, h.nodes[1].ID
	// a 的负载 60%（100 Mbps 的 60%），b 空闲：权重 100×0.4 : 100×1.0 = 40 : 100。
	h.setLoad(a, 7_500_000)
	h.setLoad(b, 0)

	h.rnd = 0.2 // 0.2 × 140 = 28 < 40：a
	require.Equal(t, a, h.assign(t, 1, 0).NodeID)
	h.rnd = 0.9
	require.Equal(t, b, h.assign(t, 2, 0).NodeID)

	// 超过负载阈值（85%）的节点不再分新用户。
	h.setLoad(a, 30_000_000) // 平均下来远超 85%
	h.rnd = 0.0
	require.Equal(t, b, h.assign(t, 3, 0).NodeID)
}

// 设计 10.5：原节点掉线后用户在下一次询问时分到别的从节点；一台从节点都没有时比例大于 0 临时分给主节点（fallback），
// 从节点恢复后下次询问分回去。
func TestAssignUserFailsOverAndFallsBackToTheMaster(t *testing.T) {
	h := newUserAssignHarness(t, 10)
	a, b := h.nodes[0].ID, h.nodes[1].ID
	h.rnd = 0.9
	first := h.assign(t, 1, 0)
	other := a
	if first.NodeID == a {
		other = b
	}

	h.advance(20*time.Second, other) // first 掉线，other 在线
	moved := h.assign(t, 1, 0)
	require.Equal(t, other, moved.NodeID, "moved to the remaining relay")
	require.NotEmpty(t, moved.Ticket)
	require.Equal(t, master.AssignReasonFailover, mustGet(t, h, 1).Reason)

	// 两台都掉线：比例 10% 临时分给主节点。
	h.advance(20 * time.Second)
	fallback := h.assign(t, 1, 0)
	require.Equal(t, service.RelayRoleMaster, fallback.Role)
	require.Equal(t, master.AssignReasonFallback, mustGet(t, h, 1).Reason)
	// 新用户同样。
	require.Equal(t, service.RelayRoleMaster, h.assign(t, 2, 0).Role)

	// 一台恢复：fallback 的用户下次询问分回从节点。
	h.beat(a, 0)
	back := h.assign(t, 1, 0)
	require.Equal(t, service.RelayRoleRelay, back.Role)
	require.Equal(t, a, back.NodeID)
}

// 比例为 0：没有可用从节点时服务中断（返回 ErrRelayUnavailable）；分到主节点的用户下一次询问时移到从节点。
func TestAssignUserWithRatioZero(t *testing.T) {
	ctx := context.Background()
	h := newUserAssignHarness(t, 0)
	h.advance(20 * time.Second) // 两台都掉线
	_, err := h.runtime.AssignUser(ctx, 1, 0)
	require.ErrorIs(t, err, service.ErrRelayUnavailable)

	// 从节点回来了。
	h.beat(h.nodes[0].ID, 0)
	h.beat(h.nodes[1].ID, 0)
	res := h.assign(t, 1, 0)
	require.Equal(t, service.RelayRoleRelay, res.Role)

	// 原来按比例分到主节点的用户：比例是 0，下一次询问移走。
	require.NoError(t, master.UserAssignmentStoreOf(h.runtime).Put(ctx, &master.UserAssignment{UserID: 2, NodeID: 0, Reason: master.AssignReasonNew}))
	moved := h.assign(t, 2, 0)
	require.Equal(t, service.RelayRoleRelay, moved.Role, "ratio 0: the master serves nobody")
}

// 设计 10.3：客户端报告连不上分配的节点，用户马上换到别的节点；同一台被足够多的用户报告，暂停分配并告警。
func TestAssignUserHandlesUnreachableReports(t *testing.T) {
	h := newUserAssignHarness(t, 0)
	a, b := h.nodes[0].ID, h.nodes[1].ID
	h.rnd = 0.0
	first := h.assign(t, 1, 0)
	bad := first.NodeID
	good := a
	if bad == a {
		good = b
	}
	again := h.assign(t, 1, bad)
	require.Equal(t, good, again.NodeID, "reported unreachable: moved elsewhere at once")

	// 5 个不同用户报告同一台：这台暂停分配，发告警。
	for user := int64(2); user <= 6; user++ {
		h.rnd = 0.0
		h.assign(t, user, 0)
		h.assign(t, user, bad)
	}
	require.Contains(t, h.notifier.kinds(), master.EventNodeUnreachableReports)
	for user := int64(7); user <= 12; user++ {
		h.rnd = 0.0
		require.Equal(t, good, h.assign(t, user, 0).NodeID, "the suppressed node gets no new users")
	}

	// 暂停 5 分钟后恢复。
	h.advance(6*time.Minute, a, b)
	got := map[int64]bool{}
	for i, user := 0, int64(13); user <= 20; user++ {
		h.rnd = float64(i) * 0.13
		i++
		got[h.assign(t, user, 0).NodeID] = true
	}
	require.True(t, got[bad], "assignable again")
}

// 设计 10.4：固定必须设到期时间；原节点可用时不动，原节点不可用时自动解除固定并通知管理员；重新平衡不动固定的。
func TestPinMoveAndRebalanceUsers(t *testing.T) {
	ctx := context.Background()
	h := newUserAssignHarness(t, 0)
	a, b := h.nodes[0].ID, h.nodes[1].ID
	now := h.clock.now()

	require.ErrorIs(t, h.runtime.PinUser(ctx, 1, 1, a, now.Add(-time.Hour)), master.ErrInvalidPin)
	require.ErrorIs(t, h.runtime.PinUser(ctx, 1, 1, a, now.Add(40*24*time.Hour)), master.ErrInvalidPin, "at most 30 days")
	require.ErrorIs(t, h.runtime.PinUser(ctx, 1, 1, 0, now.Add(time.Hour)), master.ErrMasterRatioZero)
	require.NoError(t, h.runtime.PinUser(ctx, 1, 1, a, now.Add(time.Hour)))

	// 固定后即使别的节点更空也不动。
	h.setLoad(a, 9_000_000)
	h.rnd = 0.99
	require.Equal(t, a, h.assign(t, 1, 0).NodeID)

	// 手动移动 / 重新平衡：固定的不动，其余下次重新分配。
	require.NoError(t, h.runtime.MoveUser(ctx, 1, 2, b))
	require.Equal(t, b, h.assign(t, 2, 0).NodeID)
	deleted, err := h.runtime.RebalanceUsers(ctx, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted, "only the unpinned row is dropped")
	require.Equal(t, a, mustGet(t, h, 1).NodeID)
	summary, err := h.runtime.UserAssignmentSummary(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, summary[a])

	// 固定的节点掉线：自动解除固定，用户分到别的节点，通知管理员。
	h.advance(20*time.Second, b)
	res := h.assign(t, 1, 0)
	require.Equal(t, b, res.NodeID)
	require.Contains(t, h.notifier.kinds(), master.EventUserPinReleased)
	require.Nil(t, mustGet(t, h, 1).PinnedUntil)

	require.NoError(t, h.runtime.PinUser(ctx, 1, 1, b, h.clock.now().Add(time.Hour)))
	require.NoError(t, h.runtime.UnpinUser(ctx, 1, 1))
	require.Nil(t, mustGet(t, h, 1).PinnedUntil)
	require.ErrorIs(t, h.runtime.UnpinUser(ctx, 1, 99), master.ErrAssignmentNotFound)
}

// 可用但压力都很高不算不可用（设计 10.5）：仍分给从节点（负载最低的），不分给主节点；停用的用户不分配。
func TestAssignUserKeepsOverloadedRelaysAndRejectsInactiveUsers(t *testing.T) {
	ctx := context.Background()
	h := newUserAssignHarness(t, 10)
	a, b := h.nodes[0].ID, h.nodes[1].ID
	h.setLoad(a, 12_000_000) // 96%
	h.setLoad(b, 11_500_000) // 92%
	h.rnd = 0.9
	res := h.assign(t, 1, 0)
	require.Equal(t, service.RelayRoleRelay, res.Role)
	require.Equal(t, b, res.NodeID, "the least loaded of the overloaded")

	h.users.users[3].Status = service.StatusDisabled
	_, err := h.runtime.AssignUser(ctx, 3, 0)
	require.ErrorIs(t, err, service.ErrUserNotActive)
	_, err = h.runtime.AssignUser(ctx, 999, 0)
	require.ErrorIs(t, err, service.ErrUserNotFound)
}

// 主节点转发上限（设计 10.5）：并发数超过 master_max_concurrent 时拿不到名额（回"服务繁忙"）；释放后又能拿。
func TestMasterForwardingCap(t *testing.T) {
	ctx := context.Background()
	h := newRuntimeWith(t, nil, nil)
	_, err := h.runtime.SetGeneralConfig(ctx, 1, master.GeneralConfig{MasterMaxConcurrent: 2})
	require.NoError(t, err)
	st, err := h.runtime.SetEnabled(ctx, 1, true)
	require.NoError(t, err)
	require.Equal(t, master.StateRunning, st.State, st.Reason)

	r1, ok := h.runtime.AcquireMasterSlot(ctx)
	require.True(t, ok)
	r2, ok := h.runtime.AcquireMasterSlot(ctx)
	require.True(t, ok)
	_, ok = h.runtime.AcquireMasterSlot(ctx)
	require.False(t, ok, "over the cap")
	r1()
	r1() // 重复释放无害
	r3, ok := h.runtime.AcquireMasterSlot(ctx)
	require.True(t, ok)
	r2()
	r3()
}

// 设计 13：所有从节点不可用时，进入"回退到主节点"、进入"服务中断"、恢复各通知一次（状态变化时，不是每次询问）；比例改动、
// 转发达到上限也通知；通知内容里的节点信息来自运行时。
func TestAvailabilityAndConfigEventsAreNotified(t *testing.T) {
	ctx := context.Background()
	h := newUserAssignHarness(t, 10)
	count := func(kind string) int {
		n := 0
		for _, k := range h.notifier.kinds() {
			if k == kind {
				n++
			}
		}
		return n
	}
	eventually := func(kind string, want int) {
		require.Eventually(t, func() bool { return count(kind) == want }, 3*time.Second, 10*time.Millisecond, kind)
	}

	h.rnd = 0.9
	h.assign(t, 1, 0)
	require.Zero(t, count(master.EventAllRelaysDownFallback), "relays are fine")

	h.advance(20 * time.Second) // 两台都掉线
	h.assign(t, 2, 0)
	h.assign(t, 3, 0)
	eventually(master.EventAllRelaysDownFallback, 1)

	// 比例改成 0：服务中断；改比例本身也通知一次。
	zero := 0
	_, err := h.runtime.SetGeneralConfig(ctx, 1, master.GeneralConfig{MasterRatioPercent: &zero})
	require.NoError(t, err)
	require.Equal(t, 1, count(master.EventMasterRatioChanged))
	_, err = h.runtime.SetGeneralConfig(ctx, 1, master.GeneralConfig{MasterRatioPercent: &zero})
	require.NoError(t, err)
	require.Equal(t, 1, count(master.EventMasterRatioChanged), "an unchanged ratio is not an event")

	// 通用配置缓存 5 秒：直接换成新运行时读到 0 太慢，这里用管理员把用户固定到主节点之外的方式验证"服务中断"的通知。
	h2 := newUserAssignHarness(t, 0)
	h2.advance(20 * time.Second)
	_, err = h2.runtime.AssignUser(ctx, 1, 0)
	require.ErrorIs(t, err, service.ErrRelayUnavailable)
	require.Eventually(t, func() bool {
		for _, k := range h2.notifier.kinds() {
			if k == master.EventAllRelaysDownOutage {
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)

	// 恢复：有从节点可用后通知一次。
	h.beat(h.nodes[0].ID, 0)
	h.assign(t, 2, 0)
	eventually(master.EventRelayRecovered, 1)
	h.assign(t, 2, 0)
	eventually(master.EventRelayRecovered, 1)

	// 通知内容里的节点信息。
	nc, ok := h.runtime.NodeContext(ctx, h.nodes[0].ID)
	require.True(t, ok)
	require.NotNil(t, nc.LastHeartbeat)
	_, ok = h.runtime.NodeContext(ctx, 999)
	require.False(t, ok)
}

// 主节点转发达到上限、开始拒绝请求：通知一次（每分钟最多一次，不在请求路径上等通知发完）。
func TestMasterCapReachedIsNotified(t *testing.T) {
	ctx := context.Background()
	n := &captureNotifier{}
	h := newRuntimeWith(t, nil, func(d *master.RuntimeDeps) { d.Notifier = n })
	_, err := h.runtime.SetGeneralConfig(ctx, 1, master.GeneralConfig{MasterMaxConcurrent: 1})
	require.NoError(t, err)
	st, err := h.runtime.SetEnabled(ctx, 1, true)
	require.NoError(t, err)
	require.Equal(t, master.StateRunning, st.State, st.Reason)

	release, ok := h.runtime.AcquireMasterSlot(ctx)
	require.True(t, ok)
	for i := 0; i < 5; i++ {
		_, ok = h.runtime.AcquireMasterSlot(ctx)
		require.False(t, ok)
	}
	require.Eventually(t, func() bool { return len(n.kinds()) >= 1 }, 3*time.Second, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	count := 0
	for _, k := range n.kinds() {
		if k == master.EventMasterCapReached {
			count++
		}
	}
	require.Equal(t, 1, count, "five rejections, one notification")
	release()
}
