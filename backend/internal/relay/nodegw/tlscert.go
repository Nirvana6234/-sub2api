package nodegw

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// 从节点对外 HTTPS 的证书（设计 11.1 第 5 步、15.2）：每台从节点自己用 ACME（TLS-ALPN-01，走 443，不需要域名服务商密钥）
// 向 Let's Encrypt 申请自己域名的证书并自动续期，私钥只在本机、主节点不持有；申请不到时管理员可以在本机放置证书文件
// （relay.node_cert_file / node_key_file，优先使用）。证书缓存在数据目录（要持久保存，否则每次重建都重新申请，容易撞签发次数限制）。

// certProvider 给 TLS 监听器提供证书。
type certProvider struct {
	domain  func() string
	manager *autocert.Manager

	certFile, keyFile string

	mu        sync.Mutex
	local     *tls.Certificate
	localMod  time.Time
	lastError string
}

// newCertProvider 创建证书提供者。domain 返回这台当前的对外域名（配置快照里的，激活后才有）；dataDir 下的 certs 目录缓存 ACME 证书。
func newCertProvider(rc config.RelayConfig, dataDir string, domain func() string) *certProvider {
	p := &certProvider{domain: domain, certFile: strings.TrimSpace(rc.NodeCertFile), keyFile: strings.TrimSpace(rc.NodeKeyFile)}
	m := &autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Cache:  autocert.DirCache(dataDir + string(os.PathSeparator) + "certs"),
		Email:  strings.TrimSpace(rc.NodeACMEEmail),
		HostPolicy: func(_ context.Context, host string) error {
			// 只给自己的域名申请证书：别人拿别的 SNI 来探不会触发申请。
			if d := p.domain(); d != "" && strings.EqualFold(strings.TrimSuffix(host, "."), d) {
				return nil
			}
			return fmt.Errorf("host %q is not this node's public domain", host)
		},
	}
	if u := strings.TrimSpace(rc.NodeACMEDirectoryURL); u != "" {
		m.Client = &acme.Client{DirectoryURL: u}
	}
	p.manager = m
	return p
}

// tlsConfig 返回监听器用的 TLS 配置：本机放置的证书优先，其次 ACME；NextProtos 带 acme-tls/1（TLS-ALPN-01 验证用）。
func (p *certProvider) tlsConfig() *tls.Config {
	cfg := p.manager.TLSConfig()
	cfg.MinVersion = tls.VersionTLS12
	cfg.GetCertificate = p.getCertificate
	return cfg
}

func (p *certProvider) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	// ACME 验证握手（ALPN acme-tls/1）一律交给 autocert。
	for _, proto := range hello.SupportedProtos {
		if proto == acme.ALPNProto {
			return p.manager.GetCertificate(hello)
		}
	}
	if cert := p.localCert(hello.ServerName); cert != nil {
		return cert, nil
	}
	cert, err := p.manager.GetCertificate(hello)
	p.mu.Lock()
	if err != nil {
		p.lastError = err.Error()
	} else {
		p.lastError = ""
	}
	p.mu.Unlock()
	return cert, err
}

// localCert 返回本机放置的证书（域名对得上时）；文件改了就重新读。
func (p *certProvider) localCert(serverName string) *tls.Certificate {
	if p.certFile == "" || p.keyFile == "" {
		return nil
	}
	d := p.domain()
	if d == "" || (serverName != "" && !strings.EqualFold(serverName, d)) {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	st, err := os.Stat(p.certFile)
	if err != nil {
		return nil
	}
	if p.local == nil || st.ModTime().After(p.localMod) {
		cert, err := tls.LoadX509KeyPair(p.certFile, p.keyFile)
		if err != nil {
			p.lastError = "local certificate could not be loaded: " + err.Error()
			return p.local
		}
		p.local, p.localMod = &cert, st.ModTime()
	}
	return p.local
}

// LastError 返回最近一次申请证书失败的原因（空表示没有）。
func (p *certProvider) LastError() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastError
}

// warm 激活后主动申请一次证书（不等第一个客户端握手），失败按间隔重试直到成功或 ctx 结束：这样外部探测第一次握手时证书已经备好。
func (p *certProvider) warm(ctx context.Context) {
	delay := 10 * time.Second
	for ctx.Err() == nil {
		d := p.domain()
		if d == "" {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		hello := &tls.ClientHelloInfo{ServerName: d, SupportedProtos: []string{"h2", "http/1.1"}, SupportedVersions: []uint16{tls.VersionTLS13, tls.VersionTLS12}}
		_, err := p.getCertificate(hello)
		if err == nil {
			slog.Info("relay node certificate is ready", "domain", d)
			return
		}
		slog.Warn("relay node certificate could not be obtained yet", "domain", d, "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < 10*time.Minute {
			delay *= 2
		}
	}
}
