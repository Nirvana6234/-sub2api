package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// RemotePairingHeader carries the phone's pairing token. Being signed in to the
// account is not enough to reach a computer; the phone must also hold the
// token of a pairing the user confirmed on that computer.
const RemotePairingHeader = "X-Remote-Pairing"

const (
	remoteAgentReadLimit    = 4 << 20
	remoteAgentWriteTimeout = 10 * time.Second
	remoteAgentPingPeriod   = 30 * time.Second
	remoteAgentReadTimeout  = 75 * time.Second
	remoteCommandBodyLimit  = 64 << 10
	remoteStreamHeartbeat   = 15 * time.Second
)

// The assistant is a desktop program and sends no Origin. A browser always
// does; refusing it keeps a web page from opening this socket at all.
var remoteUpgrader = websocket.Upgrader{
	ReadBufferSize:  16 << 10,
	WriteBufferSize: 16 << 10,
	CheckOrigin:     func(r *http.Request) bool { return r.Header.Get("Origin") == "" },
}

// RemoteHandler serves phone ↔ desktop session sync.
type RemoteHandler struct {
	svc *service.RemoteSyncService
}

func NewRemoteHandler(svc *service.RemoteSyncService) *RemoteHandler {
	return &RemoteHandler{svc: svc}
}

// ---- Assistant side ----------------------------------------------------------

type remoteStartPairingRequest struct {
	DeviceID   string `json:"device_id" binding:"required"`
	DeviceName string `json:"device_name"`
}

// StartPairing issues a code for the computer to show.
// POST /api/v1/remote/pair/start
func (h *RemoteHandler) StartPairing(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	var req remoteStartPairingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "device_id is required")
		return
	}
	start, err := h.svc.StartPairing(c.Request.Context(), subject.UserID, req.DeviceID, req.DeviceName)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"pairing_id": start.PairingID, "code": start.Code, "expires_at": start.ExpiresAt})
}

// Agent upgrades to the assistant's long-lived connection.
// GET /api/v1/remote/agent?device_id=...
func (h *RemoteHandler) Agent(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	deviceID := c.Query("device_id")
	if err := service.ValidateRemoteDeviceID(deviceID); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	ws, err := remoteUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	conn := &remoteWSConn{ws: ws}
	ws.SetReadLimit(remoteAgentReadLimit)

	hub := h.svc.Hub()
	session := hub.Attach(subject.UserID, deviceID, conn)
	defer session.Close()
	h.svc.AgentConnected(c.Request.Context(), subject.UserID, deviceID)

	// The connection must not outlive the token it was opened with.
	if expires, ok := c.Get(middleware2.ContextKeyTokenExpiresAt); ok {
		if at, ok := expires.(time.Time); ok {
			timer := time.AfterFunc(time.Until(at), func() {
				conn.closeWith(websocket.ClosePolicyViolation, "token expired")
				session.Close()
			})
			defer timer.Stop()
		}
	}

	go conn.keepAlive(session.Done())

	_ = ws.SetReadDeadline(time.Now().Add(remoteAgentReadTimeout))
	ws.SetPongHandler(func(string) error {
		return ws.SetReadDeadline(time.Now().Add(remoteAgentReadTimeout))
	})
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		_ = ws.SetReadDeadline(time.Now().Add(remoteAgentReadTimeout))

		frame, err := session.HandleFrame(data)
		if err != nil || frame == nil {
			continue
		}
		if err := h.svc.HandleAgentFrame(c.Request.Context(), subject.UserID, deviceID, frame); err != nil {
			body, _ := json.Marshal(gin.H{"kind": "error", "pairing_id": frame.PairingID, "body": gin.H{"message": err.Error()}})
			_ = conn.Send(body)
		}
	}
}

// remoteWSConn serialises writes: gorilla allows one writer at a time.
type remoteWSConn struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func (c *remoteWSConn) Send(frame []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(remoteAgentWriteTimeout))
	return c.ws.WriteMessage(websocket.TextMessage, frame)
}

func (c *remoteWSConn) Close() error { return c.ws.Close() }

func (c *remoteWSConn) closeWith(code int, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
}

func (c *remoteWSConn) keepAlive(done <-chan struct{}) {
	ticker := time.NewTicker(remoteAgentPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			c.mu.Lock()
			err := c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(remoteAgentWriteTimeout))
			c.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

// ---- Phone side --------------------------------------------------------------

type remoteClaimRequest struct {
	Code       string `json:"code" binding:"required"`
	PhoneLabel string `json:"phone_label"`
	PublicKey  string `json:"public_key" binding:"required"`
}

// ClaimPairing takes a code shown on a computer.
// POST /api/v1/remote/pair/claim
func (h *RemoteHandler) ClaimPairing(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	var req remoteClaimRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "code and public_key are required")
		return
	}
	claim, err := h.svc.ClaimPairing(c.Request.Context(), subject.UserID, req.Code, req.PhoneLabel, req.PublicKey)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"pairing_id":  claim.PairingID,
		"device_id":   claim.DeviceID,
		"device_name": claim.DeviceName,
		"token":       claim.Token,
		"status":      service.RemotePairingClaimed,
	})
}

// PairingStatus lets the phone wait for the computer's confirmation.
// GET /api/v1/remote/pairings/:id
func (h *RemoteHandler) PairingStatus(c *gin.Context) {
	subject, id, ok := remotePairingParams(c)
	if !ok {
		return
	}
	status, err := h.svc.PairingStatus(c.Request.Context(), subject.UserID, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"pairing_id": id, "status": status})
}

// RevokePairing ends a pairing; either side of the account may.
// DELETE /api/v1/remote/pairings/:id
func (h *RemoteHandler) RevokePairing(c *gin.Context) {
	subject, id, ok := remotePairingParams(c)
	if !ok {
		return
	}
	if err := h.svc.RevokePairing(c.Request.Context(), subject.UserID, id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"pairing_id": id, "status": service.RemotePairingRevoked})
}

// ListDevices lists the account's paired computers and whether they are online.
// GET /api/v1/remote/devices
func (h *RemoteHandler) ListDevices(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	devices, err := h.svc.ListDevices(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]gin.H, 0, len(devices))
	for _, device := range devices {
		pairings := make([]gin.H, 0, len(device.Pairings))
		for _, p := range device.Pairings {
			pairings = append(pairings, gin.H{"pairing_id": p.ID, "phone_label": p.PhoneLabel, "confirmed_at": p.ConfirmedAt, "last_used_at": p.LastUsedAt})
		}
		out = append(out, gin.H{
			"device_id":   device.DeviceID,
			"device_name": device.DeviceName,
			"online":      device.Online,
			"status":      device.Status,
			"pairings":    pairings,
		})
	}
	response.Success(c, out)
}

// Command relays one phone request to the computer and returns its answer
// verbatim. The server does not interpret it; the assistant decides.
// POST /api/v1/remote/devices/:device_id/cmd
func (h *RemoteHandler) Command(c *gin.Context) {
	pairing, ok := h.phonePairing(c)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, remoteCommandBodyLimit))
	if err != nil {
		response.BadRequest(c, "command body is too large or unreadable")
		return
	}
	result, err := h.svc.Forward(c.Request.Context(), pairing, body)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", result)
}

// Stream follows one conversation for as long as the phone keeps this open.
// It is not a notification channel: nothing is kept for a phone that is away.
// GET /api/v1/remote/devices/:device_id/sessions/:thread_id/stream?cursor=
func (h *RemoteHandler) Stream(c *gin.Context) {
	pairing, ok := h.phonePairing(c)
	if !ok {
		return
	}
	events, stop, err := h.svc.Subscribe(pairing, c.Param("thread_id"), c.Query("cursor"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	defer stop()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	flusher, _ := c.Writer.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	flush()

	heartbeat := time.NewTicker(remoteStreamHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := io.WriteString(c.Writer, ": ping\n\n"); err != nil {
				return
			}
			flush()
		case event, open := <-events:
			if !open {
				// The computer went away or the phone fell behind; either way it
				// reconnects with its last cursor.
				_, _ = io.WriteString(c.Writer, "event: end\ndata: {}\n\n")
				flush()
				return
			}
			if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", event); err != nil {
				return
			}
			flush()
		}
	}
}

func (h *RemoteHandler) phonePairing(c *gin.Context) (*service.RemotePairing, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return nil, false
	}
	pairing, err := h.svc.AuthenticatePhone(c.Request.Context(), subject.UserID, c.Param("device_id"), c.GetHeader(RemotePairingHeader))
	if err != nil {
		response.ErrorFrom(c, err)
		return nil, false
	}
	return pairing, true
}

func remotePairingParams(c *gin.Context) (middleware2.AuthSubject, int64, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return subject, 0, false
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid pairing id")
		return subject, 0, false
	}
	return subject, id, true
}
