package nodegw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 心跳要报的本机计数：进行中的请求、近 1 分钟请求和错误（5xx）、活跃用户和 Key（鉴权通过之后），过了窗口的不再算。
func TestStatsCountRequestsErrorsAndActiveKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Unix(1_700_000_000, 0)
	s := NewStats()
	s.now = func() time.Time { return now }
	s.started = now.Add(-time.Hour)

	inflightSeen := int32(-1)
	r := gin.New()
	r.Use(s.Middleware())
	r.GET("/ok", func(c *gin.Context) {
		inflightSeen = s.inflight.Load()
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 11, UserID: 21})
		c.String(http.StatusOK, "ok")
	})
	r.GET("/boom", func(c *gin.Context) { c.String(http.StatusBadGateway, "bad") })
	r.GET("/rejected", func(c *gin.Context) { c.String(http.StatusUnauthorized, "no") })
	do := func(path string) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	}
	do("/ok")
	do("/ok")
	do("/boom")
	do("/rejected")

	require.Equal(t, int32(1), inflightSeen)
	require.Zero(t, s.inflight.Load(), "finished requests are no longer in flight")
	snap := s.Snapshot(context.Background(), "")
	require.Equal(t, int32(4), snap.GetRequests_1M())
	require.Equal(t, int32(1), snap.GetErrors_1M(), "only 5xx counts as an error")
	require.Equal(t, int32(1), snap.GetActiveKeys())
	require.Equal(t, int32(1), snap.GetActiveUsers())
	require.Equal(t, s.started.UnixMilli(), snap.GetStartedAtUnixMs())

	// 窗口过了：请求计数滑出 1 分钟，活跃用户和 Key 滑出 5 分钟。
	now = now.Add(61 * time.Second)
	snap = s.Snapshot(context.Background(), "")
	require.Zero(t, snap.GetRequests_1M())
	require.Equal(t, int32(1), snap.GetActiveKeys())
	now = now.Add(5 * time.Minute)
	snap = s.Snapshot(context.Background(), "")
	require.Zero(t, snap.GetActiveKeys())
	require.Zero(t, snap.GetActiveUsers())
}

// 日志和错误按两个标签汇总，每分钟换一批报给主节点（只有条数，没有内容）。
func TestStatsSummariseLogAndErrorCountsPerMinute(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := NewStats()
	s.now = func() time.Time { return now }
	s.countFrom = now
	s.NoteLog("error", "gateway")
	s.NoteLog("error", "gateway")
	s.NoteLog("warn", "scheduler")
	s.NoteError("upstream_error", "502")

	require.Empty(t, s.Snapshot(context.Background(), "").GetLogCounts(), "the current minute is still being collected")
	now = now.Add(time.Minute)
	snap := s.Snapshot(context.Background(), "")
	got := map[[2]string]int32{}
	for _, b := range snap.GetLogCounts() {
		got[[2]string{b.GetA(), b.GetB()}] = b.GetCount()
	}
	require.Equal(t, map[[2]string]int32{{"error", "gateway"}: 2, {"warn", "scheduler"}: 1}, got)
	require.Len(t, snap.GetErrorCounts(), 1)
	require.Equal(t, "upstream_error", snap.GetErrorCounts()[0].GetA())
}
