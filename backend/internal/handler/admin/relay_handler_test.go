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
