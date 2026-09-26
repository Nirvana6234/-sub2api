// Package sign 是中转票据和扣费凭证的签名与验证（设计 5.3、8.1，开发计划 WP5）。
//
// 主节点持有签名私钥（keystore 的 ticket、voucher 两种用途），从节点只有验票据用的公钥，
// 能验不能签。扣费凭证只在主节点入账时验，公钥不下发。
//
// 签名信封见 relayv1.SignedToken：按收到的原始 payload 字节验签，从不重新编码；
// 签名覆盖"用途串 || 0x00 || payload"，票据和凭证用不同的用途串，一种不能冒充另一种。
package sign

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/keystore"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
)

const (
	contextTicket  = "sub2api/relay/ticket/v1"
	contextVoucher = "sub2api/relay/voucher/v1"

	// TicketLifetime 是中转票据有效期（设计第 19 节）。
	TicketLifetime = 10 * time.Minute
	// ClockSkew 是主从时钟允许的偏差：超过它从节点拒绝服务（设计第 19 节），
	// 有效期检查按它放宽。
	ClockSkew = 30 * time.Second
	// VoucherMaxAge：签发超过 60 天的凭证不再入账，按丢失处理（设计 5.4）。
	VoucherMaxAge = 60 * 24 * time.Hour
)

var (
	// ErrMalformed：格式不对（编码、长度、字段缺失）。
	ErrMalformed = errors.New("relay token is malformed")
	// ErrUnknownKey：签名密钥版本未知或已停用。
	ErrUnknownKey = errors.New("relay token is signed by an unknown or retired key")
	// ErrBadSignature：签名不对（伪造或篡改）。
	ErrBadSignature = errors.New("relay token signature is invalid")
	// ErrExpired：已过期（票据）或超过入账期限（凭证）。
	ErrExpired = errors.New("relay token has expired")
	// ErrNotYetValid：签发时间在未来（超出允许的时钟偏差）。
	ErrNotYetValid = errors.New("relay token is not valid yet")
	// ErrWrongNode：不是签给这台从节点的。
	ErrWrongNode = errors.New("relay token was issued for another node")
	// ErrRevoked：票据已被吊销。
	ErrRevoked = errors.New("relay ticket has been revoked")
)

// Signer 用某个版本的私钥签名。
type Signer struct {
	version uint32
	key     crypto.Signer
}

// NewSigner 用 keystore 里签发用的密钥（Ed25519）创建签名器。
func NewSigner(k *keystore.Key) (*Signer, error) {
	if k == nil || k.Signer == nil {
		return nil, errors.New("sign: no signing key")
	}
	if _, ok := k.Signer.Public().(ed25519.PublicKey); !ok {
		return nil, fmt.Errorf("sign: %s v%d is not an Ed25519 key", k.Purpose, k.Version)
	}
	if k.Version <= 0 {
		return nil, fmt.Errorf("sign: invalid key version %d", k.Version)
	}
	return &Signer{version: uint32(k.Version), key: k.Signer}, nil
}

// Version 返回签名密钥版本。
func (s *Signer) Version() uint32 { return s.version }

func (s *Signer) sign(purpose string, payload []byte) (*relayv1.SignedToken, error) {
	sig, err := s.key.Sign(rand.Reader, signedMessage(purpose, payload), crypto.Hash(0))
	if err != nil {
		return nil, err
	}
	return &relayv1.SignedToken{KeyVersion: s.version, Payload: payload, Signature: sig}, nil
}

func signedMessage(purpose string, payload []byte) []byte {
	msg := make([]byte, 0, len(purpose)+1+len(payload))
	msg = append(msg, purpose...)
	msg = append(msg, 0)
	return append(msg, payload...)
}

// PublicKeys 是验签用的公钥集合（按版本），创建后不再改动，可以并发使用。
type PublicKeys struct {
	keys map[uint32]ed25519.PublicKey
}

// NewPublicKeys 从下发的公钥列表创建集合（从节点从配置快照取）。
func NewPublicKeys(list []*relayv1.SigningPublicKey) (*PublicKeys, error) {
	p := &PublicKeys{keys: make(map[uint32]ed25519.PublicKey, len(list))}
	for _, k := range list {
		if k == nil || k.Version == 0 || len(k.PublicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("sign: invalid public key entry")
		}
		p.keys[k.Version] = append(ed25519.PublicKey(nil), k.PublicKey...)
	}
	return p, nil
}

// PublicKeysFromRing 从 keystore 的一个用途（全部未停用的版本，含预备中的）创建集合，
// 同时返回下发用的列表。
func PublicKeysFromRing(ring *keystore.Ring) (*PublicKeys, []*relayv1.SigningPublicKey, error) {
	list := make([]*relayv1.SigningPublicKey, 0, len(ring.Keys))
	for _, k := range ring.Keys {
		pub, ok := k.Signer.Public().(ed25519.PublicKey)
		if !ok {
			return nil, nil, fmt.Errorf("sign: %s v%d is not an Ed25519 key", k.Purpose, k.Version)
		}
		list = append(list, &relayv1.SigningPublicKey{Version: uint32(k.Version), PublicKey: append([]byte(nil), pub...)})
	}
	p, err := NewPublicKeys(list)
	if err != nil {
		return nil, nil, err
	}
	return p, list, nil
}

// Versions 返回集合里的版本号（诊断用）。
func (p *PublicKeys) Versions() []uint32 {
	out := make([]uint32, 0, len(p.keys))
	for v := range p.keys {
		out = append(out, v)
	}
	return out
}

func (p *PublicKeys) verify(purpose string, t *relayv1.SignedToken) error {
	if t == nil || len(t.Payload) == 0 || len(t.Signature) != ed25519.SignatureSize {
		return ErrMalformed
	}
	if p == nil {
		return ErrUnknownKey
	}
	pub, ok := p.keys[t.KeyVersion]
	if !ok {
		return ErrUnknownKey
	}
	if !ed25519.Verify(pub, signedMessage(purpose, t.Payload), t.Signature) {
		return ErrBadSignature
	}
	return nil
}
