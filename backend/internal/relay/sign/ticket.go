package sign

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"google.golang.org/protobuf/proto"
)

// TicketPrefix 是票据字符串的前缀，便于和 JWT、API Key 区分（小白端放在 Authorization: Bearer 里）。
const TicketPrefix = "srt1."

// maxTicketLength：正常票据约 150 字节，超过这个长度直接拒绝，不做解码。
const maxTicketLength = 1024

// IssueTicket 签发一张中转票据（主节点，设计 8.1）。
func IssueTicket(s *Signer, userID, nodeID, tokenVersion int64, now time.Time) (string, *relayv1.Ticket, error) {
	t := &relayv1.Ticket{
		UserId:          userID,
		NodeId:          nodeID,
		TokenVersion:    tokenVersion,
		IssuedAtUnixMs:  now.UnixMilli(),
		ExpiresAtUnixMs: now.Add(TicketLifetime).UnixMilli(),
	}
	payload, err := proto.Marshal(t)
	if err != nil {
		return "", nil, err
	}
	signed, err := s.sign(contextTicket, payload)
	if err != nil {
		return "", nil, err
	}
	raw, err := proto.Marshal(signed)
	if err != nil {
		return "", nil, err
	}
	return TicketPrefix + base64.RawURLEncoding.EncodeToString(raw), t, nil
}

// TicketVerifier 在从节点验票据。只在请求开始时验：进行中的长回复不因票据到期中断（设计 8.1）。
//
// 签名、有效期、节点、吊销在这里查；用户状态和 token_version 以主节点选号时的复查为准。
type TicketVerifier struct {
	// Keys 返回当前的票据公钥（来自配置快照）。
	Keys func() *PublicKeys
	// NodeID 返回本机节点 ID。
	NodeID func() int64
	// Revocations 是吊销表；nil 表示不查（测试）。
	Revocations *RevocationList
	Now         func() time.Time
}

// Verify 验一张票据，通过时返回票据内容。
func (v *TicketVerifier) Verify(token string) (*relayv1.Ticket, error) {
	if len(token) > maxTicketLength || !strings.HasPrefix(token, TicketPrefix) {
		return nil, ErrMalformed
	}
	raw, err := base64.RawURLEncoding.DecodeString(token[len(TicketPrefix):])
	if err != nil {
		return nil, ErrMalformed
	}
	var signed relayv1.SignedToken
	if err := proto.Unmarshal(raw, &signed); err != nil {
		return nil, ErrMalformed
	}
	var keys *PublicKeys
	if v.Keys != nil {
		keys = v.Keys()
	}
	if err := keys.verify(contextTicket, &signed); err != nil {
		return nil, err
	}
	var t relayv1.Ticket
	if err := proto.Unmarshal(signed.Payload, &t); err != nil {
		return nil, ErrMalformed
	}
	if t.UserId <= 0 || t.NodeId <= 0 || t.ExpiresAtUnixMs <= t.IssuedAtUnixMs ||
		t.ExpiresAtUnixMs-t.IssuedAtUnixMs > TicketLifetime.Milliseconds() {
		return nil, ErrMalformed
	}

	now := time.Now()
	if v.Now != nil {
		now = v.Now()
	}
	if now.Add(-ClockSkew).UnixMilli() >= t.ExpiresAtUnixMs {
		return nil, ErrExpired
	}
	if t.IssuedAtUnixMs > now.Add(ClockSkew).UnixMilli() {
		return nil, ErrNotYetValid
	}
	if v.NodeID == nil || t.NodeId != v.NodeID() {
		return nil, ErrWrongNode
	}
	if v.Revocations.Revoked(t.UserId, time.UnixMilli(t.IssuedAtUnixMs), now) {
		return nil, ErrRevoked
	}
	return &t, nil
}
