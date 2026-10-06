package master_test

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/stretchr/testify/require"
)

// 外部探测成功路径：证书链和主机名通过校验、/health 回 200，读到证书到期时间；/health 不是 200 时报错。
func TestProbeWithRootsSucceedsAgainstATrustedNode(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" || r.Host != "example.com" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	host, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	require.NoError(t, err)
	port, _ := strconv.Atoi(portStr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// httptest 的证书签给 example.com：探测直连 IP、按这个域名握手和请求。
	res, err := master.ProbeWithRoots(ctx, host, port, "example.com", roots)
	require.NoError(t, err)
	require.False(t, res.CertNotAfter.IsZero())
	require.True(t, res.CertNotAfter.After(time.Now()))

	_, err = master.ProbeWithRoots(ctx, host, port, "other.example.org", roots)
	require.Error(t, err, "the certificate does not match the domain registered for the node")
	var hostErr x509.HostnameError
	require.ErrorAs(t, err, &hostErr)

	status = http.StatusServiceUnavailable
	_, err = master.ProbeWithRoots(ctx, host, port, "example.com", roots)
	require.Error(t, err)
	require.Contains(t, err.Error(), "503")
}
