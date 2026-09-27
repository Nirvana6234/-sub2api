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
	"github.com/gin-gonic/gin"
)

// NewHandOff 返回"交给主节点转发"的实现（设计 3.1：主节点回"暂不支持"的请求）。masterURL 是主节点对外的
// HTTP 地址。原始请求体原样转发；客户端 IP 放在 X-Forwarded-For，主节点要把从节点地址配成可信代理
// 才会按它识别客户端（部署说明，WP17）。
func NewHandOff(masterURL *url.URL, rt http.RoundTripper) func(c *gin.Context, body []byte) {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(masterURL)
			r.Out.Host = masterURL.Host
			if clientIP, _ := r.In.Context().Value(clientIPKey{}).(string); clientIP != "" {
				r.Out.Header.Set("X-Forwarded-For", clientIP)
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
