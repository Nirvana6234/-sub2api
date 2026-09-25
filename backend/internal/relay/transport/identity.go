// Package transport 是主从节点之间的通信层（docs/MASTER_RELAY_NODES.md 第 7 节，
// 开发计划 2.3、WP2）：TLS 1.3 双向认证、按优先级分开的连接、纪元、幂等、
// 过载回复与协议版本。业务消息在 internal/relay/proto/relayv1 里定义。
//
// 本包不依赖 internal/service、internal/repository，从节点装配守卫（WP9）
// 因此可以直接复用它。
package transport

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MasterServerName 是主节点主从通信证书里的 DNS SAN，也是从节点握手时的 SNI。
// 它和主节点网站域名无关：网站证书几个月一换，主从通信只认专用根证书（设计 7.2）。
// .invalid 是保留顶级域，不会被真实解析。
const MasterServerName = "master.relay.sub2api.invalid"

// nodeURIPrefix 是主节点签发给从节点的证书里 URI SAN 的前缀，后接节点 ID。
const nodeURIPrefix = "urn:sub2api:relay-node:"

// PeerClass 是主节点对连接对端的分类（设计 7.2、11.1、11.2）。
type PeerClass int

const (
	// PeerAnonymous 没有出示证书，只能注册。
	PeerAnonymous PeerClass = iota + 1
	// PeerLongTerm 出示的是从节点用长期密钥自签的证书：待激活节点的心跳、
	// 证书过期后的恢复。它只证明"持有这把长期密钥"。
	PeerLongTerm
	// PeerIssued 出示的是主节点签发的主从通信证书：已激活节点的正常通信。
	PeerIssued
)

func (c PeerClass) String() string {
	switch c {
	case PeerAnonymous:
		return "anonymous"
	case PeerLongTerm:
		return "long_term"
	case PeerIssued:
		return "issued"
	default:
		return "unknown"
	}
}

// PeerIdentity 是握手后确定的对端身份。节点 ID 只取自验证过的证书，
// 从不信任请求元数据里报的值。
type PeerIdentity struct {
	Class PeerClass
	// NodeID 只在 PeerIssued 时有值。
	NodeID int64
	// CertSerial 是证书序列号（十六进制），PeerIssued 时用于吊销和按证书断连。
	CertSerial string
	// KeyFingerprint 是对端证书公钥的 SHA-256（SubjectPublicKeyInfo），
	// PeerLongTerm 时就是节点长期密钥的指纹（管理员激活时核对的那个）。
	KeyFingerprint string
	NotAfter       time.Time
	RemoteAddr     net.Addr
}

// limiterKey 是按对端限流、幂等分区用的键。
func (p PeerIdentity) limiterKey() string {
	switch p.Class {
	case PeerIssued:
		return "node:" + strconv.FormatInt(p.NodeID, 10)
	default:
		// 长期密钥和匿名对端都按来源 IP 限流：换一把密钥不能换来一份新的额度。
		if p.RemoteAddr != nil {
			if host, _, err := net.SplitHostPort(p.RemoteAddr.String()); err == nil {
				return "ip:" + host
			}
			return "ip:" + p.RemoteAddr.String()
		}
		return "anonymous"
	}
}

// NodeURI 返回写进从节点证书的 URI SAN。
func NodeURI(nodeID int64) *url.URL {
	u, _ := url.Parse(nodeURIPrefix + strconv.FormatInt(nodeID, 10))
	return u
}

// NodeIDFromCertificate 从主节点签发的证书里取节点 ID：必须恰好有一个本协议的 URI SAN。
func NodeIDFromCertificate(cert *x509.Certificate) (int64, error) {
	var found int64
	for _, u := range cert.URIs {
		raw := u.String()
		if !strings.HasPrefix(raw, nodeURIPrefix) {
			continue
		}
		if found != 0 {
			return 0, errors.New("relay certificate carries more than one node id")
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(raw, nodeURIPrefix), 10, 64)
		if err != nil || id <= 0 {
			return 0, fmt.Errorf("relay certificate has an invalid node id %q", raw)
		}
		found = id
	}
	if found == 0 {
		return 0, errors.New("relay certificate has no node id")
	}
	return found, nil
}

// PublicKeyFingerprint 返回公钥的 SHA-256 指纹（十六进制小写），按 PKIX 编码计算。
func PublicKeyFingerprint(pub any) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:]), nil
}

// CertificateFingerprint 返回证书 DER 的 SHA-256 指纹（十六进制小写）。
// 从节点固定的"主从通信根证书指纹"就是这个值（设计 7.2、11.1）。
func CertificateFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// NormalizeFingerprint 去掉空白、冒号并转小写，方便管理员从页面复制带冒号的写法。
func NormalizeFingerprint(fp string) string {
	fp = strings.ToLower(strings.TrimSpace(fp))
	fp = strings.ReplaceAll(fp, ":", "")
	return strings.ReplaceAll(fp, " ", "")
}

// allowedKey 限制证书密钥类型：Ed25519 或 ECDSA P-256（设计 7.1）。
func allowedKey(pub any) bool {
	switch k := pub.(type) {
	case ed25519.PublicKey:
		return len(k) == ed25519.PublicKeySize
	case *ecdsa.PublicKey:
		return k.Curve == elliptic.P256()
	default:
		return false
	}
}

// classifyPeer 给主节点端的握手结果分类。三类之外的一律失败（fail closed）：
//   - 没有证书：匿名；
//   - 能验证到主从通信根证书、带节点 ID：签发证书；
//   - 只有一张、自签名有效、密钥类型允许、在有效期内：长期密钥。
//
// 验证到根证书失败、却带着多张证书（像是签发证书链）的，不降级为长期密钥。
func classifyPeer(certs []*x509.Certificate, roots *x509.CertPool, now time.Time) (PeerIdentity, error) {
	if len(certs) == 0 {
		return PeerIdentity{Class: PeerAnonymous}, nil
	}
	leaf := certs[0]
	if !allowedKey(leaf.PublicKey) {
		return PeerIdentity{}, errors.New("relay peer certificate uses a disallowed key type")
	}
	fp, err := PublicKeyFingerprint(leaf.PublicKey)
	if err != nil {
		return PeerIdentity{}, err
	}

	if roots != nil {
		intermediates := x509.NewCertPool()
		for _, c := range certs[1:] {
			intermediates.AddCert(c)
		}
		_, verifyErr := leaf.Verify(x509.VerifyOptions{
			Roots:         roots,
			Intermediates: intermediates,
			CurrentTime:   now,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		})
		if verifyErr == nil {
			nodeID, err := NodeIDFromCertificate(leaf)
			if err != nil {
				return PeerIdentity{}, err
			}
			return PeerIdentity{
				Class:          PeerIssued,
				NodeID:         nodeID,
				CertSerial:     leaf.SerialNumber.Text(16),
				KeyFingerprint: fp,
				NotAfter:       leaf.NotAfter,
			}, nil
		}
		if len(certs) > 1 {
			return PeerIdentity{}, fmt.Errorf("relay peer certificate chain does not verify: %w", verifyErr)
		}
	}

	if len(certs) != 1 {
		return PeerIdentity{}, errors.New("relay peer presented an unrecognized certificate chain")
	}
	// 自签名：用证书自己的公钥验自己的签名。不用 CheckSignatureFrom，它要求父证书是 CA。
	if err := leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature); err != nil {
		return PeerIdentity{}, fmt.Errorf("relay peer certificate is neither issued nor validly self-signed: %w", err)
	}
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return PeerIdentity{}, errors.New("relay peer long-term certificate is outside its validity period")
	}
	return PeerIdentity{
		Class:          PeerLongTerm,
		KeyFingerprint: fp,
		NotAfter:       leaf.NotAfter,
	}, nil
}
