package sign_test

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/keystore"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

type keys struct {
	store   *keystore.Store
	purpose keystore.Purpose
}

func newKeys(t *testing.T, purpose keystore.Purpose) *keys {
	t.Helper()
	kek := make([]byte, keystore.KEKLength)
	_, err := rand.Read(kek)
	require.NoError(t, err)
	s, err := keystore.Open(t.TempDir(), kek)
	require.NoError(t, err)
	_, err = s.EnsureActive(purpose)
	require.NoError(t, err)
	return &keys{store: s, purpose: purpose}
}

func (k *keys) signer(t *testing.T) *sign.Signer {
	t.Helper()
	ring, err := k.store.Ring(k.purpose)
	require.NoError(t, err)
	s, err := sign.NewSigner(ring.Active)
	require.NoError(t, err)
	return s
}

func (k *keys) public(t *testing.T) *sign.PublicKeys {
	t.Helper()
	ring, err := k.store.Ring(k.purpose)
	require.NoError(t, err)
	p, list, err := sign.PublicKeysFromRing(ring)
	require.NoError(t, err)
	// 从节点拿到的是下发的列表：走一遍列表构造，保证两条路径一致。
	fromList, err := sign.NewPublicKeys(list)
	require.NoError(t, err)
	require.ElementsMatch(t, p.Versions(), fromList.Versions())
	return fromList
}

func verifier(pub *sign.PublicKeys, nodeID int64, now time.Time, rev *sign.RevocationList) *sign.TicketVerifier {
	return &sign.TicketVerifier{
		Keys:        func() *sign.PublicKeys { return pub },
		NodeID:      func() int64 { return nodeID },
		Revocations: rev,
		Now:         func() time.Time { return now },
	}
}

// 改写票据里的签名信封（篡改测试用）。
func rewrite(t *testing.T, token string, fn func(*relayv1.SignedToken)) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, sign.TicketPrefix))
	require.NoError(t, err)
	var st relayv1.SignedToken
	require.NoError(t, proto.Unmarshal(raw, &st))
	fn(&st)
	out, err := proto.Marshal(&st)
	require.NoError(t, err)
	return sign.TicketPrefix + base64.RawURLEncoding.EncodeToString(out)
}

func TestTicketRoundTripAndRejections(t *testing.T) {
	k := newKeys(t, keystore.PurposeTicket)
	now := time.Now()
	token, issued, err := sign.IssueTicket(k.signer(t), 42, 7, 99, now)
	require.NoError(t, err)
	require.Less(t, len(token), 300)
	pub := k.public(t)

	got, err := verifier(pub, 7, now, nil).Verify(token)
	require.NoError(t, err)
	require.True(t, proto.Equal(issued, got))
	require.Equal(t, int64(99), got.TokenVersion)

	// 过期：有效期 + 时钟偏差之内仍可用，之后拒绝。
	_, err = verifier(pub, 7, now.Add(sign.TicketLifetime+sign.ClockSkew-time.Second), nil).Verify(token)
	require.NoError(t, err)
	_, err = verifier(pub, 7, now.Add(sign.TicketLifetime+sign.ClockSkew), nil).Verify(token)
	require.ErrorIs(t, err, sign.ErrExpired)
	// 签发时间在未来（超出偏差）。
	_, err = verifier(pub, 7, now.Add(-sign.ClockSkew-time.Second), nil).Verify(token)
	require.ErrorIs(t, err, sign.ErrNotYetValid)
	// 换节点使用。
	_, err = verifier(pub, 8, now, nil).Verify(token)
	require.ErrorIs(t, err, sign.ErrWrongNode)

	// 篡改 payload（改用户 ID）。
	tampered := rewrite(t, token, func(st *relayv1.SignedToken) {
		var tk relayv1.Ticket
		require.NoError(t, proto.Unmarshal(st.Payload, &tk))
		tk.UserId = 1
		st.Payload, _ = proto.Marshal(&tk)
	})
	_, err = verifier(pub, 7, now, nil).Verify(tampered)
	require.ErrorIs(t, err, sign.ErrBadSignature)
	// 未知密钥版本。
	_, err = verifier(pub, 7, now, nil).Verify(rewrite(t, token, func(st *relayv1.SignedToken) { st.KeyVersion = 99 }))
	require.ErrorIs(t, err, sign.ErrUnknownKey)
	// 伪造：别的私钥签的（版本号相同）。
	forger := newKeys(t, keystore.PurposeTicket)
	forged, _, err := sign.IssueTicket(forger.signer(t), 42, 7, 99, now)
	require.NoError(t, err)
	_, err = verifier(pub, 7, now, nil).Verify(forged)
	require.ErrorIs(t, err, sign.ErrBadSignature)
	// 格式。
	for _, bad := range []string{"", "Bearer x", sign.TicketPrefix + "!!!", sign.TicketPrefix + strings.Repeat("A", 2000), token[:len(token)-10]} {
		_, err = verifier(pub, 7, now, nil).Verify(bad)
		require.Error(t, err, bad)
	}
	// 没有公钥（还没拿到配置）时拒绝。
	_, err = verifier(nil, 7, now, nil).Verify(token)
	require.ErrorIs(t, err, sign.ErrUnknownKey)
}

// 票据和凭证用不同的用途串：同一把密钥签的票据当凭证验、凭证当票据验都不行。
func TestTicketAndVoucherCannotStandInForEachOther(t *testing.T) {
	k := newKeys(t, keystore.PurposeTicket)
	now := time.Now()
	s := k.signer(t)
	pub := k.public(t)

	token, _, err := sign.IssueTicket(s, 42, 7, 1, now)
	require.NoError(t, err)
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, sign.TicketPrefix))
	require.NoError(t, err)
	_, err = sign.VerifyVoucher(raw, pub, now)
	require.ErrorIs(t, err, sign.ErrBadSignature)

	voucher, _, err := sign.IssueVoucher(s, &relayv1.Voucher{NodeId: 7, UserId: 42}, now)
	require.NoError(t, err)
	_, err = verifier(pub, 7, now, nil).Verify(sign.TicketPrefix + base64.RawURLEncoding.EncodeToString(voucher))
	require.ErrorIs(t, err, sign.ErrBadSignature)
}

func TestVoucherRoundTripAndRejections(t *testing.T) {
	k := newKeys(t, keystore.PurposeVoucher)
	now := time.Now()
	in := &relayv1.Voucher{
		NodeId: 7, UserId: 42, ApiKeyId: 3, AccountId: 11, GroupId: 5,
		BillingMode: relayv1.BillingMode_BILLING_MODE_SUBSCRIPTION, RequestedModel: "gpt-5",
		AllowedBillingModels: []string{"gpt-5", "gpt-5-2026-08-01"}, SelectionId: "sel-1",
		Context: &relayv1.SelectionContext{PricingAtUnixMs: now.UnixMilli()},
	}
	raw, issued, err := sign.IssueVoucher(k.signer(t), in, now)
	require.NoError(t, err)
	require.Len(t, issued.VoucherId, 16)
	require.Equal(t, byte(0x40), issued.VoucherId[6]&0xf0, "UUID v4")
	require.Empty(t, in.VoucherId, "the caller's message is not modified")
	pub := k.public(t)

	got, err := sign.VerifyVoucher(raw, pub, now.Add(sign.VoucherMaxAge-time.Second))
	require.NoError(t, err)
	require.True(t, proto.Equal(issued, got))
	_, err = sign.VerifyVoucher(raw, pub, now.Add(sign.VoucherMaxAge+time.Minute))
	require.ErrorIs(t, err, sign.ErrExpired, "older than 60 days is treated as lost")
	_, err = sign.VerifyVoucher(raw, pub, now.Add(-sign.ClockSkew-time.Second))
	require.ErrorIs(t, err, sign.ErrNotYetValid)

	var st relayv1.SignedToken
	require.NoError(t, proto.Unmarshal(raw, &st))
	st.Payload = append(append([]byte(nil), st.Payload...), 0x08, 0x01) // 追加字段也算篡改
	tampered, err := proto.Marshal(&st)
	require.NoError(t, err)
	_, err = sign.VerifyVoucher(tampered, pub, now)
	require.ErrorIs(t, err, sign.ErrBadSignature)

	second, _, err := sign.IssueVoucher(k.signer(t), in, now)
	require.NoError(t, err)
	a, _ := sign.VerifyVoucher(raw, pub, now)
	b, _ := sign.VerifyVoucher(second, pub, now)
	require.NotEqual(t, a.VoucherId, b.VoucherId, "every voucher gets its own id")

	_, err = sign.VerifyVoucher(make([]byte, 20<<10), pub, now)
	require.ErrorIs(t, err, sign.ErrMalformed)
}

// 轮换：新旧公钥并存期间两种签名都能验；旧版本停用后，它签的被拒。
func TestKeyRotationOverlapAndRetirement(t *testing.T) {
	k := newKeys(t, keystore.PurposeTicket)
	now := time.Now()
	oldToken, _, err := sign.IssueTicket(k.signer(t), 42, 7, 1, now)
	require.NoError(t, err)
	oldRing, err := k.store.Ring(keystore.PurposeTicket)
	require.NoError(t, err)

	staged, err := k.store.Stage(keystore.PurposeTicket)
	require.NoError(t, err)
	require.Equal(t, uint32(oldRing.Active.Version), k.signer(t).Version(), "a staged key does not sign yet")
	require.NoError(t, k.store.Activate(keystore.PurposeTicket, staged.Version))
	newToken, _, err := sign.IssueTicket(k.signer(t), 42, 7, 1, now)
	require.NoError(t, err)

	pub := k.public(t)
	for _, tk := range []string{oldToken, newToken} {
		_, err = verifier(pub, 7, now, nil).Verify(tk)
		require.NoError(t, err)
	}
	require.NoError(t, k.store.Retire(keystore.PurposeTicket, oldRing.Active.Version))
	pub = k.public(t)
	_, err = verifier(pub, 7, now, nil).Verify(oldToken)
	require.ErrorIs(t, err, sign.ErrUnknownKey)
	_, err = verifier(pub, 7, now, nil).Verify(newToken)
	require.NoError(t, err)
}

func TestRevocationList(t *testing.T) {
	k := newKeys(t, keystore.PurposeTicket)
	now := time.Now()
	rev := sign.NewRevocationList()
	token, issued, err := sign.IssueTicket(k.signer(t), 42, 7, 1, now)
	require.NoError(t, err)
	pub := k.public(t)

	// 恰好在吊销时刻签发的算吊销；之后签发的不受影响。
	rev.Revoke(42, time.UnixMilli(issued.IssuedAtUnixMs), now)
	_, err = verifier(pub, 7, now, rev).Verify(token)
	require.ErrorIs(t, err, sign.ErrRevoked)
	later, _, err := sign.IssueTicket(k.signer(t), 42, 7, 1, now.Add(time.Millisecond))
	require.NoError(t, err)
	_, err = verifier(pub, 7, now, rev).Verify(later)
	require.NoError(t, err)
	other, _, err := sign.IssueTicket(k.signer(t), 43, 7, 1, now)
	require.NoError(t, err)
	_, err = verifier(pub, 7, now, rev).Verify(other)
	require.NoError(t, err, "other users are not affected")

	// 吊销时间只往后移。
	rev.Revoke(42, now.Add(-time.Hour), now)
	require.True(t, rev.Revoked(42, time.UnixMilli(issued.IssuedAtUnixMs), now))

	// 整份快照 → 另一张表（主节点连上时发给节点）。
	copyRev := sign.NewRevocationList()
	copyRev.Apply(rev.Snapshot(now), now)
	require.True(t, copyRev.Revoked(42, time.UnixMilli(issued.IssuedAtUnixMs), now))

	// 过了票据有效期 + 偏差，条目清掉（被它挡住的票据也都过期了）。
	gone := now.Add(sign.TicketLifetime + sign.ClockSkew + time.Second)
	require.False(t, rev.Revoked(42, time.UnixMilli(issued.IssuedAtUnixMs), gone))
	require.Empty(t, rev.Snapshot(gone).Users)

	copyRev.Reset()
	require.Equal(t, 0, copyRev.Len())
	var nilList *sign.RevocationList
	require.False(t, nilList.Revoked(42, now, now))
}
