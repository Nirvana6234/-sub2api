// Package identity 是从节点的身份（设计 7.2、11.1）：
//
//   - 长期密钥：第一次启动时在本机生成（Ed25519），私钥不离开本机，指纹打印在控制台，
//     管理员激活时核对。它只用于注册、待激活期间的心跳、领取证书和证书过期后的恢复。
//   - 主从通信证书：激活后由主节点签发，24 小时有效，在线时自动续签，每次续签都换新的
//     TLS 密钥和 X25519 加密密钥（上游凭据二次加密用）。
//
// 长期密钥和当前证书都存在持久目录里：容器重建后不需要重新激活（设计 11.6）。
package identity

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sealbox"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
)

const (
	fileLongTermKey = "identity.key"
	fileCert        = "node.crt"
	fileCertKey     = "node.key"
	fileEncKey      = "node.enc"
	// 待用的新密钥：发出领证 / 续签请求之前先落盘，回复丢失后重试、甚至重启后都复用
	// 同一对密钥，主节点据此把重发识别为重放，而不是"同一证书续签两次"。
	filePendingKey = "pending.key"
	filePendingEnc = "pending.enc"
)

// validityMargin：签发证书剩余有效期少于它就不再使用，改走恢复。
const validityMargin = time.Minute

// Identity 是从节点的身份和当前证书。
type Identity struct {
	dir          string
	longTerm     ed25519.PrivateKey
	longTermCert *tls.Certificate
	fingerprint  string
	now          func() time.Time

	mu         sync.RWMutex
	issued     *tls.Certificate
	nodeID     int64
	encKey     *ecdh.PrivateKey
	prevEncKey *ecdh.PrivateKey
}

// Load 读取（没有时生成）长期密钥，并读出持久化的签发证书。
func Load(dir string) (*Identity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	key, err := loadOrCreateLongTermKey(filepath.Join(dir, fileLongTermKey))
	if err != nil {
		return nil, err
	}
	fp, err := transport.PublicKeyFingerprint(key.Public())
	if err != nil {
		return nil, err
	}
	cert, err := selfSigned(key)
	if err != nil {
		return nil, err
	}
	id := &Identity{dir: dir, longTerm: key, longTermCert: cert, fingerprint: fp, now: time.Now}
	if err := id.loadIssued(); err != nil {
		// 证书文件坏了不影响启动：走恢复重新领取。
		id.issued, id.encKey, id.nodeID = nil, nil, 0
	}
	return id, nil
}

// Fingerprint 返回长期密钥指纹：管理员激活时核对的值，启动时打印到控制台和日志。
func (i *Identity) Fingerprint() string { return i.fingerprint }

// LongTermCertificate 返回长期密钥自签证书（注册、待激活心跳、领证和恢复时出示）。
func (i *Identity) LongTermCertificate() *tls.Certificate { return i.longTermCert }

// NodeID 返回签发证书里的节点 ID；还没领到证书时为 0。
func (i *Identity) NodeID() int64 {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.nodeID
}

// TLSCertificate 返回握手要出示的证书：有效的签发证书，否则长期密钥自签证书。
// 交给 transport.ClientTLSOptions.Certificate。
func (i *Identity) TLSCertificate() *tls.Certificate {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.issuedValidLocked() {
		return i.issued
	}
	return i.longTermCert
}

// HasValidIssued 报告手里是否有还能用的签发证书。
func (i *Identity) HasValidIssued() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.issuedValidLocked()
}

// IssuedValidity 返回当前签发证书的有效期；没有证书时返回零值。
func (i *Identity) IssuedValidity() (notBefore, notAfter time.Time) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.issued == nil {
		return time.Time{}, time.Time{}
	}
	return i.issued.Leaf.NotBefore, i.issued.Leaf.NotAfter
}

func (i *Identity) issuedValidLocked() bool {
	return i.issued != nil && i.now().Before(i.issued.Leaf.NotAfter.Add(-validityMargin))
}

// EncryptionKeys 返回用于解开上游凭据的 X25519 私钥：当前的在前。续签后的宽限期内
// 旧连接上发来的凭据还是用旧公钥加密的，所以旧私钥保留到 DropPreviousEncryptionKey。
func (i *Identity) EncryptionKeys() []*ecdh.PrivateKey {
	i.mu.RLock()
	defer i.mu.RUnlock()
	var out []*ecdh.PrivateKey
	if i.encKey != nil {
		out = append(out, i.encKey)
	}
	if i.prevEncKey != nil {
		out = append(out, i.prevEncKey)
	}
	return out
}

// OpenSealed 用当前或上一把加密私钥解开主节点加密的凭据。
func (i *Identity) OpenSealed(sealed, aad []byte) ([]byte, error) {
	lastErr := errors.New("relay node has no encryption key")
	for _, k := range i.EncryptionKeys() {
		plain, err := sealbox.Open(k, sealed, aad)
		if err == nil {
			return plain, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// DropPreviousEncryptionKey 在旧证书连接都关闭后丢掉旧的加密私钥。
func (i *Identity) DropPreviousEncryptionKey() {
	i.mu.Lock()
	i.prevEncKey = nil
	i.mu.Unlock()
}

// PendingKeys 是一次领证 / 续签新生成、还没拿到证书的密钥。
type PendingKeys struct {
	tlsKey ed25519.PrivateKey
	encKey *ecdh.PrivateKey
}

// PrepareRequest 返回要发给主节点的领证 / 续签请求。上一次请求还没装上证书时
// 复用那一对待用密钥（已落盘），否则生成新的 TLS 密钥和加密密钥并先落盘。
func (i *Identity) PrepareRequest() (*relayv1.CertificateRequest, *PendingKeys, error) {
	p, err := i.loadPending()
	if err != nil || p == nil {
		p, err = i.newPending()
		if err != nil {
			return nil, nil, err
		}
	}
	pub, err := x509.MarshalPKIXPublicKey(p.tlsKey.Public())
	if err != nil {
		return nil, nil, err
	}
	return &relayv1.CertificateRequest{TlsPublicKey: pub, EncryptionPublicKey: p.encKey.PublicKey().Bytes()}, p, nil
}

func (i *Identity) newPending() (*PendingKeys, error) {
	_, tlsKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	encKey, err := sealbox.GenerateKey()
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(tlsKey)
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(filepath.Join(i.dir, filePendingEnc), pem.EncodeToMemory(&pem.Block{Type: "X25519 PRIVATE KEY", Bytes: encKey.Bytes()})); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(filepath.Join(i.dir, filePendingKey), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil {
		return nil, err
	}
	return &PendingKeys{tlsKey: tlsKey, encKey: encKey}, nil
}

func (i *Identity) loadPending() (*PendingKeys, error) {
	keyPEM, err := os.ReadFile(filepath.Join(i.dir, filePendingKey))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	encPEM, err := os.ReadFile(filepath.Join(i.dir, filePendingEnc))
	if err != nil {
		return nil, err
	}
	kb, _ := pem.Decode(keyPEM)
	eb, _ := pem.Decode(encPEM)
	if kb == nil || eb == nil {
		return nil, errors.New("relay pending key files are malformed")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return nil, err
	}
	tlsKey, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("relay pending key is not Ed25519")
	}
	encKey, err := ecdh.X25519().NewPrivateKey(eb.Bytes)
	if err != nil {
		return nil, err
	}
	return &PendingKeys{tlsKey: tlsKey, encKey: encKey}, nil
}

// DiscardIssued 丢掉当前签发证书（内存和文件），之后握手改用长期密钥证书，
// 走 ObtainCertificate 恢复。续签一直失败、旧证书已被主节点替换时用。
func (i *Identity) DiscardIssued() {
	i.mu.Lock()
	i.issued = nil
	i.mu.Unlock()
	_ = os.Remove(filepath.Join(i.dir, fileCert))
}

// Install 校验并启用主节点签发的证书：公钥必须是这次生成的、节点 ID 与证书一致且
// 不能变（领过证书的节点换 ID 说明走错了主节点或被冒充），再落盘。
func (i *Identity) Install(p *PendingKeys, resp *relayv1.CertificateResponse) error {
	if p == nil || resp == nil {
		return errors.New("relay certificate response is missing")
	}
	leaf, err := x509.ParseCertificate(resp.GetCertificate())
	if err != nil {
		return fmt.Errorf("relay certificate does not parse: %w", err)
	}
	certPub, ok := leaf.PublicKey.(ed25519.PublicKey)
	if !ok || !certPub.Equal(p.tlsKey.Public()) {
		return errors.New("relay certificate was not issued for the key this node generated")
	}
	nodeID, err := transport.NodeIDFromCertificate(leaf)
	if err != nil {
		return err
	}
	if nodeID != resp.GetNodeId() {
		return errors.New("relay certificate node id does not match the response")
	}
	if !i.now().Before(leaf.NotAfter) {
		return errors.New("relay certificate is already expired")
	}
	i.mu.RLock()
	prevNode := i.nodeID
	i.mu.RUnlock()
	if prevNode != 0 && prevNode != nodeID {
		return fmt.Errorf("relay certificate is for node %d but this node is %d", nodeID, prevNode)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(p.tlsKey)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(i.dir, fileCertKey), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(i.dir, fileEncKey), pem.EncodeToMemory(&pem.Block{Type: "X25519 PRIVATE KEY", Bytes: p.encKey.Bytes()})); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(i.dir, fileCert), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})); err != nil {
		return err
	}

	// 证书已落盘，待用密钥用完了。
	_ = os.Remove(filepath.Join(i.dir, filePendingKey))
	_ = os.Remove(filepath.Join(i.dir, filePendingEnc))

	i.mu.Lock()
	i.prevEncKey = i.encKey
	i.encKey = p.encKey
	i.issued = &tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: p.tlsKey, Leaf: leaf}
	i.nodeID = nodeID
	i.mu.Unlock()
	return nil
}

func (i *Identity) loadIssued() error {
	certPEM, err := os.ReadFile(filepath.Join(i.dir, fileCert))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(filepath.Join(i.dir, fileCertKey))
	if err != nil {
		return err
	}
	encPEM, err := os.ReadFile(filepath.Join(i.dir, fileEncKey))
	if err != nil {
		return err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return err
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return err
	}
	cert.Leaf = leaf
	nodeID, err := transport.NodeIDFromCertificate(leaf)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(encPEM)
	if block == nil {
		return errors.New("relay encryption key file is malformed")
	}
	encKey, err := ecdh.X25519().NewPrivateKey(block.Bytes)
	if err != nil {
		return err
	}
	i.issued, i.nodeID, i.encKey = &cert, nodeID, encKey
	return nil
}

func loadOrCreateLongTermKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		block, _ := pem.Decode(raw)
		if block == nil || block.Type != "PRIVATE KEY" {
			return nil, fmt.Errorf("relay long-term key %s is malformed", path)
		}
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		key, ok := parsed.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("relay long-term key %s is not Ed25519", path)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil {
		return nil, err
	}
	return key, nil
}

// selfSigned 用长期密钥生成自签证书，握手时证明"持有这把长期密钥"。
func selfSigned(key ed25519.PrivateKey) (*tls.Certificate, error) {
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()),
		Subject:      pkix.Name{CommonName: "sub2api relay node long-term key"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
