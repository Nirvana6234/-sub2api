package master

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/keystore"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
)

// 证书有效期（设计 19）。
const (
	NodeCertificateLifetime   = 24 * time.Hour
	masterCertificateLifetime = 30 * 24 * time.Hour
)

// CA 用主从通信根证书签发证书。根证书私钥由 keystore 加密保管；
// 轮换期间新旧根证书都用于验证，签发用最新的。
type CA struct {
	keys *keystore.Store
	now  func() time.Time

	mu        sync.Mutex
	ring      *keystore.Ring
	pool      *x509.CertPool
	masterTLS *tls.Certificate
}

// NewCA 读出根证书；一个都没有时生成第一张。
func NewCA(keys *keystore.Store) (*CA, error) {
	c := &CA{keys: keys, now: time.Now}
	if err := c.Reload(); err != nil {
		return nil, err
	}
	return c, nil
}

// Reload 重新读取根证书（轮换后调用）。
func (c *CA) Reload() error {
	ring, err := c.keys.EnsureActive(keystore.PurposeRootCA)
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	for _, k := range ring.Keys {
		if k.Certificate == nil {
			return fmt.Errorf("relay root v%d has no certificate", k.Version)
		}
		pool.AddCert(k.Certificate)
	}
	c.mu.Lock()
	c.ring, c.pool, c.masterTLS = ring, pool, nil
	c.mu.Unlock()
	return nil
}

// RootPool 是验证从节点证书用的根证书池（所有未停用的根）。
func (c *CA) RootPool() *x509.CertPool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pool
}

// RootFingerprints 返回所有未停用根证书的指纹，最新的在最后。管理页展示给管理员，
// 从节点本机配置的就是它（设计 11.1）。
func (c *CA) RootFingerprints() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.ring.Keys))
	for _, k := range c.ring.Keys {
		out = append(out, transport.CertificateFingerprint(k.Certificate))
	}
	return out
}

// IssueNodeCertificate 给节点签发一张主从通信证书（ClientAuth，URI SAN 带节点 ID）。
func (c *CA) IssueNodeCertificate(nodeID int64, pub crypto.PublicKey, lifetime time.Duration) (*x509.Certificate, error) {
	if nodeID <= 0 {
		return nil, errors.New("relay certificate needs a node id")
	}
	if !allowedNodeKey(pub) {
		return nil, errors.New("relay node certificate key must be Ed25519 or ECDSA P-256")
	}
	c.mu.Lock()
	root := c.ring.Active
	c.mu.Unlock()
	now := c.now()
	tmpl := &x509.Certificate{
		Subject:     pkix.Name{CommonName: fmt.Sprintf("sub2api relay node %d", nodeID)},
		URIs:        []*url.URL{transport.NodeURI(nodeID)},
		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    now.Add(lifetime),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	return signLeaf(tmpl, pub, root)
}

// MasterCertificate 返回主节点的主从通信证书（链里带根证书），过了一半有效期就换新的。
func (c *CA) MasterCertificate() (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if cur := c.masterTLS; cur != nil && now.Before(cur.Leaf.NotAfter.Add(-masterCertificateLifetime/2)) {
		return cur, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	root := c.ring.Active
	tmpl := &x509.Certificate{
		Subject:     pkix.Name{CommonName: "sub2api relay master"},
		DNSNames:    []string{transport.MasterServerName},
		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    now.Add(masterCertificateLifetime),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leaf, err := signLeaf(tmpl, key.Public(), root)
	if err != nil {
		return nil, err
	}
	c.masterTLS = &tls.Certificate{Certificate: [][]byte{leaf.Raw, root.Certificate.Raw}, PrivateKey: key, Leaf: leaf}
	return c.masterTLS, nil
}

func signLeaf(tmpl *x509.Certificate, pub crypto.PublicKey, root *keystore.Key) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	tmpl.SerialNumber = serial
	der, err := x509.CreateCertificate(rand.Reader, tmpl, root.Certificate, pub, root.Signer)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

func allowedNodeKey(pub crypto.PublicKey) bool {
	switch k := pub.(type) {
	case ed25519.PublicKey:
		return len(k) == ed25519.PublicKeySize
	case *ecdsa.PublicKey:
		return k.Curve == elliptic.P256()
	default:
		return false
	}
}
