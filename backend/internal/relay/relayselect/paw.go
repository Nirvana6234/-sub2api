package relayselect

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/relay/keycodec"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// 小白端转发接口（/paw/*，设计 8.1）。从节点验过票据后，每个请求向主节点解析：主节点按本地 /paw 同一段代码复查用户、校验分组和
// 模型、取内部 key 钉死在分组上，回一个不透明的会话句柄；这次请求之后的选号、换组等调用用句柄代替 API Key 原文放进凭据字段，
// 主节点在 admitAPIKey 里认句柄——内部 key 的原文不离开主节点。句柄只在这台节点上用，每次使用都复查用户状态和 token_version。

// pawSessionPrefix 是会话句柄的前缀（与 API Key 原文区分）。
const pawSessionPrefix = "paw-session."

// pawSessionLifetime 是句柄的有效期：覆盖一次请求里的换号、换组重试；每次使用都复查用户，所以不必很短。
const pawSessionLifetime = 30 * time.Minute

type pawSession struct {
	nodeID       int64
	userID       int64
	tokenVersion int64
	key          *service.APIKey
	subscription *service.UserSubscription
	expires      time.Time
}

type pawSessions struct {
	mu    sync.Mutex
	items map[string]*pawSession
	calls int
}

func (p *pawSessions) put(sess *pawSession, now time.Time) (string, error) {
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	handle := pawSessionPrefix + base64.RawURLEncoding.EncodeToString(b[:])
	sess.expires = now.Add(pawSessionLifetime)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.items == nil {
		p.items = map[string]*pawSession{}
	}
	p.items[handle] = sess
	// 顺带清掉过期的（每 256 次一轮），不另起定时任务。
	if p.calls++; p.calls%256 == 0 {
		for h, v := range p.items {
			if now.After(v.expires) {
				delete(p.items, h)
			}
		}
	}
	return handle, nil
}

func (p *pawSessions) get(handle string, nodeID int64, now time.Time) *pawSession {
	p.mu.Lock()
	defer p.mu.Unlock()
	sess := p.items[handle]
	if sess == nil || sess.nodeID != nodeID {
		return nil
	}
	if now.After(sess.expires) {
		delete(p.items, handle)
		return nil
	}
	return sess
}

func isPawSession(credential string) bool { return strings.HasPrefix(credential, pawSessionPrefix) }

type callingNodeKey struct{}

// withCallingNode 把调用的节点 ID 放进 ctx：admitAPIKey 认句柄时核对它（句柄只在签给的节点上用）。
func withCallingNode(ctx context.Context, nodeID int64) context.Context {
	return context.WithValue(ctx, callingNodeKey{}, nodeID)
}

func callingNode(ctx context.Context) int64 {
	id, _ := ctx.Value(callingNodeKey{}).(int64)
	return id
}

// pawAuthRejection 是票据 / 用户复查不过的响应：与本地 jwtAuth 同样的错误码和写法。
func pawAuthRejection(method, path string, status int, code, message string) *middleware.CapturedRejection {
	return middleware.CaptureResponse(method, path, func(c *gin.Context) { middleware.AbortWithError(c, status, code, message) })
}

// pawErrorBody 是 /paw 接口自己的错误体（`{"error":{"code","message"}}`，本地 pawChatError）。
func pawErrorBody(status int, code, message string) *middleware.CapturedRejection {
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	return &middleware.CapturedRejection{Status: status, Header: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}}, Body: body}
}

// pawMessagesErrorBody 是 /paw/messages 的错误体（Anthropic 形状，本地 pawMessagesError）。
func pawMessagesErrorBody(status int, message string) *middleware.CapturedRejection {
	body, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]string{"type": pawAnthropicErrorType(status), "message": message}})
	return &middleware.CapturedRejection{Status: status, Header: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}}, Body: body}
}

func pawAnthropicErrorType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	default:
		return "api_error"
	}
}

func pawServiceError(err error) (status int, code, message string) {
	status = http.StatusInternalServerError
	if appErr := infraerrors.FromError(err); appErr != nil {
		status = int(appErr.Code)
	}
	code = infraerrors.Reason(err)
	if code == "" {
		code = "CONFIG_UNAVAILABLE"
	}
	message = infraerrors.Message(err)
	if message == "" {
		message = err.Error()
	}
	return status, code, message
}

func pawRejected(r *middleware.CapturedRejection) *relayv1.PawResolveResponse {
	return &relayv1.PawResolveResponse{Result: &relayv1.PawResolveResponse_Rejection{Rejection: rawRejection(r).GetRejection()}}
}

// PawResolve 见 RelayControl.PawResolve。
func (s *selector) PawResolve(ctx context.Context, nodeID int64, req *relayv1.PawResolveRequest) (*relayv1.PawResolveResponse, error) {
	if s.env.VerifyTicket == nil || s.deps.Users == nil {
		return nil, errors.New("paw requests are not available")
	}
	ticket, err := s.env.VerifyTicket(req.GetTicket(), nodeID)
	if err != nil {
		code, message := "INVALID_TOKEN", "Invalid token"
		if errors.Is(err, sign.ErrExpired) {
			code, message = "TOKEN_EXPIRED", "Token has expired"
		}
		return pawRejected(pawAuthRejection(req.GetMethod(), req.GetPath(), http.StatusUnauthorized, code, message)), nil
	}
	// 以下复查与本地 jwtAuth + BackendModeUserGuard 同一套规则（设计 8.1：以选号时的复查为准）。
	user, err := s.deps.Users.GetByID(ctx, ticket.GetUserId())
	if err != nil {
		if errors.Is(err, service.ErrUserNotFound) {
			return pawRejected(pawAuthRejection(req.GetMethod(), req.GetPath(), http.StatusUnauthorized, "USER_NOT_FOUND", "User not found")), nil
		}
		return nil, err
	}
	if rej := pawUserRejection(req.GetMethod(), req.GetPath(), user, ticket.GetTokenVersion()); rej != nil {
		return pawRejected(rej), nil
	}
	if s.deps.Settings != nil && s.deps.Settings.IsBackendModeEnabled(ctx) && user.Role != "admin" {
		return pawRejected(middleware.CaptureResponse(req.GetMethod(), req.GetPath(), func(c *gin.Context) {
			response.Forbidden(c, "Backend mode is active. User self-service is disabled.")
			c.Abort()
		})), nil
	}
	s.admitted.note(nodeID, user.ID, s.now())

	res, rej, err := s.pawPrepare(ctx, req, user.ID)
	if err != nil {
		return nil, err
	}
	if rej != nil {
		return pawRejected(rej), nil
	}
	if res.key == nil || res.key.User == nil {
		return pawRejected(pawErrorBody(http.StatusServiceUnavailable, "CONFIG_UNAVAILABLE", "Paw chat credentials are unavailable")), nil
	}
	out := &relayv1.PawResolution{Model: res.model}
	if out.ApiKey, err = keycodec.EncodeAPIKey(res.key); err != nil {
		return nil, err
	}
	if out.Subscription, err = keycodec.EncodeSubscription(res.subscription); err != nil {
		return nil, err
	}
	if res.composite.Matched {
		if out.CompositeDecision, err = json.Marshal(res.composite); err != nil {
			return nil, err
		}
	}
	handle, err := s.paw.put(&pawSession{nodeID: nodeID, userID: user.ID, tokenVersion: ticket.GetTokenVersion(), key: res.key, subscription: res.subscription}, s.now())
	if err != nil {
		return nil, err
	}
	out.Session = handle
	return &relayv1.PawResolveResponse{Result: &relayv1.PawResolveResponse_Resolution{Resolution: out}}, nil
}

// pawUserRejection 是用户复查（本地 jwtAuth 里查库之后的那几项）：启用、token_version 与签票据时一致。
func pawUserRejection(method, path string, user *service.User, ticketTokenVersion int64) *middleware.CapturedRejection {
	if !user.IsActive() {
		return pawAuthRejection(method, path, http.StatusUnauthorized, "USER_INACTIVE", "User account is not active")
	}
	if service.ResolvedTokenVersion(user) != ticketTokenVersion {
		return pawAuthRejection(method, path, http.StatusUnauthorized, "TOKEN_REVOKED", "Token has been revoked (password changed)")
	}
	return nil
}

type pawPrepared struct {
	key          *service.APIKey
	subscription *service.UserSubscription
	model        string
	composite    service.CompositeRouteDecision
}

// pawPrepare 是各个 /paw 入口在换上内部 key 之前的校验（本地路由处理函数里 Prepare* 到组合平台选目标那一段）。
func (s *selector) pawPrepare(ctx context.Context, req *relayv1.PawResolveRequest, userID int64) (*pawPrepared, *middleware.CapturedRejection, error) {
	chatErr := func(err error) (*pawPrepared, *middleware.CapturedRejection, error) {
		status, code, message := pawServiceError(err)
		return nil, pawErrorBody(status, code, message), nil
	}
	messagesErr := func(err error) (*pawPrepared, *middleware.CapturedRejection, error) {
		status, _, message := pawServiceError(err)
		return nil, pawMessagesErrorBody(status, message), nil
	}
	typeSafeDenied := func(messages bool) *middleware.CapturedRejection {
		if messages {
			return pawMessagesErrorBody(http.StatusForbidden, "selected group only serves System One")
		}
		return pawErrorBody(http.StatusForbidden, "GROUP_FORBIDDEN", "selected group only serves System One")
	}
	var (
		group    *service.Group
		endpoint string
		out      pawPrepared
	)
	switch req.GetOp() {
	case relayv1.PawResolveRequest_OP_CHAT:
		if s.deps.PawChat == nil {
			return nil, pawErrorBody(http.StatusServiceUnavailable, "CONFIG_UNAVAILABLE", "Paw chat configuration is unavailable"), nil
		}
		var request service.PawChatRequest
		if err := json.Unmarshal(req.GetRequestJson(), &request); err != nil {
			return nil, pawErrorBody(http.StatusBadRequest, "INVALID_REQUEST", "invalid Paw chat request"), nil
		}
		resolution, err := s.deps.PawChat.PrepareSelection(ctx, userID, request)
		if err != nil {
			return chatErr(err)
		}
		if resolution.Group != nil && resolution.Group.Platform == service.PlatformTypeSafe {
			return nil, typeSafeDenied(false), nil
		}
		group, endpoint = resolution.Group, service.CompositeRouteEndpointChatCompletions
		out = pawPrepared{key: resolution.APIKey, subscription: resolution.Subscription, model: resolution.Model}
	case relayv1.PawResolveRequest_OP_RESPONSES:
		if s.deps.PawChat == nil {
			return nil, pawErrorBody(http.StatusServiceUnavailable, "CONFIG_UNAVAILABLE", "Paw chat configuration is unavailable"), nil
		}
		resolution, err := s.deps.PawChat.PrepareResponses(ctx, userID, service.PawResponsesRequest{GroupID: req.GetGroupId(), ModelID: req.GetModelId()})
		if err != nil {
			return chatErr(err)
		}
		if resolution.Group != nil && resolution.Group.Platform == service.PlatformTypeSafe {
			return nil, typeSafeDenied(false), nil
		}
		group, endpoint = resolution.Group, service.CompositeRouteEndpointResponses
		out = pawPrepared{key: resolution.APIKey, subscription: resolution.Subscription, model: resolution.Model}
	case relayv1.PawResolveRequest_OP_MESSAGES, relayv1.PawResolveRequest_OP_SYSTEMONE:
		systemOne := req.GetOp() == relayv1.PawResolveRequest_OP_SYSTEMONE
		if s.deps.PawChat == nil {
			if systemOne {
				return nil, pawErrorBody(http.StatusServiceUnavailable, "CONFIG_UNAVAILABLE", "Paw System One gateway is unavailable"), nil
			}
			return nil, pawMessagesErrorBody(http.StatusServiceUnavailable, "Paw messages gateway is unavailable"), nil
		}
		resolution, err := s.deps.PawChat.PrepareMessages(ctx, userID, req.GetGroupId())
		if systemOne {
			if err != nil {
				return chatErr(err)
			}
			if resolution.Group.Platform != service.PlatformTypeSafe {
				return nil, pawErrorBody(http.StatusForbidden, "GROUP_FORBIDDEN", "selected group does not serve System One"), nil
			}
			return &pawPrepared{key: resolution.APIKey, subscription: resolution.Subscription}, nil, nil
		}
		if err != nil {
			return messagesErr(err)
		}
		if resolution.Group != nil && resolution.Group.Platform == service.PlatformTypeSafe {
			return nil, typeSafeDenied(true), nil
		}
		group, endpoint = resolution.Group, service.CompositeRouteEndpointMessages
		if req.GetCountTokens() {
			endpoint = service.CompositeRouteEndpointCountTokens
		}
		out = pawPrepared{key: resolution.APIKey, subscription: resolution.Subscription, model: strings.TrimSpace(req.GetModelId())}
	case relayv1.PawResolveRequest_OP_IMAGES:
		if s.deps.PawImages == nil {
			return nil, pawErrorBody(http.StatusServiceUnavailable, "CONFIG_UNAVAILABLE", "Paw image configuration is unavailable"), nil
		}
		var request struct {
			GroupID int64  `json:"group_id"`
			ModelID string `json:"model_id"`
			Prompt  string `json:"prompt"`
			Size    string `json:"size"`
			N       int    `json:"n"`
			Stream  bool   `json:"stream"`
		}
		if err := json.Unmarshal(req.GetRequestJson(), &request); err != nil {
			return nil, pawErrorBody(http.StatusBadRequest, "INVALID_REQUEST", "invalid Paw image request"), nil
		}
		resolution, err := s.deps.PawImages.ValidateGeneration(ctx, userID, service.PawImageGenerationRequest{
			GroupID: request.GroupID, ModelID: request.ModelID, Prompt: request.Prompt, Size: request.Size, N: request.N, Stream: request.Stream,
		})
		if err != nil {
			return chatErr(err)
		}
		group, endpoint = resolution.Group, service.CompositeRouteEndpointImages
		out = pawPrepared{key: resolution.APIKey, model: resolution.Model.ID}
	default:
		return nil, pawErrorBody(http.StatusBadRequest, "INVALID_REQUEST", "unknown Paw operation"), nil
	}
	// 组合平台分组：按模型选目标（本地处理函数里的 CompositeResolver.Resolve）。
	if s.deps.Composite != nil && group != nil && group.Platform == service.PlatformComposite {
		decision, err := s.deps.Composite.Resolve(ctx, group.ID, out.model, endpoint)
		if err != nil {
			if req.GetOp() == relayv1.PawResolveRequest_OP_MESSAGES {
				return nil, pawMessagesErrorBody(http.StatusServiceUnavailable, "failed to resolve the selected model route"), nil
			}
			return nil, pawErrorBody(http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", "failed to resolve the selected model route"), nil
		}
		out.composite = decision
	}
	return &out, nil, nil
}

// admitPawSession 是 admitAPIKey 认会话句柄时做的（代替 API Key 的鉴权链）：句柄有效且签给这台节点，用户仍然启用且 token_version
// 没变，然后用句柄里钉死在分组上的内部 key。不跑 API Key 的 IP 白名单、全局黑名单、自动分组、分组白名单等：本地 /paw 不过那条链。
func (s *selector) admitPawSession(ctx context.Context, handle, method, path string, served []string) (middleware.RelayAPIKeyAdmission, *relayv1.SelectResponse, error) {
	sess := s.paw.get(handle, callingNode(ctx), s.now())
	if sess == nil || s.deps.Users == nil {
		return middleware.RelayAPIKeyAdmission{}, rawRejection(pawAuthRejection(method, path, http.StatusUnauthorized, "INVALID_TOKEN", "Invalid token")), nil
	}
	user, err := s.deps.Users.GetByID(ctx, sess.userID)
	if err != nil {
		if errors.Is(err, service.ErrUserNotFound) {
			return middleware.RelayAPIKeyAdmission{}, rawRejection(pawAuthRejection(method, path, http.StatusUnauthorized, "USER_NOT_FOUND", "User not found")), nil
		}
		return middleware.RelayAPIKeyAdmission{}, nil, err
	}
	if rej := pawUserRejection(method, path, user, sess.tokenVersion); rej != nil {
		return middleware.RelayAPIKeyAdmission{}, rawRejection(rej), nil
	}
	key := *sess.key
	if sess.key.User != nil {
		u := *sess.key.User
		key.User = &u
	}
	adm := middleware.RelayAPIKeyAdmission{APIKey: &key, Billing: middleware.APIKeyBillingDecision{Subscription: sess.subscription}}
	platform := noGroupPlatform
	if key.Group != nil {
		platform = key.Group.Platform
	}
	if len(served) == 0 {
		served = openAIServedPlatforms
	}
	for _, p := range served {
		if p == platform {
			return adm, nil, nil
		}
	}
	return middleware.RelayAPIKeyAdmission{}, unsupported(), nil
}
