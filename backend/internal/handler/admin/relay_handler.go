package admin

import (
	"errors"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/relay/keystore"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// RelayHandler 是从节点管理页的后台接口（设计 11.5）：总开关、通用配置、节点激活与停用、
// 根证书分阶段轮换。所有改动类接口在路由上要求二次验证（stepUpAuth），并由主节点记审计。
type RelayHandler struct {
	runtime *master.Runtime
}

// NewRelayHandler 创建主从分流管理处理器。
func NewRelayHandler(runtime *master.Runtime) *RelayHandler {
	return &RelayHandler{runtime: runtime}
}

// RelayNodeView 是管理页上的一台从节点。
type RelayNodeView struct {
	ID                  int64             `json:"id"`
	Name                string            `json:"name"`
	Hostname            string            `json:"hostname"`
	Region              string            `json:"region"`
	PublicDomain        string            `json:"public_domain"`
	Status              master.NodeStatus `json:"status"`
	IdentityFingerprint string            `json:"identity_fingerprint"`
	RegisteredIP        string            `json:"registered_ip"`
	ProgramVersion      string            `json:"program_version"`
	SystemInfo          map[string]string `json:"system_info,omitempty"`
	BandwidthLimitMbps  int               `json:"bandwidth_limit_mbps"`
	AllowMultiIP        bool              `json:"allow_multi_ip"`
	ActivatedAt         *time.Time        `json:"activated_at,omitempty"`
	ActivatedBy         *int64            `json:"activated_by,omitempty"`
	LastSeenAt          *time.Time        `json:"last_seen_at,omitempty"`
	LastSeenIP          string            `json:"last_seen_ip"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

func relayNodeView(n *master.Node) RelayNodeView {
	return RelayNodeView{
		ID: n.ID, Name: n.Name, Hostname: n.Hostname, Region: n.Region, PublicDomain: n.PublicDomain,
		Status: n.Status, IdentityFingerprint: n.IdentityFingerprint, RegisteredIP: n.RegisteredIP,
		ProgramVersion: n.ProgramVersion, SystemInfo: n.SystemInfo, BandwidthLimitMbps: n.BandwidthLimitMbps,
		AllowMultiIP: n.AllowMultiIP, ActivatedAt: n.ActivatedAt, ActivatedBy: n.ActivatedBy,
		LastSeenAt: n.LastSeenAt, LastSeenIP: n.LastSeenIP, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
	}
}

// RelayStatusResponse 是总开关与运行状态。
type RelayStatusResponse struct {
	Enabled bool                 `json:"enabled"`
	Runtime master.RuntimeStatus `json:"runtime"`
}

// SetRelayEnabledRequest 打开或关闭主从分流。
type SetRelayEnabledRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

// ActivateRelayNodeRequest 核对指纹后激活节点（设计 11.1 第 5 步）。
type ActivateRelayNodeRequest struct {
	Fingerprint        string `json:"fingerprint" binding:"required"`
	Name               string `json:"name"`
	PublicDomain       string `json:"public_domain" binding:"required"`
	BandwidthLimitMbps int    `json:"bandwidth_limit_mbps" binding:"min=0"`
	Region             string `json:"region"`
}

// RevokeRelayNodeRequest 吊销节点证书的原因。
type RevokeRelayNodeRequest struct {
	Reason string `json:"reason" binding:"required,max=500"`
}

// SetRelayNodeMultiIPRequest 单独打开或关闭"同时两个 IP"判断。
type SetRelayNodeMultiIPRequest struct {
	Allow *bool `json:"allow" binding:"required"`
}

// relayActor 取当前管理员，并把来源 IP 放进 ctx 供审计使用。
func relayActor(c *gin.Context) (int64, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return 0, false
	}
	c.Request = c.Request.WithContext(master.WithSourceIP(c.Request.Context(), middleware2.SecurityClientIP(c)))
	return subject.UserID, true
}

func relayNodeID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid node ID")
		return 0, false
	}
	return id, true
}

func relayRootVersion(c *gin.Context) (int, bool) {
	v, err := strconv.Atoi(c.Param("version"))
	if err != nil || v <= 0 {
		response.BadRequest(c, "Invalid root version")
		return 0, false
	}
	return v, true
}

// relayError 把主从分流的业务错误映射成 4xx；其余按内部错误处理。
func relayError(c *gin.Context, err error) {
	var notDelivered *master.RootNotDeliveredError
	switch {
	case errors.As(err, &notDelivered):
		ids := make([]string, 0, len(notDelivered.NodeIDs))
		for _, id := range notDelivered.NodeIDs {
			ids = append(ids, strconv.FormatInt(id, 10))
		}
		err = infraerrors.Conflict("RELAY_ROOT_NOT_DELIVERED", err.Error()).
			WithMetadata(map[string]string{"node_ids": strings.Join(ids, ",")})
	case errors.Is(err, master.ErrNodeNotFound), errors.Is(err, master.ErrRootNotFound):
		err = infraerrors.NotFound("RELAY_NOT_FOUND", err.Error())
	case errors.Is(err, master.ErrFingerprintMismatch), errors.Is(err, master.ErrDomainRequired),
		errors.Is(err, master.ErrInvalidGeneralConfig):
		err = infraerrors.BadRequest("RELAY_INVALID_REQUEST", err.Error())
	case errors.Is(err, master.ErrRelayNotRunning):
		err = infraerrors.Conflict("RELAY_NOT_RUNNING", err.Error())
	case errors.Is(err, master.ErrNodesStillServing):
		err = infraerrors.Conflict("RELAY_NODES_STILL_SERVING", err.Error())
	case errors.Is(err, master.ErrStatusConflict), errors.Is(err, master.ErrDomainTaken),
		errors.Is(err, master.ErrRootNotStaged), errors.Is(err, master.ErrRootInUse),
		errors.Is(err, master.ErrRootRetireTooEarly), errors.Is(err, keystore.ErrAlreadyStaged):
		err = infraerrors.Conflict("RELAY_CONFLICT", err.Error())
	}
	response.ErrorFrom(c, err)
}

// GetStatus 返回总开关与运行状态。
// GET /api/v1/admin/relay/status
func (h *RelayHandler) GetStatus(c *gin.Context) {
	enabled, err := h.runtime.Enabled(c.Request.Context())
	if err != nil {
		relayError(c, err)
		return
	}
	response.Success(c, RelayStatusResponse{Enabled: enabled, Runtime: h.runtime.Status()})
}

// SetEnabled 打开或关闭主从分流，当场生效。关闭前要求没有已激活或排空中的节点。
// PUT /api/v1/admin/relay/enabled
func (h *RelayHandler) SetEnabled(c *gin.Context) {
	var req SetRelayEnabledRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	actor, ok := relayActor(c)
	if !ok {
		return
	}
	st, err := h.runtime.SetEnabled(c.Request.Context(), actor, *req.Enabled)
	if err != nil {
		relayError(c, err)
		return
	}
	response.Success(c, RelayStatusResponse{Enabled: *req.Enabled, Runtime: st})
}

// GetGeneralConfig 返回通用配置（含默认值）。
// GET /api/v1/admin/relay/general-config
func (h *RelayHandler) GetGeneralConfig(c *gin.Context) {
	g, err := h.runtime.GeneralConfig(c.Request.Context())
	if err != nil {
		relayError(c, err)
		return
	}
	response.Success(c, g)
}

// UpdateGeneralConfig 保存通用配置（整份替换，未填的项取默认值）。
// PUT /api/v1/admin/relay/general-config
func (h *RelayHandler) UpdateGeneralConfig(c *gin.Context) {
	var req master.GeneralConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	actor, ok := relayActor(c)
	if !ok {
		return
	}
	g, err := h.runtime.SetGeneralConfig(c.Request.Context(), actor, req)
	if err != nil {
		relayError(c, err)
		return
	}
	response.Success(c, g)
}

// ListNodes 列出所有从节点（开关关闭时也能看）。
// GET /api/v1/admin/relay/nodes
func (h *RelayHandler) ListNodes(c *gin.Context) {
	nodes, err := h.runtime.ListNodes(c.Request.Context())
	if err != nil {
		relayError(c, err)
		return
	}
	out := make([]RelayNodeView, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, relayNodeView(n))
	}
	response.Success(c, out)
}

// ActivateNode 核对指纹后激活待激活节点。
// POST /api/v1/admin/relay/nodes/:id/activate
func (h *RelayHandler) ActivateNode(c *gin.Context) {
	id, ok := relayNodeID(c)
	if !ok {
		return
	}
	var req ActivateRelayNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	actor, ok := relayActor(c)
	if !ok {
		return
	}
	err := h.runtime.ActivateNode(c.Request.Context(), id, req.Fingerprint, master.Activation{
		Name:               strings.TrimSpace(req.Name),
		PublicDomain:       req.PublicDomain,
		BandwidthLimitMbps: req.BandwidthLimitMbps,
		Region:             strings.TrimSpace(req.Region),
		ActorUserID:        actor,
	})
	if err != nil {
		relayError(c, err)
		return
	}
	response.Success(c, nil)
}

// nodeAction 处理只需要节点 ID 和操作人的节点操作。
func (h *RelayHandler) nodeAction(c *gin.Context, op func(c *gin.Context, id, actor int64) error) {
	id, ok := relayNodeID(c)
	if !ok {
		return
	}
	actor, ok := relayActor(c)
	if !ok {
		return
	}
	if err := op(c, id, actor); err != nil {
		relayError(c, err)
		return
	}
	response.Success(c, nil)
}

// RejectNode 拒绝待激活节点。
// POST /api/v1/admin/relay/nodes/:id/reject
func (h *RelayHandler) RejectNode(c *gin.Context) {
	h.nodeAction(c, func(c *gin.Context, id, actor int64) error {
		return h.runtime.RejectNode(c.Request.Context(), id, actor)
	})
}

// DisableNode 停用节点（吊销证书、断开连接）。
// POST /api/v1/admin/relay/nodes/:id/disable
func (h *RelayHandler) DisableNode(c *gin.Context) {
	h.nodeAction(c, func(c *gin.Context, id, actor int64) error {
		return h.runtime.DisableNode(c.Request.Context(), id, actor)
	})
}

// EnableNode 让停用的节点回到待激活。
// POST /api/v1/admin/relay/nodes/:id/enable
func (h *RelayHandler) EnableNode(c *gin.Context) {
	h.nodeAction(c, func(c *gin.Context, id, actor int64) error {
		return h.runtime.EnableNode(c.Request.Context(), id, actor)
	})
}

// RevokeNode 因怀疑被攻破吊销节点证书（退回待激活）。
// POST /api/v1/admin/relay/nodes/:id/revoke
func (h *RelayHandler) RevokeNode(c *gin.Context) {
	var req RevokeRelayNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	h.nodeAction(c, func(c *gin.Context, id, actor int64) error {
		return h.runtime.RevokeNode(c.Request.Context(), id, actor, strings.TrimSpace(req.Reason))
	})
}

// SetNodeAllowMultiIP 对这台单独打开或关闭"同时两个 IP"判断。
// PUT /api/v1/admin/relay/nodes/:id/allow-multi-ip
func (h *RelayHandler) SetNodeAllowMultiIP(c *gin.Context) {
	var req SetRelayNodeMultiIPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	h.nodeAction(c, func(c *gin.Context, id, actor int64) error {
		return h.runtime.SetNodeAllowMultiIP(c.Request.Context(), id, actor, *req.Allow)
	})
}

// RejectAllPendingNodes 一键拒绝所有待激活节点。
// POST /api/v1/admin/relay/nodes/reject-pending
func (h *RelayHandler) RejectAllPendingNodes(c *gin.Context) {
	actor, ok := relayActor(c)
	if !ok {
		return
	}
	count, err := h.runtime.RejectAllPendingNodes(c.Request.Context(), actor)
	if err != nil {
		relayError(c, err)
		return
	}
	response.Success(c, gin.H{"rejected": count})
}

// ListRoots 列出未停用的主从通信根证书版本；预备版本带上还没收到它的节点。
// GET /api/v1/admin/relay/roots
func (h *RelayHandler) ListRoots(c *gin.Context) {
	roots, err := h.runtime.Roots(c.Request.Context())
	if err != nil {
		relayError(c, err)
		return
	}
	response.Success(c, roots)
}

// StageRoot 根证书轮换第一步：生成预备根证书，指纹随配置推给所有节点。
// POST /api/v1/admin/relay/roots/stage
func (h *RelayHandler) StageRoot(c *gin.Context) {
	actor, ok := relayActor(c)
	if !ok {
		return
	}
	info, err := h.runtime.StageRoot(c.Request.Context(), actor)
	if err != nil {
		relayError(c, err)
		return
	}
	response.Success(c, info)
}

// ActivateRoot 根证书轮换第二步：切换到预备根证书签发。有在服务的节点还没收到指纹时 409。
// POST /api/v1/admin/relay/roots/:version/activate
func (h *RelayHandler) ActivateRoot(c *gin.Context) {
	version, ok := relayRootVersion(c)
	if !ok {
		return
	}
	actor, ok := relayActor(c)
	if !ok {
		return
	}
	if err := h.runtime.ActivateRoot(c.Request.Context(), actor, version); err != nil {
		relayError(c, err)
		return
	}
	response.Success(c, nil)
}

// RetireRoot 根证书轮换第三步：停用旧根（新根签发满 25 小时后），或放弃预备根。
// POST /api/v1/admin/relay/roots/:version/retire
func (h *RelayHandler) RetireRoot(c *gin.Context) {
	version, ok := relayRootVersion(c)
	if !ok {
		return
	}
	actor, ok := relayActor(c)
	if !ok {
		return
	}
	if err := h.runtime.RetireRoot(c.Request.Context(), actor, version); err != nil {
		relayError(c, err)
		return
	}
	response.Success(c, nil)
}
