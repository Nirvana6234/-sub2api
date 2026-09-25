// Package relaytest 提供主从通信测试用的证书：主从通信根证书、主节点证书、
// 从节点签发证书和长期密钥自签证书。只给测试用，生产代码不要导入。
package relaytest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
)

// PKI 是一套测试用的主从通信证书体系。
type PKI struct {
	t        testing.TB
	Root     *x509.Certificate
	rootKey  crypto.Signer
	serial   atomic.Int64
	RootPool *x509.CertPool
}

// NewPKI 生成一张主从通信根证书（ECDSA P-256）。subject 为空时用默认名。
func NewPKI(t testing.TB, subject ...string) *PKI {
	t.Helper()
	cn := "sub2api relay root"
	if len(subject) > 0 {
		cn = subject[0]
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p := &PKI{t: t, rootKey: key}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(p.serial.Add(1)),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	p.Root, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	p.RootPool = x509.NewCertPool()
	p.RootPool.AddCert(p.Root)
	return p
}

// RootFingerprint 返回根证书指纹（从节点固定的那个值）。
func (p *PKI) RootFingerprint() string { return transport.CertificateFingerprint(p.Root) }

// MasterOptions 定制主节点证书，默认是合格的证书。
type MasterOptions struct {
	DNSNames  []string
	NotBefore time.Time
	NotAfter  time.Time
	// OmitRoot 为 true 时证书链不带根证书。
	OmitRoot bool
}

// MasterCert 签发主节点的主从通信证书，链里带根证书。
func (p *PKI) MasterCert(opts ...MasterOptions) *tls.Certificate {
	p.t.Helper()
	o := MasterOptions{DNSNames: []string{transport.MasterServerName}}
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.NotBefore.IsZero() {
		o.NotBefore = time.Now().Add(-time.Hour)
	}
	if o.NotAfter.IsZero() {
		o.NotAfter = time.Now().Add(24 * time.Hour)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		p.t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(p.serial.Add(1)),
		Subject:      pkix.Name{CommonName: "sub2api relay master"},
		DNSNames:     o.DNSNames,
		NotBefore:    o.NotBefore,
		NotAfter:     o.NotAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	return p.sign(tmpl, key, key.Public(), !o.OmitRoot)
}

// NodeCert 签发节点 nodeID 的主从通信证书（Ed25519，URI SAN 带节点 ID）。
func (p *PKI) NodeCert(nodeID int64, notAfter ...time.Time) *tls.Certificate {
	p.t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		p.t.Fatal(err)
	}
	na := time.Now().Add(24 * time.Hour)
	if len(notAfter) > 0 {
		na = notAfter[0]
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(p.serial.Add(1)),
		Subject:      pkix.Name{CommonName: "sub2api relay node"},
		URIs:         []*url.URL{transport.NodeURI(nodeID)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     na,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	return p.sign(tmpl, priv, pub, false)
}

func (p *PKI) sign(tmpl *x509.Certificate, key crypto.Signer, pub crypto.PublicKey, withRoot bool) *tls.Certificate {
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.Root, pub, p.rootKey)
	if err != nil {
		p.t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		p.t.Fatal(err)
	}
	chain := [][]byte{der}
	if withRoot {
		chain = append(chain, p.Root.Raw)
	}
	return &tls.Certificate{Certificate: chain, PrivateKey: key, Leaf: leaf}
}

// LongTermCert 生成从节点用长期密钥自签的证书（Ed25519），返回证书和公钥指纹。
func LongTermCert(t testing.TB) (*tls.Certificate, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "sub2api relay node long-term key"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := transport.PublicKeyFingerprint(pub)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv, Leaf: leaf}, fp
}
