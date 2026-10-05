package nodegw

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/relay/keycodec"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// AutoGroupMiddleware 是从节点上的自动分组选组（本地 autoGroupModelRoutingMiddleware，设计 3.2）：在准入之后、
// 分组模型白名单之前，按请求体里的模型（与本地同一段取法）问主节点选分组，换上选定分组的 Key 快照和订阅。
// 选定的分组记在这次请求的 Key 上，之后问主节点（组合平台选目标、选号）都带着它，主节点只核对不重选。
// GET（WebSocket 升级）、取不到模型的请求与本地一样保留鉴权时的冷启动分组。
// 请求结束时把结果（按本地同一段算法得出的状态码、首字耗时）报给主节点调整之后的选组；交给主节点转发的不报
// （主节点照本地处理，自己记）。
func (d *Dispatcher) AutoGroupMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey, ok := middleware2.GetAPIKeyFromContext(c)
		if !ok || apiKey == nil || !apiKey.AutoGroup || c.Request == nil || c.Request.Method == http.MethodGet {
			c.Next()
			return
		}
		model := requestmodel.RoutingModel(c.GetHeader("Content-Type"), stateOf(c).rawBody)
		if model == "" {
			// Gemini 原生 URL 里的模型（本地 autoGroupModelRouting 同一个取法）。
			model = requestmodel.GeminiModelFromRouteParams(c.Param("model"), c.Param("modelAction"))
		}
		if model == "" {
			model = requestmodel.DefaultAutoGroupModel(c.Request.URL.Path)
		}
		if model == "" {
			c.Next()
			return
		}
		resp, err := d.deps.Select.ResolveRoute(c.Request.Context(), &relayv1.ResolveRouteRequest{
			ApiKey: apiKey.Key, ClientIp: strings.TrimSpace(ip.GetClientIP(c)), Method: c.Request.Method,
			Path: c.Request.URL.Path, Model: model,
		})
		if err == nil && resp.GetRejection() == nil {
			var resolved *service.APIKey
			if resolved, err = keycodec.DecodeAPIKey(resp.GetResolution().GetApiKey(), apiKey.Key); err == nil {
				var sub *service.UserSubscription
				if sub, err = keycodec.DecodeSubscription(resp.GetResolution().GetSubscription()); err == nil {
					middleware2.ReplaceAuthenticatedAPIKey(c, resolved, sub)
					c.Next()
					d.reportAutoGroupResult(c, apiKey.Key, model)
					return
				}
			}
		}
		if err != nil {
			// 与本地选组出错时的写法一致。
			slog.Warn("relay auto group resolution failed", "error", err)
			middleware2.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to resolve automatic API key group")
			return
		}
		writeRouteRejection(c, d, resp.GetRejection())
	}
}

// reportAutoGroupResult 在请求结束时报自动分组的结果（本地自动分组中间件 c.Next 之后那一段）。按请求结束时
// 上下文里的 Key 报：处理函数中途换过分组的，记在换到的分组上。
func (d *Dispatcher) reportAutoGroupResult(c *gin.Context, rawKey, model string) {
	st := stateOf(c)
	st.mu.Lock()
	handedOff := st.handedOff
	st.mu.Unlock()
	current, ok := middleware2.GetAPIKeyFromContext(c)
	if handedOff || !ok || autoGroupID(current) == 0 {
		return
	}
	status, firstTokenMs := middleware2.AutoGroupObservedResult(c)
	report := &relayv1.AutoGroupResult{ApiKey: rawKey, Model: model, GroupId: autoGroupID(current), Status: int32(status)}
	if firstTokenMs != nil {
		report.HasFirstTokenMs, report.FirstTokenMs = true, *firstTokenMs
	}
	d.deps.Select.ReportAutoGroupResult(report)
}

// SwitchAutoGroup 自动分组 Key 换到下一个候选分组（handler.OpenAIRelayDispatcher）：主节点选，这里换上它回的 Key 快照
// 和订阅。不重发（换组会改主节点的选组状态），出错按没换处理。
func (d *Dispatcher) SwitchAutoGroup(c *gin.Context, apiKey *service.APIKey, model string, failedGroupIDs map[int64]struct{}) (handler.OpenAIRelayAutoGroupSwitch, bool) {
	current := autoGroupID(apiKey)
	if current == 0 {
		return handler.OpenAIRelayAutoGroupSwitch{}, false
	}
	req := &relayv1.SwitchAutoGroupRequest{
		ApiKey: apiKey.Key, ClientIp: strings.TrimSpace(ip.GetClientIP(c)), Method: c.Request.Method, Path: c.Request.URL.Path,
		Model: model, CurrentGroupId: current,
	}
	for id := range failedGroupIDs {
		req.FailedGroupIds = append(req.FailedGroupIds, id)
	}
	if apiKey.User != nil {
		req.HeldQuota = d.heldQuota(apiKey.User.ID, apiKey.ID)
	}
	resp, err := d.deps.Select.SwitchAutoGroup(c.Request.Context(), req)
	if err != nil {
		slog.Warn("relay auto group switch failed", "error", err)
		return handler.OpenAIRelayAutoGroupSwitch{}, false
	}
	if !resp.GetSwitched() {
		return handler.OpenAIRelayAutoGroupSwitch{}, false
	}
	resolved, err := keycodec.DecodeAPIKey(resp.GetApiKey(), apiKey.Key)
	if err != nil {
		slog.Error("relay auto group switch: bad api key snapshot", "error", err)
		return handler.OpenAIRelayAutoGroupSwitch{}, false
	}
	sub, err := keycodec.DecodeSubscription(resp.GetSubscription())
	if err != nil {
		slog.Error("relay auto group switch: bad subscription snapshot", "error", err)
		return handler.OpenAIRelayAutoGroupSwitch{}, false
	}
	out := handler.OpenAIRelayAutoGroupSwitch{APIKey: resolved, Subscription: sub}
	if r := resp.GetBillingRejection(); r != nil {
		g := gatewayOf(r)
		out.BillingRejection = &g
	}
	return out, true
}

// SwitchFallbackGroup 换到兜底分组（handler.OpenAIRelayDispatcher）：主节点解析兜底分组、做计费资格复查，回兜底分组的
// Key 快照；之后的选号带上兜底分组（主节点核对）。出错按没换处理。
func (d *Dispatcher) SwitchFallbackGroup(c *gin.Context, apiKey *service.APIKey) (handler.OpenAIRelayFallbackSwitch, bool) {
	req := &relayv1.SwitchFallbackGroupRequest{
		ApiKey: apiKey.Key, ClientIp: strings.TrimSpace(ip.GetClientIP(c)), Method: c.Request.Method, Path: c.Request.URL.Path,
		AutoGroupId: autoGroupID(apiKey),
	}
	if apiKey.User != nil {
		req.HeldQuota = d.heldQuota(apiKey.User.ID, apiKey.ID)
	}
	resp, err := d.deps.Select.SwitchFallbackGroup(c.Request.Context(), req)
	if err != nil {
		slog.Warn("relay fallback group switch failed", "error", err)
		return handler.OpenAIRelayFallbackSwitch{}, false
	}
	if r := resp.GetBillingRejection(); r != nil {
		g := gatewayOf(r)
		return handler.OpenAIRelayFallbackSwitch{BillingRejection: &g}, false
	}
	if !resp.GetSwitched() {
		return handler.OpenAIRelayFallbackSwitch{}, false
	}
	resolved, err := keycodec.DecodeAPIKey(resp.GetApiKey(), apiKey.Key)
	if err != nil {
		slog.Error("relay fallback group switch: bad api key snapshot", "error", err)
		return handler.OpenAIRelayFallbackSwitch{}, false
	}
	st := stateOf(c)
	st.mu.Lock()
	st.fallbackGroupID, st.fallbackAutoGroupID = resp.GetFallbackGroupId(), autoGroupID(apiKey)
	st.mu.Unlock()
	return handler.OpenAIRelayFallbackSwitch{APIKey: resolved}, true
}

// writeRouteRejection 写出 ResolveRoute 的拒绝：主节点生成的拒绝原样写出，"暂不支持"交给主节点转发。
func writeRouteRejection(c *gin.Context, d *Dispatcher, r *relayv1.SelectRejection) {
	if r.GetFormat() == relayv1.RejectionFormat_REJECTION_FORMAT_RAW {
		middleware2.WriteCapturedRejection(c, capturedRejection(r))
	} else {
		d.HandOff(c)
	}
	c.Abort()
}

// autoGroupID 是自动分组 Key 这次请求用的分组（问主节点时带上，主节点核对后照用）；不是自动分组 Key 时为 0。
func autoGroupID(apiKey *service.APIKey) int64 {
	if apiKey == nil || !apiKey.AutoGroup || apiKey.GroupID == nil {
		return 0
	}
	return *apiKey.GroupID
}

// CompositeRouteMiddleware 是从节点上的组合平台选目标（本地 compositeTargetPlatformMiddleware，设计 3.2）：在分组模型
// 白名单之后，按请求体里的公开模型问主节点选目标平台和上游模型，放进请求 ctx，并把请求体里的模型改成上游模型
// （与本地同一段取模型、改模型的代码）。交给主节点转发时用的是原始请求体（stateOf(c).rawBody 不改）。
// GET（WebSocket 升级）与本地一样不选。
func (d *Dispatcher) CompositeRouteMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey, ok := middleware2.GetAPIKeyFromContext(c)
		if !ok || apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformComposite ||
			c.Request == nil || c.Request.Method == http.MethodGet {
			c.Next()
			return
		}
		st := stateOf(c)
		body := st.rawBody
		routePath := c.FullPath()
		model := requestmodel.FromBodyForRoute(routePath, c.GetHeader("Content-Type"), body)
		if model != "" {
			resp, err := d.deps.Select.ResolveRoute(c.Request.Context(), &relayv1.ResolveRouteRequest{
				ApiKey: apiKey.Key, ClientIp: strings.TrimSpace(ip.GetClientIP(c)), Method: c.Request.Method,
				Path: c.Request.URL.Path, Model: model, AutoGroupId: autoGroupID(apiKey),
			})
			var decision service.CompositeRouteDecision
			if err == nil && resp.GetRejection() == nil {
				err = json.Unmarshal(resp.GetResolution().GetCompositeDecision(), &decision)
			}
			if err != nil {
				// 与本地选目标出错时的写法一致。
				slog.Warn("relay composite route resolution failed", "error", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"type": "server_error", "message": "Failed to resolve composite model route"}})
				c.Abort()
				return
			}
			if r := resp.GetRejection(); r != nil {
				writeRouteRejection(c, d, r)
				return
			}
			st.routeModel = model
			if decision.Matched {
				c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), decision))
				if upstreamModel := strings.TrimSpace(decision.UpstreamModel); upstreamModel != "" && upstreamModel != model && gjson.ValidBytes(body) {
					if _, modelPath := requestmodel.JSONModelPathForRoute(routePath, body); modelPath != "" {
						if rewritten, rewriteErr := sjson.SetBytes(body, modelPath, upstreamModel); rewriteErr == nil {
							body = rewritten
						}
					}
				}
			}
		}
		requestmodel.ResetRequestBody(c.Request, body)
		c.Next()
	}
}

// CompositeGeminiRouteMiddleware 是从节点上 Gemini 原生入口的组合平台选目标（本地 compositeGeminiTargetPlatformMiddleware）：
// 模型取自 URL（请求体里没有），不改请求体；没有匹配的目标时按 Gemini。
func (d *Dispatcher) CompositeGeminiRouteMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey, ok := middleware2.GetAPIKeyFromContext(c)
		if ok && apiKey != nil && apiKey.Group != nil && apiKey.Group.Platform == service.PlatformComposite {
			if model := requestmodel.GeminiModelFromRouteParams(c.Param("model"), c.Param("modelAction")); model != "" {
				resp, err := d.deps.Select.ResolveRoute(c.Request.Context(), &relayv1.ResolveRouteRequest{
					ApiKey: apiKey.Key, ClientIp: strings.TrimSpace(ip.GetClientIP(c)), Method: c.Request.Method,
					Path: c.Request.URL.Path, Model: model, AutoGroupId: autoGroupID(apiKey),
				})
				var decision service.CompositeRouteDecision
				if err == nil && resp.GetRejection() == nil {
					err = json.Unmarshal(resp.GetResolution().GetCompositeDecision(), &decision)
				}
				if err != nil {
					// 与本地选目标出错时的写法一致。
					slog.Warn("relay composite route resolution failed", "error", err)
					c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"type": "server_error", "message": "Failed to resolve composite model route"}})
					c.Abort()
					return
				}
				if r := resp.GetRejection(); r != nil {
					writeRouteRejection(c, d, r)
					return
				}
				stateOf(c).routeModel = model
				if decision.Matched {
					c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), decision))
				}
			}
			if _, resolved := service.ResolvedTargetPlatformFromContext(c.Request.Context()); !resolved {
				c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), service.PlatformGemini))
			}
		}
		c.Next()
	}
}

// servedPlatform 是这次请求要走的平台：组合平台分组看选定的目标平台（本地 getGroupPlatform）。
func servedPlatform(c *gin.Context, apiKey *service.APIKey) string {
	if apiKey == nil || apiKey.Group == nil {
		return ""
	}
	if apiKey.Group.Platform == service.PlatformComposite {
		if platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context()); ok {
			return platform
		}
	}
	return apiKey.Group.Platform
}
