package sign

import (
	"crypto/rand"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"google.golang.org/protobuf/proto"
)

// maxVoucherSize：凭证带报价和选号上下文，几百字节到几 KB；超过直接拒绝。
const maxVoucherSize = 16 << 10

// IssueVoucher 签发一张扣费凭证（主节点选号时，设计 5.3）。voucher_id（UUID v4）和
// 签发时间由这里填，调用方填其余字段。返回签名信封的编码（从节点原样带回）和凭证内容。
func IssueVoucher(s *Signer, v *relayv1.Voucher, now time.Time) ([]byte, *relayv1.Voucher, error) {
	if v == nil {
		return nil, nil, ErrMalformed
	}
	v, _ = proto.Clone(v).(*relayv1.Voucher)
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, nil, err
	}
	id[6] = (id[6] & 0x0f) | 0x40 // UUID v4
	id[8] = (id[8] & 0x3f) | 0x80 // RFC 4122 变体
	v.VoucherId = id
	v.IssuedAtUnixMs = now.UnixMilli()
	payload, err := proto.Marshal(v)
	if err != nil {
		return nil, nil, err
	}
	signed, err := s.sign(contextVoucher, payload)
	if err != nil {
		return nil, nil, err
	}
	raw, err := proto.Marshal(signed)
	if err != nil {
		return nil, nil, err
	}
	return raw, v, nil
}

// VerifyVoucher 验一张凭证（主节点入账时）：签名、签发时间不在未来、签发未超过 60 天、
// 上报的节点就是凭证签给的节点（reportingNodeID，来自主从连接的证书）。
// 是否已入账、实际模型是否在允许范围内由入账（WP8）检查。
func VerifyVoucher(raw []byte, keys *PublicKeys, reportingNodeID int64, now time.Time) (*relayv1.Voucher, error) {
	if len(raw) == 0 || len(raw) > maxVoucherSize {
		return nil, ErrMalformed
	}
	var signed relayv1.SignedToken
	if err := proto.Unmarshal(raw, &signed); err != nil {
		return nil, ErrMalformed
	}
	if err := keys.verify(contextVoucher, &signed); err != nil {
		return nil, err
	}
	var v relayv1.Voucher
	if err := proto.Unmarshal(signed.Payload, &v); err != nil {
		return nil, ErrMalformed
	}
	if len(v.VoucherId) != 16 || v.IssuedAtUnixMs <= 0 || v.NodeId <= 0 || v.UserId <= 0 {
		return nil, ErrMalformed
	}
	if v.IssuedAtUnixMs > now.Add(ClockSkew).UnixMilli() {
		return nil, ErrNotYetValid
	}
	if now.Sub(time.UnixMilli(v.IssuedAtUnixMs)) > VoucherMaxAge {
		return nil, ErrExpired
	}
	if v.NodeId != reportingNodeID {
		return nil, ErrWrongNode
	}
	return &v, nil
}
