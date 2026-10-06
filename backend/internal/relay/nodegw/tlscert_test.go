package nodegw

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/acme"
)

func writeSelfSigned(t *testing.T, dir, domain string, notAfter time.Time) (certFile, keyFile string, cert *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	parsed, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return certFile, keyFile, parsed
}

// 证书只给自己的域名申请：别的 SNI 触发不了申请；TLS-ALPN-01 验证握手交给 autocert（NextProtos 带 acme-tls/1）。
func TestCertProviderOnlyServesItsOwnDomain(t *testing.T) {
	domain := ""
	p := newCertProvider(config.RelayConfig{}, t.TempDir(), func() string { return domain })

	require.Error(t, p.manager.HostPolicy(context.Background(), "relay1.example.com"), "no domain yet: nothing may be requested")
	domain = "relay1.example.com"
	require.NoError(t, p.manager.HostPolicy(context.Background(), "relay1.example.com"))
	require.NoError(t, p.manager.HostPolicy(context.Background(), "RELAY1.example.com."))
	require.Error(t, p.manager.HostPolicy(context.Background(), "evil.example.com"))

	cfg := p.tlsConfig()
	require.Contains(t, cfg.NextProtos, acme.ALPNProto)
	require.Contains(t, cfg.NextProtos, "h2")
	require.GreaterOrEqual(t, int(cfg.MinVersion), tls.VersionTLS12)

	// 别的 SNI：策略拒绝，记下原因。
	_, err := p.getCertificate(&tls.ClientHelloInfo{ServerName: "evil.example.com", SupportedProtos: []string{"h2"}})
	require.Error(t, err)
	require.NotEmpty(t, p.LastError())
}

// 申请不到时管理员在本机放置证书文件：配置后优先用它；域名对不上时不用；文件换了会重新读；坏文件不影响已有的。
func TestCertProviderPrefersLocalCertificateFiles(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, first := writeSelfSigned(t, dir, "relay1.example.com", time.Now().Add(90*24*time.Hour))
	domain := "relay1.example.com"
	p := newCertProvider(config.RelayConfig{NodeCertFile: certFile, NodeKeyFile: keyFile}, dir, func() string { return domain })

	got, err := p.getCertificate(&tls.ClientHelloInfo{ServerName: "relay1.example.com", SupportedProtos: []string{"h2"}})
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(got.Certificate[0])
	require.NoError(t, err)
	require.Equal(t, first.SerialNumber, leaf.SerialNumber)

	// 没带 SNI 的连接（按 IP 访问）同样用本机证书。
	got2, err := p.getCertificate(&tls.ClientHelloInfo{SupportedProtos: []string{"h2"}})
	require.NoError(t, err)
	require.Equal(t, got.Certificate[0], got2.Certificate[0])

	// 域名对不上：不用本机证书（交给 ACME 的策略拒绝）。
	_, err = p.getCertificate(&tls.ClientHelloInfo{ServerName: "other.example.com", SupportedProtos: []string{"h2"}})
	require.Error(t, err)

	// 文件换了：重新读。
	time.Sleep(20 * time.Millisecond)
	_, _, second := writeSelfSigned(t, dir, "relay1.example.com", time.Now().Add(180*24*time.Hour))
	require.NoError(t, os.Chtimes(certFile, time.Now().Add(time.Second), time.Now().Add(time.Second)))
	got, err = p.getCertificate(&tls.ClientHelloInfo{ServerName: "relay1.example.com", SupportedProtos: []string{"h2"}})
	require.NoError(t, err)
	leaf, _ = x509.ParseCertificate(got.Certificate[0])
	require.Equal(t, second.SerialNumber, leaf.SerialNumber)

	// 坏文件：保留已加载的证书，记下原因。
	require.NoError(t, os.WriteFile(certFile, []byte("garbage"), 0o600))
	require.NoError(t, os.Chtimes(certFile, time.Now().Add(2*time.Second), time.Now().Add(2*time.Second)))
	got, err = p.getCertificate(&tls.ClientHelloInfo{ServerName: "relay1.example.com", SupportedProtos: []string{"h2"}})
	require.NoError(t, err)
	leaf, _ = x509.ParseCertificate(got.Certificate[0])
	require.Equal(t, second.SerialNumber, leaf.SerialNumber)
	require.Contains(t, p.LastError(), "could not be loaded")
}

// 完整握手：用本机证书起 TLS 监听，客户端按域名校验通过。
func TestCertProviderServesARealHandshake(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, leaf := writeSelfSigned(t, dir, "relay1.example.com", time.Now().Add(48*time.Hour))
	p := newCertProvider(config.RelayConfig{NodeCertFile: certFile, NodeKeyFile: keyFile}, dir, func() string { return "relay1.example.com" })

	lis, err := tls.Listen("tcp", "127.0.0.1:0", p.tlsConfig())
	require.NoError(t, err)
	t.Cleanup(func() { _ = lis.Close() })
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			go func() { _ = c.(*tls.Conn).Handshake(); _ = c.Close() }()
		}
	}()
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", lis.Addr().String(), &tls.Config{ServerName: "relay1.example.com", RootCAs: roots})
	require.NoError(t, err)
	require.Equal(t, leaf.SerialNumber, conn.ConnectionState().PeerCertificates[0].SerialNumber)
	_ = conn.Close()
}
