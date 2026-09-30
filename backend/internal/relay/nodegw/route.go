package nodegw

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

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
				Path: c.Request.URL.Path, Model: model,
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
				if r.GetFormat() == relayv1.RejectionFormat_REJECTION_FORMAT_RAW {
					middleware2.WriteCapturedRejection(c, capturedRejection(r))
				} else {
					d.HandOff(c)
				}
				c.Abort()
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
