package nodegw

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	relaysign "github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/gin-gonic/gin"
)

// NewHandOff 返回"交给主节点转发"的实现（设计 3.1：主节点回"暂不支持"的请求）。masterURL 是主节点对外的
// HTTP 地址。原始请求体原样转发；客户端 IP 放在 X-Forwarded-For，主节点要把从节点地址配成可信代理
// 才会按它识别客户端（部署说明，WP17）。
//
// signer 非 nil 时给每个请求带上"来自从节点"的标记（sign.HandoffHeader）：主节点分配比例为 0 时主节点只接带标记的转发请求。
func NewHandOff(masterURL *url.URL, rt http.RoundTripper, signer ...func(method, path string) string) func(c *gin.Context, body []byte) {
	var sign func(method, path string) string
	if len(signer) > 0 {
		sign = signer[0]
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(masterURL)
			r.Out.Host = masterURL.Host
			if clientIP, _ := r.In.Context().Value(clientIPKey{}).(string); clientIP != "" {
				r.Out.Header.Set("X-Forwarded-For", clientIP)
			}
			// 客户端自己带的标记一律去掉，只认从节点自己签的。
			r.Out.Header.Del(relaysign.HandoffHeader)
			if sign != nil {
				if v := sign(r.In.Method, r.In.URL.Path); v != "" {
					r.Out.Header.Set(relaysign.HandoffHeader, v)
				}
			}
		},
		FlushInterval: -1,
		Transport:     rt,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			slog.Warn("relay hand-off to the master failed", "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"error":{"type":"api_error","message":"Upstream request failed"}}`)
		},
	}
	return func(c *gin.Context, body []byte) {
		req := c.Request.Clone(context.WithValue(c.Request.Context(), clientIPKey{}, strings.TrimSpace(ip.GetClientIP(c))))
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		proxy.ServeHTTP(c.Writer, req)
		c.Abort()
	}
}

type clientIPKey struct{}
