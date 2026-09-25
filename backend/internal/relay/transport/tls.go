package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"time"
)

// 主从通信只用 TLS 1.3（设计 7.1）。TLS 1.3 的套件全是 AEAD，Go 不允许也不需要另配；
// 密钥交换限定 X25519 和 P-256。
var relayCurves = []tls.CurveID{tls.X25519, tls.CurveP256}

// ServerTLSOptions 是主节点端 TLS 配置的输入。两个回调都在每次握手时调用，
// 证书和根证书可以在运行时轮换（设计 7.4 私钥轮换：新旧根并存一段时间）。
type ServerTLSOptions struct {
	// Certificate 返回主节点的主从通信证书，链里必须带上根证书（从节点只有根的指纹）。
	Certificate func() (*tls.Certificate, error)
	// Roots 返回用于验证从节点签发证书的根证书池。
	Roots func() *x509.CertPool
	// Now 可替换时间源，测试用。
	Now func() time.Time
}

func (o ServerTLSOptions) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// ServerTLSConfig 构造主节点端的 tls.Config。
//
// ClientAuth 用 RequestClientCert：主节点端口同时要接没有证书的注册、用长期密钥
// 自签的待激活节点和签发证书的正式节点（设计 11.1、11.2、7.2），标准库的几种
// 校验模式都会误拒其中一类。所以证书在 VerifyConnection 里由 classifyPeer 自己校验，
// 三类之外的握手直接失败；每个 RPC 允许哪类对端由服务端拦截器按访问策略检查。
func ServerTLSConfig(opts ServerTLSOptions) (*tls.Config, error) {
	if opts.Certificate == nil || opts.Roots == nil {
		return nil, errors.New("relay server TLS needs a certificate and a root pool")
	}
	return &tls.Config{
		MinVersion:       tls.VersionTLS13,
		MaxVersion:       tls.VersionTLS13,
		CurvePreferences: relayCurves,
		ClientAuth:       tls.RequestClientCert,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return opts.Certificate()
		},
		VerifyConnection: func(cs tls.ConnectionState) error {
			_, err := classifyPeer(cs.PeerCertificates, opts.Roots(), opts.now())
			return err
		},
	}, nil
}

// ClientTLSOptions 是从节点端 TLS 配置的输入。
type ClientTLSOptions struct {
	// PinnedRootFingerprints 是主从通信根证书的 SHA-256 指纹（CertificateFingerprint）。
	// 从节点本机只有这个（设计 11.1）；轮换期间可以同时固定新旧两个。
	PinnedRootFingerprints func() []string
	// Certificate 返回这次握手出示的客户端证书；返回 nil 表示匿名（注册）。
	Certificate func() *tls.Certificate
	Now         func() time.Time
}

func (o ClientTLSOptions) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// ClientTLSConfig 构造从节点端的 tls.Config。
//
// 从节点手里没有根证书，只有它的指纹，所以不能预先填 RootCAs：握手时在主节点
// 出示的链里按指纹找到根证书，只用这一张建证书池，再按 ServerAuth 用途和固定的
// MasterServerName 验证主节点证书。标准校验因此关掉（InsecureSkipVerify），
// VerifyConnection 是唯一的校验，任何一步不过都让握手失败。
func ClientTLSConfig(opts ClientTLSOptions) (*tls.Config, error) {
	if opts.PinnedRootFingerprints == nil {
		return nil, errors.New("relay client TLS needs pinned root fingerprints")
	}
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
		CurvePreferences:   relayCurves,
		ServerName:         MasterServerName,
		InsecureSkipVerify: true, //nolint:gosec // 由 VerifyConnection 按固定的根证书指纹完整校验，见上方说明。
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			if opts.Certificate != nil {
				if cert := opts.Certificate(); cert != nil {
					return cert, nil
				}
			}
			return &tls.Certificate{}, nil
		},
		VerifyConnection: func(cs tls.ConnectionState) error {
			return verifyMaster(cs.PeerCertificates, opts.PinnedRootFingerprints(), opts.now())
		},
	}, nil
}

// verifyMaster 按固定的根证书指纹验证主节点证书链。
func verifyMaster(chain []*x509.Certificate, pinned []string, now time.Time) error {
	if len(chain) == 0 {
		return errors.New("relay master presented no certificate")
	}
	want := make(map[string]struct{}, len(pinned))
	for _, fp := range pinned {
		if fp = NormalizeFingerprint(fp); fp != "" {
			want[fp] = struct{}{}
		}
	}
	if len(want) == 0 {
		return errors.New("relay client has no pinned root fingerprint")
	}

	leaf := chain[0]
	var root *x509.Certificate
	intermediates := x509.NewCertPool()
	for _, c := range chain[1:] {
		if _, ok := want[CertificateFingerprint(c)]; ok && root == nil {
			root = c
			continue
		}
		intermediates.AddCert(c)
	}
	if root == nil {
		return errors.New("relay master certificate chain does not contain the pinned root")
	}
	if !root.IsCA || !root.BasicConstraintsValid {
		return errors.New("relay pinned root is not a CA certificate")
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		DNSName:       MasterServerName,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return fmt.Errorf("relay master certificate does not verify against the pinned root: %w", err)
	}
	return nil
}
