package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/keystore"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRelayErrorMapsBusinessErrorsTo4xx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		err    error
		status int
	}{
		{master.ErrNodeNotFound, http.StatusNotFound},
		{master.ErrKeyNotFound, http.StatusNotFound},
		{master.ErrFingerprintMismatch, http.StatusBadRequest},
		{master.ErrDomainRequired, http.StatusBadRequest},
		{fmt.Errorf("%w: ratio", master.ErrInvalidGeneralConfig), http.StatusBadRequest},
		{master.ErrRelayNotRunning, http.StatusConflict},
		{master.ErrNodesStillServing, http.StatusConflict},
		{master.ErrStatusConflict, http.StatusConflict},
		{master.ErrDomainTaken, http.StatusConflict},
		{master.ErrKeyNotStaged, http.StatusConflict},
		{master.ErrKeyInUse, http.StatusConflict},
		{master.ErrKeyRetireTooEarly, http.StatusConflict},
		{master.ErrKeyTargetUnavailable, http.StatusConflict},
		{master.ErrMasterRatioZero, http.StatusConflict},
		{master.ErrKeyAssignmentUnavailable, http.StatusConflict},
		{keystore.ErrAlreadyStaged, http.StatusConflict},
		{errors.New("database is down"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		relayError(c, tc.err)
		require.Equal(t, tc.status, w.Code, tc.err.Error())
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	relayError(c, &master.KeyNotDeliveredError{NodeIDs: []int64{4, 9}})
	require.Equal(t, http.StatusConflict, w.Code)
	var body struct {
		Reason   string            `json:"reason"`
		Metadata map[string]string `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "RELAY_KEY_NOT_DELIVERED", body.Reason)
	require.Equal(t, "4,9", body.Metadata["node_ids"], "the admin page shows which nodes are holding up the rotation")
}

func newRelayTestRouter(t *testing.T) (*gin.Engine, *master.MemoryStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := master.NewMemoryStore()
	rt := master.NewRuntime(master.RuntimeDeps{
		Config:   &config.Config{Relay: config.RelayConfig{NodeRole: config.RelayNodeRoleMaster}},
		Store:    store,
		Settings: newTestSettingRepo(),
	})
	rt.Init(context.Background())
	t.Cleanup(rt.Close)
	h := NewRelayHandler(rt)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Next()
	})
	g := r.Group("/relay")
	g.GET("/status", h.GetStatus)
	g.PUT("/general-config", h.UpdateGeneralConfig)
	g.GET("/nodes", h.ListNodes)
	g.POST("/nodes/:id/reject", h.RejectNode)
	g.POST("/nodes/:id/activate", h.ActivateNode)
	g.GET("/keys/:purpose", h.ListKeys)
	g.POST("/nodes/:id/reclaim-quota", h.ReclaimNodeQuota)
	g.POST("/users/:id/reclaim-quota", h.ReclaimUserQuota)
	g.POST("/keys/:purpose/:version/activate", h.ActivateKey)
	return r, store
}

func relayDo(r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.7:5555"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRelayHandlerWhileRelayIsOff(t *testing.T) {
	r, store := newRelayTestRouter(t)

	w := relayDo(r, http.MethodGet, "/relay/status", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"state":"off"`)

	// 节点列表开关关着也能看，字段是 snake_case 的视图（不直接暴露内部结构）。
	_, err := store.CreatePending(context.Background(), &master.Node{Name: "tokyo-1", IdentityFingerprint: "fp", IdentityPublicKey: []byte{1}}, 20)
	require.NoError(t, err)
	w = relayDo(r, http.MethodGet, "/relay/nodes", nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"identity_fingerprint":"fp"`)
	require.NotContains(t, w.Body.String(), "IdentityPublicKey")

	// 节点操作要主从分流在运行。
	w = relayDo(r, http.MethodPost, "/relay/nodes/1/reject", nil)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "RELAY_NOT_RUNNING")

	w = relayDo(r, http.MethodPost, "/relay/nodes/abc/reject", nil)
	require.Equal(t, http.StatusBadRequest, w.Code)
	w = relayDo(r, http.MethodPost, "/relay/users/x/reclaim-quota", nil)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "Invalid user ID")
	for _, path := range []string{"/relay/users/9/reclaim-quota", "/relay/nodes/1/reclaim-quota"} {
		w = relayDo(r, http.MethodPost, path, nil)
		require.Equal(t, http.StatusConflict, w.Code, path)
		require.Contains(t, w.Body.String(), "RELAY_NOT_RUNNING")
	}
	w = relayDo(r, http.MethodPost, "/relay/keys/ticket/0/activate", nil)
	require.Equal(t, http.StatusBadRequest, w.Code)
	w = relayDo(r, http.MethodPost, "/relay/keys/tls/1/activate", nil)
	require.Equal(t, http.StatusBadRequest, w.Code, "unknown purpose")
	w = relayDo(r, http.MethodGet, "/relay/keys/voucher", nil)
	require.Equal(t, http.StatusConflict, w.Code, "keys are listed only while relay is running")
	w = relayDo(r, http.MethodPost, "/relay/nodes/1/activate", map[string]any{"public_domain": "r.example.com"})
	require.Equal(t, http.StatusBadRequest, w.Code, "the fingerprint must be confirmed")

	// 通用配置：校验失败 400；成功后写审计，带操作人和来源 IP。
	w = relayDo(r, http.MethodPut, "/relay/general-config", map[string]any{"master_ratio_percent": 150})
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	w = relayDo(r, http.MethodPut, "/relay/general-config", map[string]any{"master_ratio_percent": 0})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"master_ratio_percent":0`)
	audits := store.Audits()
	last := audits[len(audits)-1]
	require.Equal(t, master.AuditGeneralConfigChanged, last.Action)
	require.Equal(t, int64(42), last.ActorUserID)
	require.Equal(t, "203.0.113.7", last.SourceIP)
}

func TestLogQueryFromRequestValidatesAndCapsTheQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parse := func(rawQuery string) (*relayv1.LogQuery, int) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/logs?"+rawQuery, nil)
		q, ok := logQueryFromRequest(c)
		if !ok {
			return nil, w.Code
		}
		return q, http.StatusOK
	}

	q, code := parse("kind=error&level=P1&request_id=r1&user_id=7&api_key_id=3&account_id=9&platform=openai&model=gpt-5&keyword=boom&from=1000&to=2000&before=1500&limit=10")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "error", q.Kind)
	require.EqualValues(t, 7, q.UserId)
	require.EqualValues(t, 1500, q.BeforeUnixMs)
	require.EqualValues(t, 10, q.Limit)
	require.EqualValues(t, master.MaxLogBytes, q.MaxBytes)

	q, _ = parse("")
	require.Equal(t, "app", q.Kind)
	require.EqualValues(t, 50, q.Limit)

	q, _ = parse("limit=100000")
	require.EqualValues(t, master.MaxLogRecords, q.Limit, "the page size is capped")

	// 节点详情页"最近日志"：程序日志、200 行、256KB。
	q, _ = parse("kind=error&tail=1&limit=5")
	require.Equal(t, "app", q.Kind)
	require.EqualValues(t, master.RecentLogRecords, q.Limit)
	require.EqualValues(t, master.RecentLogBytes, q.MaxBytes)

	for _, bad := range []string{"kind=secrets", "user_id=abc", "limit=0", "from=-1", "page=x"} {
		_, code := parse(bad)
		require.Equal(t, http.StatusBadRequest, code, bad)
	}
}
