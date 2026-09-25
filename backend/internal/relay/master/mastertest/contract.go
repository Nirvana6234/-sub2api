// Package mastertest 是 master.NodeStore 的契约测试：内存实现和 SQL 实现
// （internal/repository）跑同一套用例，保证两者语义一致。只给测试用。
package mastertest

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/stretchr/testify/require"
)

// Harness 由各实现提供。
type Harness struct {
	// New 返回一个空的存储。
	New func(t *testing.T) master.NodeStore
	// Age 把节点的注册时间改到 at（测试待激活超时用）。
	Age func(t *testing.T, s master.NodeStore, nodeID int64, at time.Time)
}

var seq int

func newNode(prefix string) *master.Node {
	seq++
	fp := prefix + strconv.Itoa(seq)
	for len(fp) < 64 {
		fp += "0"
	}
	return &master.Node{
		Hostname:            "host-" + strconv.Itoa(seq),
		Name:                "node",
		IdentityPublicKey:   []byte{1, 2, 3},
		IdentityFingerprint: fp,
		RegisteredIP:        "10.0.0.1",
		ProgramVersion:      "v1",
		SystemInfo:          map[string]string{"os": "linux"},
	}
}

// Run 跑全部用例。
func Run(t *testing.T, h Harness) {
	ctx := context.Background()

	t.Run("create pending is idempotent per key and capped", func(t *testing.T) {
		s := h.New(t)
		a, err := s.CreatePending(ctx, newNode("a"), 2)
		require.NoError(t, err)
		require.Positive(t, a.ID)
		require.Equal(t, master.NodePending, a.Status)
		require.Equal(t, []byte{1, 2, 3}, a.IdentityPublicKey)
		require.Equal(t, "linux", a.SystemInfo["os"])

		again := *newNode("x")
		again.IdentityFingerprint = a.IdentityFingerprint
		same, err := s.CreatePending(ctx, &again, 2)
		require.NoError(t, err)
		require.Equal(t, a.ID, same.ID, "the same key registers once")

		_, err = s.CreatePending(ctx, newNode("b"), 2)
		require.NoError(t, err)
		_, err = s.CreatePending(ctx, newNode("c"), 2)
		require.ErrorIs(t, err, master.ErrPendingLimit)
	})

	t.Run("lookups report not found", func(t *testing.T) {
		s := h.New(t)
		_, err := s.GetByID(ctx, 987654)
		require.ErrorIs(t, err, master.ErrNodeNotFound)
		_, err = s.GetByFingerprint(ctx, "missing")
		require.ErrorIs(t, err, master.ErrNodeNotFound)
		_, err = s.SetStatus(ctx, 987654, []master.NodeStatus{master.NodePending}, master.NodeActive)
		require.ErrorIs(t, err, master.ErrNodeNotFound)
	})

	t.Run("status changes are conditional", func(t *testing.T) {
		s := h.New(t)
		n, err := s.CreatePending(ctx, newNode("s"), 20)
		require.NoError(t, err)
		ok, err := s.SetStatus(ctx, n.ID, []master.NodeStatus{master.NodeActive}, master.NodeDisabled)
		require.NoError(t, err)
		require.False(t, ok, "not active, nothing changes")
		ok, err = s.SetStatus(ctx, n.ID, []master.NodeStatus{master.NodeActive, master.NodePending}, master.NodeRejected)
		require.NoError(t, err)
		require.True(t, ok)
		got, err := s.GetByID(ctx, n.ID)
		require.NoError(t, err)
		require.Equal(t, master.NodeRejected, got.Status)
	})

	t.Run("activate", func(t *testing.T) {
		s := h.New(t)
		a, err := s.CreatePending(ctx, newNode("act"), 20)
		require.NoError(t, err)
		b, err := s.CreatePending(ctx, newNode("act"), 20)
		require.NoError(t, err)
		at := time.Now().UTC().Truncate(time.Millisecond)
		require.NoError(t, s.Activate(ctx, a.ID, master.Activation{
			Name: "tokyo-1", PublicDomain: "relay1.example.com", BandwidthLimitMbps: 500, Region: "jp", ActorUserID: 7, At: at,
		}))
		got, err := s.GetByID(ctx, a.ID)
		require.NoError(t, err)
		require.Equal(t, master.NodeActive, got.Status)
		require.Equal(t, "tokyo-1", got.Name)
		require.Equal(t, "relay1.example.com", got.PublicDomain)
		require.Equal(t, 500, got.BandwidthLimitMbps)
		require.Equal(t, "jp", got.Region)
		require.NotNil(t, got.ActivatedAt)
		require.EqualValues(t, 7, *got.ActivatedBy)

		require.ErrorIs(t, s.Activate(ctx, a.ID, master.Activation{PublicDomain: "other.example.com"}), master.ErrStatusConflict)
		require.ErrorIs(t, s.Activate(ctx, b.ID, master.Activation{PublicDomain: "RELAY1.example.com"}), master.ErrDomainTaken)
	})

	t.Run("registration, keys and multi-ip updates", func(t *testing.T) {
		s := h.New(t)
		n, err := s.CreatePending(ctx, newNode("u"), 20)
		require.NoError(t, err)
		require.NoError(t, s.UpdateRegistration(ctx, n.ID, "new-host", "v2", "display", map[string]string{"cpu": "4"}, "10.0.0.9"))
		require.NoError(t, s.SetEncryptionKey(ctx, n.ID, []byte{9, 9}))
		require.NoError(t, s.SetAllowMultiIP(ctx, n.ID, true))
		got, err := s.GetByID(ctx, n.ID)
		require.NoError(t, err)
		require.Equal(t, "new-host", got.Hostname)
		require.Equal(t, "v2", got.ProgramVersion)
		require.Equal(t, "display", got.Name)
		require.Equal(t, "4", got.SystemInfo["cpu"])
		require.Equal(t, "10.0.0.9", got.RegisteredIP)
		require.Equal(t, []byte{9, 9}, got.EncryptionPublicKey)
		require.True(t, got.AllowMultiIP)
	})

	t.Run("touch seen reports ip changes", func(t *testing.T) {
		s := h.New(t)
		n, err := s.CreatePending(ctx, newNode("t"), 20)
		require.NoError(t, err)
		changed, err := s.TouchSeen(ctx, n.ID, "1.1.1.1", time.Now())
		require.NoError(t, err)
		require.False(t, changed, "first sighting is not a change")
		changed, err = s.TouchSeen(ctx, n.ID, "1.1.1.1", time.Now())
		require.NoError(t, err)
		require.False(t, changed)
		changed, err = s.TouchSeen(ctx, n.ID, "2.2.2.2", time.Now())
		require.NoError(t, err)
		require.True(t, changed)
		_, err = s.TouchSeen(ctx, 987654, "1.1.1.1", time.Now())
		require.ErrorIs(t, err, master.ErrNodeNotFound)
	})

	t.Run("certificates", func(t *testing.T) {
		s := h.New(t)
		n, err := s.CreatePending(ctx, newNode("c"), 20)
		require.NoError(t, err)
		now := time.Now().UTC().Truncate(time.Millisecond)
		first := &master.Certificate{NodeID: n.ID, Serial: "s1-" + n.IdentityFingerprint[:8], PublicKey: []byte{1}, DER: []byte{0xde, 0xad}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour)}
		require.NoError(t, s.InsertCertificate(ctx, first))
		renewed := &master.Certificate{NodeID: n.ID, Serial: "s2-" + n.IdentityFingerprint[:8], PublicKey: []byte{2}, NotBefore: now, NotAfter: now.Add(2 * time.Hour), RenewedFromSerial: first.Serial}
		require.NoError(t, s.InsertCertificate(ctx, renewed))
		again := &master.Certificate{NodeID: n.ID, Serial: "s3-" + n.IdentityFingerprint[:8], PublicKey: []byte{3}, NotBefore: now, NotAfter: now.Add(2 * time.Hour), RenewedFromSerial: first.Serial}
		require.ErrorIs(t, s.InsertCertificate(ctx, again), master.ErrDuplicateRenewal, "one certificate can be renewed only once")

		expired := &master.Certificate{NodeID: n.ID, Serial: "s0-" + n.IdentityFingerprint[:8], PublicKey: []byte{0}, NotBefore: now.Add(-3 * time.Hour), NotAfter: now.Add(-2 * time.Hour)}
		require.NoError(t, s.InsertCertificate(ctx, expired))

		renewal, err := s.GetRenewalOf(ctx, first.Serial)
		require.NoError(t, err)
		require.Equal(t, renewed.Serial, renewal.Serial)
		_, err = s.GetRenewalOf(ctx, renewed.Serial)
		require.ErrorIs(t, err, master.ErrNodeNotFound, "the newest certificate has not been renewed")
		superseded, err := s.ListRenewedSerials(ctx, now)
		require.NoError(t, err)
		require.Contains(t, superseded, first.Serial)
		require.NotContains(t, superseded, renewed.Serial)

		count, err := s.CountCertificates(ctx, n.ID)
		require.NoError(t, err)
		require.Equal(t, 3, count)
		got, err := s.GetCertificate(ctx, first.Serial)
		require.NoError(t, err)
		require.Equal(t, []byte{1}, got.PublicKey)
		require.Equal(t, []byte{0xde, 0xad}, got.DER)
		require.Nil(t, got.RevokedAt)

		revoked, err := s.RevokeCertificates(ctx, n.ID, "test", now)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{first.Serial, renewed.Serial}, revoked, "expired certificates need no revocation")
		list, err := s.ListRevokedSerials(ctx, now)
		require.NoError(t, err)
		require.Subset(t, list, []string{first.Serial, renewed.Serial})
		got, err = s.GetCertificate(ctx, first.Serial)
		require.NoError(t, err)
		require.NotNil(t, got.RevokedAt)
		require.Equal(t, "test", got.RevokeReason)
	})

	t.Run("stale pending registrations are purged", func(t *testing.T) {
		s := h.New(t)
		stale, err := s.CreatePending(ctx, newNode("p"), 20)
		require.NoError(t, err)
		fresh, err := s.CreatePending(ctx, newNode("p"), 20)
		require.NoError(t, err)
		h.Age(t, s, stale.ID, time.Now().Add(-25*time.Hour))
		n, err := s.PurgeStalePending(ctx, time.Now().Add(-24*time.Hour))
		require.NoError(t, err)
		require.EqualValues(t, 1, n)
		_, err = s.GetByID(ctx, stale.ID)
		require.ErrorIs(t, err, master.ErrNodeNotFound)
		_, err = s.GetByID(ctx, fresh.ID)
		require.NoError(t, err)

		// 被清除的密钥可以重新注册。
		back := newNode("x")
		back.IdentityFingerprint = stale.IdentityFingerprint
		again, err := s.CreatePending(ctx, back, 20)
		require.NoError(t, err)
		require.NotEqual(t, stale.ID, again.ID)
	})

	t.Run("audit", func(t *testing.T) {
		s := h.New(t)
		require.NoError(t, s.Audit(ctx, master.AuditEntry{Action: "rejected_all_pending"}))
		n, err := s.CreatePending(ctx, newNode("au"), 20)
		require.NoError(t, err)
		require.NoError(t, s.Audit(ctx, master.AuditEntry{NodeID: n.ID, ActorUserID: 3, Action: "activated", SourceIP: "1.2.3.4", Detail: map[string]any{"k": "v"}}))
	})
}
