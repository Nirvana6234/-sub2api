package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// End to end over real HTTP and a real WebSocket: an assistant connects, a
// phone pairs, the computer confirms, then a command and a stream go through.

type remoteTestRepo struct {
	mu   sync.Mutex
	rows []*service.RemotePairing
}

func (r *remoteTestRepo) find(match func(*service.RemotePairing) bool) *service.RemotePairing {
	for _, row := range r.rows {
		if match(row) {
			return row
		}
	}
	return nil
}

func (r *remoteTestRepo) Create(_ context.Context, p *service.RemotePairing) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p.ID = int64(len(r.rows) + 1)
	copied := *p
	r.rows = append(r.rows, &copied)
	return nil
}

func (r *remoteTestRepo) GetForUser(_ context.Context, userID, id int64) (*service.RemotePairing, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if row := r.find(func(p *service.RemotePairing) bool { return p.ID == id && p.UserID == userID }); row != nil {
		copied := *row
		return &copied, nil
	}
	return nil, service.ErrRemotePairingNotFound
}

func (r *remoteTestRepo) FindPendingByCode(_ context.Context, userID int64, codeHash string, now time.Time) (*service.RemotePairing, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if row := r.find(func(p *service.RemotePairing) bool {
		return p.UserID == userID && p.Status == service.RemotePairingPending && p.CodeHash == codeHash && p.CodeExpiresAt.After(now)
	}); row != nil {
		copied := *row
		return &copied, nil
	}
	return nil, service.ErrRemotePairingNotFound
}

func (r *remoteTestRepo) RecordFailedClaim(context.Context, int64, int, time.Time) error { return nil }

func (r *remoteTestRepo) RevokeOpenForDevice(context.Context, int64, string, time.Time) error {
	return nil
}

func (r *remoteTestRepo) Claim(_ context.Context, id int64, label, key, tokenHash string, now time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row := r.find(func(p *service.RemotePairing) bool { return p.ID == id && p.Status == service.RemotePairingPending })
	if row == nil {
		return false, nil
	}
	row.Status, row.PhoneLabel, row.PhonePublicKey, row.TokenHash = service.RemotePairingClaimed, label, key, tokenHash
	return true, nil
}

func (r *remoteTestRepo) Confirm(_ context.Context, userID int64, deviceID string, id int64, now time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row := r.find(func(p *service.RemotePairing) bool {
		return p.ID == id && p.UserID == userID && p.DeviceID == deviceID && p.Status == service.RemotePairingClaimed
	})
	if row == nil {
		return false, nil
	}
	row.Status = service.RemotePairingActive
	return true, nil
}

func (r *remoteTestRepo) Revoke(_ context.Context, userID, id int64, now time.Time) (*service.RemotePairing, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row := r.find(func(p *service.RemotePairing) bool { return p.ID == id && p.UserID == userID })
	if row == nil {
		return nil, service.ErrRemotePairingNotFound
	}
	row.Status, row.TokenHash = service.RemotePairingRevoked, ""
	copied := *row
	return &copied, nil
}

func (r *remoteTestRepo) FindActiveByTokenHash(_ context.Context, tokenHash string) (*service.RemotePairing, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if row := r.find(func(p *service.RemotePairing) bool { return p.TokenHash != "" && p.TokenHash == tokenHash }); row != nil {
		copied := *row
		return &copied, nil
	}
	return nil, service.ErrRemotePairingNotFound
}

func (r *remoteTestRepo) ListClaimedForDevice(context.Context, int64, string) ([]service.RemotePairing, error) {
	return nil, nil
}

func (r *remoteTestRepo) ListActiveForUser(_ context.Context, userID int64) ([]service.RemotePairing, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []service.RemotePairing
	for _, row := range r.rows {
		if row.UserID == userID && row.Status == service.RemotePairingActive {
			out = append(out, *row)
		}
	}
	return out, nil
}

func (r *remoteTestRepo) TouchLastUsed(context.Context, int64, time.Time) error { return nil }

const remoteTestDevice = "device-0001"

// newRemoteTestServer stands in the real routes behind a fake login that
// authenticates as the user named in X-Test-User.
func newRemoteTestServer(t *testing.T, tokenLifetime time.Duration) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewRemoteHandler(service.NewRemoteSyncService(&remoteTestRepo{}, service.NewRemoteHub()))

	r := gin.New()
	remote := r.Group("/api/v1/remote", func(c *gin.Context) {
		user := int64(7)
		if c.GetHeader("X-Test-User") == "8" {
			user = 8
		}
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: user})
		c.Set(middleware2.ContextKeyTokenExpiresAt, time.Now().Add(tokenLifetime))
		c.Next()
	})
	remote.GET("/agent", h.Agent)
	remote.GET("/devices/:device_id/sessions/:thread_id/stream", h.Stream)
	remote.POST("/pair/start", h.StartPairing)
	remote.POST("/pair/claim", h.ClaimPairing)
	remote.GET("/devices", h.ListDevices)
	remote.GET("/pairings/:id", h.PairingStatus)
	remote.POST("/devices/:device_id/cmd", h.Command)

	server := httptest.NewServer(r)
	t.Cleanup(server.Close)
	return server
}

func connectAgent(t *testing.T, server *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/remote/agent?device_id=" + remoteTestDevice
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

func postJSON(t *testing.T, url string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var out bytes.Buffer
	_, _ = out.ReadFrom(resp.Body)
	return resp.StatusCode, out.Bytes()
}

type agentFrame struct {
	ID           string          `json:"id"`
	Kind         string          `json:"kind"`
	Type         string          `json:"type"`
	PairingID    int64           `json:"pairing_id"`
	Subscription string          `json:"subscription"`
	Body         json.RawMessage `json:"body"`
}

func readFrame(t *testing.T, ws *websocket.Conn) agentFrame {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var frame agentFrame
	require.NoError(t, ws.ReadJSON(&frame))
	return frame
}

// pair runs the whole pairing ceremony and returns the phone's token.
func pair(t *testing.T, server *httptest.Server, agent *websocket.Conn) string {
	t.Helper()
	status, body := postJSON(t, server.URL+"/api/v1/remote/pair/start", map[string]string{"device_id": remoteTestDevice}, nil)
	require.Equal(t, http.StatusOK, status, string(body))
	var start struct {
		Data struct {
			Code string `json:"code"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &start))

	key := base64.StdEncoding.EncodeToString([]byte("spki"))
	status, body = postJSON(t, server.URL+"/api/v1/remote/pair/claim", map[string]string{"code": start.Data.Code, "phone_label": "iPhone", "public_key": key}, nil)
	require.Equal(t, http.StatusOK, status, string(body))
	var claim struct {
		Data struct {
			PairingID int64  `json:"pairing_id"`
			Token     string `json:"token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &claim))

	request := readFrame(t, agent)
	require.Equal(t, "pair.request", request.Type)
	require.NoError(t, agent.WriteJSON(map[string]any{"kind": "pair.confirm", "pairing_id": claim.Data.PairingID}))

	// The confirmation travels over the socket; wait for it the way the phone does.
	require.Eventually(t, func() bool {
		resp, err := http.Get(server.URL + "/api/v1/remote/pairings/" + strconv.FormatInt(claim.Data.PairingID, 10))
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		var status struct {
			Data struct {
				Status string `json:"status"`
			} `json:"data"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&status)
		return status.Data.Status == string(service.RemotePairingActive)
	}, 3*time.Second, 20*time.Millisecond)
	return claim.Data.Token
}

func TestRemoteHandler_ACommandRoundTripsThroughTheComputer(t *testing.T) {
	server := newRemoteTestServer(t, time.Hour)
	agent := connectAgent(t, server)
	token := pair(t, server, agent)

	type result struct {
		status int
		body   []byte
	}
	done := make(chan result, 1)
	go func() {
		status, body := postJSON(t, server.URL+"/api/v1/remote/devices/"+remoteTestDevice+"/cmd",
			map[string]string{"type": "message.send", "thread_id": "t1", "text": "hi"}, map[string]string{RemotePairingHeader: token})
		done <- result{status, body}
	}()

	_ = agent.SetReadDeadline(time.Now().Add(3 * time.Second))
	cmd := readFrame(t, agent)
	require.Equal(t, "cmd", cmd.Kind)
	require.NotZero(t, cmd.PairingID)
	require.JSONEq(t, `{"type":"message.send","thread_id":"t1","text":"hi"}`, string(cmd.Body))
	require.NoError(t, agent.WriteJSON(map[string]any{"kind": "result", "id": cmd.ID, "body": map[string]any{"queued": false}}))

	got := <-done
	require.Equal(t, http.StatusOK, got.status)
	require.JSONEq(t, `{"queued":false}`, string(got.body))
}

func TestRemoteHandler_ASignedInPhoneWithoutAPairingIsRefused(t *testing.T) {
	server := newRemoteTestServer(t, time.Hour)
	connectAgent(t, server)

	status, _ := postJSON(t, server.URL+"/api/v1/remote/devices/"+remoteTestDevice+"/cmd",
		map[string]string{"type": "sessions.list"}, map[string]string{RemotePairingHeader: "made-up"})

	require.Equal(t, http.StatusForbidden, status)
}

func TestRemoteHandler_AnotherAccountCannotUseTheToken(t *testing.T) {
	server := newRemoteTestServer(t, time.Hour)
	agent := connectAgent(t, server)
	token := pair(t, server, agent)

	status, _ := postJSON(t, server.URL+"/api/v1/remote/devices/"+remoteTestDevice+"/cmd",
		map[string]string{"type": "sessions.list"}, map[string]string{RemotePairingHeader: token, "X-Test-User": "8"})

	require.Equal(t, http.StatusForbidden, status)
}

func TestRemoteHandler_TheStreamCarriesTheComputersEvents(t *testing.T) {
	server := newRemoteTestServer(t, time.Hour)
	agent := connectAgent(t, server)
	token := pair(t, server, agent)

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/remote/devices/"+remoteTestDevice+"/sessions/t1/stream?cursor=42", nil)
	req.Header.Set(RemotePairingHeader, token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	subscribe := readFrame(t, agent)
	require.Equal(t, "subscribe", subscribe.Kind)
	require.JSONEq(t, `{"type":"session.subscribe","thread_id":"t1","cursor":"42"}`, string(subscribe.Body))
	require.NoError(t, agent.WriteJSON(map[string]any{"kind": "event", "subscription": subscribe.Subscription, "body": map[string]any{"seq": 43}}))

	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "data: {\"seq\":43}\n", line)

	// The phone leaving ends the subscription on the computer.
	_ = resp.Body.Close()
	require.Equal(t, "unsubscribe", readFrame(t, agent).Kind)
}

// A browser page must not be able to open the assistant socket.
func TestRemoteHandler_TheAgentSocketRefusesBrowsers(t *testing.T) {
	server := newRemoteTestServer(t, time.Hour)
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/remote/agent?device_id=" + remoteTestDevice

	_, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": []string{"https://evil.example"}})

	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// The socket is checked once, at the handshake. It must not outlive the token.
func TestRemoteHandler_TheAgentSocketClosesWhenItsTokenExpires(t *testing.T) {
	server := newRemoteTestServer(t, 300*time.Millisecond)
	agent := connectAgent(t, server)

	_ = agent.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err := agent.ReadMessage()

	var closeErr *websocket.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, websocket.ClosePolicyViolation, closeErr.Code)
}
