package nodegw

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/relay/keycodec"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// 小白端转发接口（/api/v1/paw/*，设计 8.1、8.3）：从节点用中转票据代替登录态（JWT）。票据的签名、有效期、节点、吊销在本地验；
// 之后每个请求向主节点解析（PawResolve）：主节点复查用户（启用、token_version、后台模式）、校验分组和模型、取内部 key，回会话句柄——
// 内部 key 的原文不在从节点上，之后这次请求的选号等调用都用句柄。分组和模型的校验、错误的写法与本地 routes/paw.go 同一份。
//
// 只在主节点：/paw/config、/paw/auto-group（登录态接口）。面板限流（panelRateLimiter，按 IP / 用户记在 Redis）不在从节点上做：
// 转发请求的并发由主节点选号时的用户并发槽限制。附件（/paw/files）存在从节点内存里（设计 16.1）。
const (
	pawGroupHeader           = "X-Paw-Group-Id"
	pawClientUserAgentHeader = "X-Paw-Client-User-Agent"
	pawTicketContextKey      = "relay.paw.ticket"
)

// PawNode 是从节点上小白端转发接口的部件。
type PawNode struct {
	// Verifier 验中转票据（签名、有效期、节点、吊销）。
	Verifier *sign.TicketVerifier
	// Attachments 是本机的附件服务（/paw/files 上传、聊天请求里按引用取用）。
	Attachments *service.PawAttachmentService
}

// NewPawNode 创建。附件服务按本地 RegisterPawRoutes 同样的参数（24 小时、单个 20MB）。
func NewPawNode(verifier *sign.TicketVerifier) *PawNode {
	return &PawNode{
		Verifier:    verifier,
		Attachments: service.NewPawAttachmentService(service.NewPawAttachmentMemoryRepository(), 24*time.Hour, 20<<20),
	}
}

// WithPaw 注册小白端转发接口（nil 时这些入口交给主节点：主节点只认登录态，会回 401）。
func WithPaw(p *PawNode) RouteOption {
	return func(o *routeOptions) { o.paw = p }
}

// ---- 票据鉴权 ----

// pawAuth 是本地 jwtAuth 的替代：Authorization: Bearer 票据，错误码与 jwtAuth 一致。
func (p *PawNode) pawAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			middleware2.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization header is required")
			return
		}
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			middleware2.AbortWithError(c, http.StatusUnauthorized, "INVALID_AUTH_HEADER", "Authorization header format must be 'Bearer {token}'")
			return
		}
		token := strings.TrimSpace(parts[1])
		if token == "" {
			middleware2.AbortWithError(c, http.StatusUnauthorized, "EMPTY_TOKEN", "Token cannot be empty")
			return
		}
		ticket, err := p.Verifier.Verify(token)
		if err != nil {
			switch {
			case errors.Is(err, sign.ErrExpired):
				middleware2.AbortWithError(c, http.StatusUnauthorized, "TOKEN_EXPIRED", "Token has expired")
			case errors.Is(err, sign.ErrRevoked):
				middleware2.AbortWithError(c, http.StatusUnauthorized, "TOKEN_REVOKED", "Token has been revoked (password changed)")
			default:
				middleware2.AbortWithError(c, http.StatusUnauthorized, "INVALID_TOKEN", "Invalid token")
			}
			return
		}
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: ticket.GetUserId()})
		c.Set(pawTicketContextKey, token)
		c.Next()
	}
}

// ---- 错误的写法（与 routes/paw.go 一致）----

type pawErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func pawChatError(c *gin.Context, status int, code, message string) {
	var body pawErrorEnvelope
	body.Error.Code, body.Error.Message = code, message
	c.JSON(status, body)
	c.Abort()
}

func pawMessagesError(c *gin.Context, status int, errorType, message string) {
	c.JSON(status, gin.H{"type": "error", "error": gin.H{"type": errorType, "message": message}})
	c.Abort()
}

func pawServiceErrorParts(err error) (int, string, string) {
	status := http.StatusInternalServerError
	if appErr := infraerrors.FromError(err); appErr != nil {
		status = int(appErr.Code)
	}
	code := infraerrors.Reason(err)
	if code == "" {
		code = "CONFIG_UNAVAILABLE"
	}
	message := infraerrors.Message(err)
	if message == "" && err != nil {
		message = err.Error()
	}
	return status, code, message
}

func pawCredentialSelectorPresent(c *gin.Context) bool {
	for _, header := range []string{"x-api-key", "x-goog-api-key", middleware2.PlaygroundKeyIDHeader} {
		if strings.TrimSpace(c.GetHeader(header)) != "" {
			return true
		}
	}
	for _, query := range []string{"key", "api_key", "key_id", "api_key_id"} {
		if strings.TrimSpace(c.Query(query)) != "" {
			return true
		}
	}
	return false
}

func pawBodyCredentialSelectorPresent(body []byte) bool {
	if len(body) == 0 || !json.Valid(body) {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return false
	}
	for _, field := range []string{"key", "api_key", "key_id", "api_key_id", "provider_api_key"} {
		if _, ok := fields[field]; ok {
			return true
		}
	}
	return false
}

func resetPawBody(c *gin.Context, body []byte) {
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
	c.Request.Header.Set("Content-Length", strconv.Itoa(len(body)))
}

// restorePawClientUserAgent 把编辑器客户端自己的 User-Agent 放回网关读的地方（本地同名函数）。
func restorePawClientUserAgent(c *gin.Context) {
	forwarded := strings.TrimSpace(c.GetHeader(pawClientUserAgentHeader))
	c.Request.Header.Del(pawClientUserAgentHeader)
	if forwarded == "" || len(forwarded) > 512 {
		return
	}
	for _, r := range forwarded {
		if r < 0x20 || r == 0x7f {
			return
		}
	}
	c.Request.Header.Set("User-Agent", forwarded)
}

// ---- 向主节点解析 ----

// pawResolved 是主节点回的解析：换上这次请求的内部 key（句柄为凭据）。
type pawResolved struct {
	model string
}

// resolve 向主节点解析这次请求。失败时按 writeErr 的格式写出响应并返回 nil。
func (d *Dispatcher) pawResolve(c *gin.Context, req *relayv1.PawResolveRequest, unavailable func(c *gin.Context)) *pawResolved {
	token, _ := c.Get(pawTicketContextKey)
	req.Ticket, _ = token.(string)
	req.ClientIp = strings.TrimSpace(ip.GetClientIP(c))
	req.Method, req.Path = c.Request.Method, c.Request.URL.Path
	resp, err := d.deps.Select.PawResolve(c.Request.Context(), req)
	if err != nil {
		slog.Warn("relay paw resolve failed", "error", err)
		unavailable(c)
		return nil
	}
	if r := resp.GetRejection(); r != nil {
		if r.GetFormat() == relayv1.RejectionFormat_REJECTION_FORMAT_RAW {
			middleware2.WriteCapturedRejection(c, capturedRejection(r))
			return nil
		}
		unavailable(c)
		return nil
	}
	res := resp.GetResolution()
	apiKey, err := keycodec.DecodeAPIKey(res.GetApiKey(), res.GetSession())
	var sub *service.UserSubscription
	if err == nil {
		sub, err = keycodec.DecodeSubscription(res.GetSubscription())
	}
	if err != nil {
		slog.Error("relay paw resolve: bad api key snapshot", "error", err)
		unavailable(c)
		return nil
	}
	middleware2.ReplaceAuthenticatedAPIKey(c, apiKey, sub)
	if raw := res.GetCompositeDecision(); len(raw) > 0 {
		var decision service.CompositeRouteDecision
		if err := json.Unmarshal(raw, &decision); err != nil {
			unavailable(c)
			return nil
		}
		if decision.Matched {
			c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), decision))
		}
	}
	if apiKey.Group != nil && apiKey.Group.Platform == service.PlatformComposite {
		// 主节点选号时按这个公开模型和路径重新选目标（与 API Key 路径同一个做法）。
		stateOf(c).routeModel = res.GetModel()
	}
	return &pawResolved{model: res.GetModel()}
}

func pawChatUnavailable(c *gin.Context) {
	pawChatError(c, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", "Paw gateway is unavailable")
}

func pawMessagesUnavailable(c *gin.Context) {
	pawMessagesError(c, http.StatusServiceUnavailable, "api_error", "Paw messages gateway is unavailable")
}

// targetPlatform 是分派用的平台：组合平台分组看选定的目标。
func pawTargetPlatform(c *gin.Context, apiKey *service.APIKey) string {
	platform := ""
	if apiKey != nil && apiKey.Group != nil {
		platform = apiKey.Group.Platform
	}
	if resolved, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context()); ok {
		platform = resolved
	}
	return platform
}

// ---- 路由 ----

func (p *PawNode) register(r *gin.Engine, h *handler.OpenAIGatewayHandler, gh *handler.GatewayHandler, d *Dispatcher, cfg *config.Config) {
	paw := r.Group("/api/v1/paw",
		middleware2.RequestBodyLimit(cfg.Gateway.MaxBodySize), middleware2.ClientRequestID(), handler.InboundEndpointMiddleware(),
		middleware2.PlaygroundRequestContext, p.pawAuth())

	paw.POST("/files", p.upload)
	paw.POST("/images/generations", p.imageGeneration(d, h))
	paw.POST("/images/edits", p.imageEdit(d, h))
	paw.POST("/chat/completions", p.chat(d, h, gh))
	paw.POST("/responses", p.responses(d, h, gh))
	paw.POST("/messages", p.messages(d, h, gh, false))
	paw.POST("/messages/count_tokens", p.messages(d, h, gh, true))
	paw.POST("/systemone", p.systemOnePrepare(d), middleware2.GroupModelAllowlist(), func(c *gin.Context) {
		if gh == nil {
			pawChatError(c, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", "Paw System One gateway is unavailable")
			return
		}
		gh.SystemOne(c)
	})
}

func pawSubject(c *gin.Context) (int64, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	return subject.UserID, ok && subject.UserID > 0
}

// upload 是 /paw/files：附件存在本机内存里（设计 16.1）。
func (p *PawNode) upload(c *gin.Context) {
	if pawCredentialSelectorPresent(c) {
		pawChatError(c, http.StatusBadRequest, "AUTH_REQUIRED", "Paw accepts only the authenticated account session")
		return
	}
	userID, ok := pawSubject(c)
	if !ok {
		pawChatError(c, http.StatusUnauthorized, "AUTH_REQUIRED", "authenticated user is required")
		return
	}
	file, err := pawFirstMultipartFile(c)
	if err != nil {
		pawUploadError(c, err)
		return
	}
	if file == nil {
		pawChatError(c, http.StatusBadRequest, "ATTACHMENT_INVALID", "file upload is required")
		return
	}
	handle, err := file.Open()
	if err != nil {
		pawChatError(c, http.StatusBadRequest, "ATTACHMENT_INVALID", "failed to open uploaded file")
		return
	}
	defer func() { _ = handle.Close() }()
	data, err := io.ReadAll(handle)
	if err != nil {
		pawChatError(c, http.StatusBadRequest, "ATTACHMENT_INVALID", "failed to read uploaded file")
		return
	}
	attachment, err := p.Attachments.Upload(c.Request.Context(), userID, file.Filename, strings.TrimSpace(file.Header.Get("Content-Type")), data)
	if err != nil {
		pawUploadError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"id": attachment.ID, "filename": attachment.Filename, "mime_type": attachment.MIMEType, "size": attachment.Size, "expires_at": attachment.ExpiresAt,
	}})
}

func pawFirstMultipartFile(c *gin.Context) (*multipart.FileHeader, error) {
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil && err != http.ErrNotMultipart {
		return nil, err
	}
	if c.Request.MultipartForm == nil {
		return nil, nil
	}
	for _, files := range c.Request.MultipartForm.File {
		if len(files) > 0 && files[0] != nil {
			return files[0], nil
		}
	}
	return nil, nil
}

func pawUploadError(c *gin.Context, err error) {
	code, message, status := "ATTACHMENT_INVALID", "file upload failed", http.StatusBadRequest
	if err != nil {
		if appErr := infraerrors.FromError(err); appErr != nil {
			status = int(appErr.Code)
			if reason := infraerrors.Reason(err); reason != "" {
				code = reason
			}
			if msg := infraerrors.Message(err); msg != "" {
				message = msg
			}
		} else {
			message = err.Error()
		}
	}
	pawChatError(c, status, code, message)
}

// chat 是 /paw/chat/completions：校验在主节点，请求体（带附件内容）在本机拼。
func (p *PawNode) chat(d *Dispatcher, h *handler.OpenAIGatewayHandler, gh *handler.GatewayHandler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if pawCredentialSelectorPresent(c) {
			pawChatError(c, http.StatusBadRequest, "AUTH_REQUIRED", "Paw accepts only the authenticated account session")
			return
		}
		userID, ok := pawSubject(c)
		if !ok {
			pawChatError(c, http.StatusUnauthorized, "AUTH_REQUIRED", "authenticated user is required")
			return
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "failed to read request body")
			return
		}
		resetPawBody(c, body)
		if pawBodyCredentialSelectorPresent(body) {
			pawChatError(c, http.StatusBadRequest, "AUTH_REQUIRED", "Paw accepts only the authenticated account session")
			return
		}
		var request service.PawChatRequest
		if err := json.Unmarshal(body, &request); err != nil {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid Paw chat request")
			return
		}
		res := d.pawResolve(c, &relayv1.PawResolveRequest{Op: relayv1.PawResolveRequest_OP_CHAT, RequestJson: body}, pawChatUnavailable)
		if res == nil {
			return
		}
		chatBody, err := service.NewPawChatService(nil, nil, p.Attachments).BuildChatBody(c.Request.Context(), userID, request)
		if err != nil {
			status, code, message := pawServiceErrorParts(err)
			pawChatError(c, status, code, message)
			return
		}
		apiKey, _ := middleware2.GetAPIKeyFromContext(c)
		resetPawBody(c, chatBody)
		switch platform := pawTargetPlatform(c, apiKey); {
		case (platform == service.PlatformOpenAI || platform == service.PlatformGrok) && h != nil:
			h.ChatCompletions(c)
		case gh != nil:
			gh.ChatCompletions(c)
		default:
			pawChatError(c, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", "Paw chat gateway is unavailable")
		}
	}
}

// responses 是 /paw/responses（工作台里的 codex）：请求体原样透传，只读出模型。
func (p *PawNode) responses(d *Dispatcher, h *handler.OpenAIGatewayHandler, gh *handler.GatewayHandler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if pawCredentialSelectorPresent(c) {
			pawChatError(c, http.StatusBadRequest, "AUTH_REQUIRED", "Paw accepts only the authenticated account session")
			return
		}
		if _, ok := pawSubject(c); !ok {
			pawChatError(c, http.StatusUnauthorized, "AUTH_REQUIRED", "authenticated user is required")
			return
		}
		// 分组头缺省（不发、空串、"0"、"auto"）= 交给自动分组解析；写错的值仍然是错。
		groupHeader := strings.TrimSpace(c.GetHeader(pawGroupHeader))
		groupID := int64(0)
		var err error
		if groupHeader != "" && !strings.EqualFold(groupHeader, "auto") {
			groupID, err = strconv.ParseInt(groupHeader, 10, 64)
		}
		if err != nil || groupID < 0 {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", pawGroupHeader+" header is invalid")
			return
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "failed to read request body")
			return
		}
		resetPawBody(c, body)
		if pawBodyCredentialSelectorPresent(body) {
			pawChatError(c, http.StatusBadRequest, "AUTH_REQUIRED", "Paw accepts only the authenticated account session")
			return
		}
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid Responses payload")
			return
		}
		if d.pawResolve(c, &relayv1.PawResolveRequest{Op: relayv1.PawResolveRequest_OP_RESPONSES, GroupId: groupID, ModelId: payload.Model}, pawChatUnavailable) == nil {
			return
		}
		apiKey, _ := middleware2.GetAPIKeyFromContext(c)
		switch platform := pawTargetPlatform(c, apiKey); {
		case (platform == service.PlatformOpenAI || platform == service.PlatformGrok) && h != nil:
			h.Responses(c)
		case gh != nil:
			gh.Responses(c)
		default:
			pawChatError(c, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", "Paw responses gateway is unavailable")
		}
	}
}

// messages 是 /paw/messages 与 /paw/messages/count_tokens（Claude Code 及其编辑器插件）：Anthropic 形状的错误，没有自动分组。
func (p *PawNode) messages(d *Dispatcher, h *handler.OpenAIGatewayHandler, gh *handler.GatewayHandler, countTokens bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if pawCredentialSelectorPresent(c) {
			pawMessagesError(c, http.StatusBadRequest, "invalid_request_error", "Paw accepts only the authenticated account session")
			return
		}
		if _, ok := pawSubject(c); !ok {
			pawMessagesError(c, http.StatusUnauthorized, "authentication_error", "authenticated user is required")
			return
		}
		groupID, err := strconv.ParseInt(strings.TrimSpace(c.GetHeader(pawGroupHeader)), 10, 64)
		if err != nil || groupID <= 0 {
			pawMessagesError(c, http.StatusBadRequest, "invalid_request_error", "a valid Paw group is required")
			return
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			pawMessagesError(c, http.StatusBadRequest, "invalid_request_error", "failed to read request body")
			return
		}
		if pawBodyCredentialSelectorPresent(body) {
			pawMessagesError(c, http.StatusBadRequest, "invalid_request_error", "Paw accepts only the authenticated account session")
			return
		}
		var request struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &request)
		if d.pawResolve(c, &relayv1.PawResolveRequest{Op: relayv1.PawResolveRequest_OP_MESSAGES, GroupId: groupID, ModelId: strings.TrimSpace(request.Model), CountTokens: countTokens}, pawMessagesUnavailable) == nil {
			return
		}
		resetPawBody(c, body)
		restorePawClientUserAgent(c)
		apiKey, _ := middleware2.GetAPIKeyFromContext(c)
		platform := pawTargetPlatform(c, apiKey)
		openAI := platform == service.PlatformOpenAI || platform == service.PlatformGrok
		switch {
		case countTokens && platform == service.PlatformOpenAI && h != nil:
			h.CountTokens(c)
		case countTokens && platform == service.PlatformGrok && h != nil:
			h.GrokCountTokens(c)
		case countTokens && !openAI && gh != nil:
			gh.CountTokens(c)
		case !countTokens && openAI && h != nil:
			h.Messages(c)
		case !countTokens && !openAI && gh != nil:
			gh.Messages(c)
		default:
			pawMessagesError(c, http.StatusServiceUnavailable, "api_error", "Paw messages gateway is unavailable")
		}
	}
}

// systemOnePrepare 是 /paw/systemone 的前一半：分组必须是 TypeSafe 平台，不走自动分组。
func (p *PawNode) systemOnePrepare(d *Dispatcher) gin.HandlerFunc {
	return func(c *gin.Context) {
		if pawCredentialSelectorPresent(c) {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "Paw accepts only the authenticated account session")
			return
		}
		if _, ok := pawSubject(c); !ok {
			pawChatError(c, http.StatusUnauthorized, "AUTH_REQUIRED", "authenticated user is required")
			return
		}
		groupID, err := strconv.ParseInt(strings.TrimSpace(c.GetHeader(pawGroupHeader)), 10, 64)
		if err != nil || groupID <= 0 {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "a valid Paw group is required")
			return
		}
		if d.pawResolve(c, &relayv1.PawResolveRequest{Op: relayv1.PawResolveRequest_OP_SYSTEMONE, GroupId: groupID}, pawChatUnavailable) == nil {
			return
		}
	}
}

func (p *PawNode) imageGeneration(d *Dispatcher, h *handler.OpenAIGatewayHandler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if pawCredentialSelectorPresent(c) {
			pawChatError(c, http.StatusBadRequest, "AUTH_REQUIRED", "Paw accepts only the authenticated account session")
			return
		}
		if _, ok := pawSubject(c); !ok {
			pawChatError(c, http.StatusUnauthorized, "AUTH_REQUIRED", "authenticated user is required")
			return
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "failed to read request body")
			return
		}
		resetPawBody(c, body)
		if pawBodyCredentialSelectorPresent(body) {
			pawChatError(c, http.StatusBadRequest, "AUTH_REQUIRED", "Paw accepts only the authenticated account session")
			return
		}
		var req struct{}
		if err := json.Unmarshal(body, &req); err != nil {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid Paw image request")
			return
		}
		if d.pawResolve(c, &relayv1.PawResolveRequest{Op: relayv1.PawResolveRequest_OP_IMAGES, RequestJson: body}, pawChatUnavailable) == nil {
			return
		}
		resetPawBody(c, body)
		p.dispatchImages(c, h)
	}
}

func (p *PawNode) imageEdit(d *Dispatcher, h *handler.OpenAIGatewayHandler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if pawCredentialSelectorPresent(c) {
			pawChatError(c, http.StatusBadRequest, "AUTH_REQUIRED", "Paw accepts only the authenticated account session")
			return
		}
		if _, ok := pawSubject(c); !ok {
			pawChatError(c, http.StatusUnauthorized, "AUTH_REQUIRED", "authenticated user is required")
			return
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			pawChatError(c, http.StatusBadRequest, "INVALID_REQUEST", "failed to read request body")
			return
		}
		resetPawBody(c, body)
		edit, err := service.NewPawImageService(nil, nil).ParseEditMultipart(c.GetHeader("Content-Type"), body)
		if err != nil {
			status, code, message := pawServiceErrorParts(err)
			pawChatError(c, status, code, message)
			return
		}
		request, _ := json.Marshal(gin.H{"group_id": edit.GroupID, "model_id": edit.ModelID, "prompt": edit.Prompt, "size": edit.Size, "n": edit.N})
		if d.pawResolve(c, &relayv1.PawResolveRequest{Op: relayv1.PawResolveRequest_OP_IMAGES, RequestJson: request}, pawChatUnavailable) == nil {
			return
		}
		resetPawBody(c, body)
		p.dispatchImages(c, h)
	}
}

func (p *PawNode) dispatchImages(c *gin.Context, h *handler.OpenAIGatewayHandler) {
	apiKey, _ := middleware2.GetAPIKeyFromContext(c)
	switch pawTargetPlatform(c, apiKey) {
	case service.PlatformOpenAI:
		if h != nil {
			h.Images(c)
			return
		}
	case service.PlatformGrok:
		if h != nil {
			h.GrokImages(c)
			return
		}
	}
	pawChatError(c, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", "Paw image gateway is unavailable")
}
