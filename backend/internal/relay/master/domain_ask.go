package master

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// 给 Caddy on_demand TLS 的 ask 接口（设计 10.3）：管理员把某台从节点的域名解析改到主节点后，主节点前面的 Caddy 按需为这个域名
// 申请证书；申请前先问这个接口"这是不是已登记的从节点域名"，不是就拒绝，防止被人拿任意域名刷证书。
// 只监听本机回环地址，不对外。

// ErrAskAddrNotLoopback：ask 接口的监听地址不是回环地址。
var ErrAskAddrNotLoopback = errors.New("relay.domain_ask_addr must be a loopback address such as 127.0.0.1:7444")

func (r *Runtime) startDomainAsk(addr string) (*http.Server, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return nil, ErrAskAddrNotLoopback
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on relay.domain_ask_addr: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.IsNodeDomain(req.URL.Query().Get("domain")) {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "unknown domain", http.StatusNotFound)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(lis) }()
	return srv, nil
}
