package nodegw

import (
	"context"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GatewayDeps 是从节点 OpenAI 网关用到的部件。转发服务只拿转发要用的：配置、设置（配置快照）、上游 HTTP；
// 仓储、调度、并发、计费一律不给（这些在主节点）。
type GatewayDeps struct {
	Config       *config.Config
	Settings     *service.SettingService
	HTTPUpstream service.HTTPUpstream
	Dispatcher   *Dispatcher
	Decider      service.OpenAIUpstreamErrorDecider
	Reporter     service.OpenAIAccountReporter
	// ErrorPassthrough 是随配置快照下发的错误透传规则（nil 表示不透传）。
	ErrorPassthrough *service.ErrorPassthroughService
}

// NewOpenAIHandler 组装从节点上的 OpenAI 处理函数：与单机同一个处理函数和转发服务，换上远程选号、
// 远程上游错误决策、远程账号事件、只在内存的响应状态存储。
func NewOpenAIHandler(d GatewayDeps) *handler.OpenAIGatewayHandler {
	gw := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, NoopGatewayCache{}, d.Config,
		nil, nil, nil, nil, nil, d.HTTPUpstream, nil, nil, nil, nil, nil, nil, d.Settings, nil)
	gw.SetUpstreamErrorDecider(d.Decider)
	gw.SetAccountReporter(d.Reporter)
	gw.SetOpenAIWSStateStore(NewStateStore())
	if d.Dispatcher.deps.CyberEnabled == nil {
		d.Dispatcher.deps.CyberEnabled = func(ctx context.Context) bool {
			on, _ := gw.CyberSessionBlockRuntime(ctx)
			return on
		}
	}
	h := handler.NewOpenAIGatewayHandler(gw, nil, nil, nil, nil, d.ErrorPassthrough, nil, nil, d.Config)
	h.SetRelayDispatcher(d.Dispatcher)
	return h
}

// RegisterRoutes 注册从节点的网关路由。已接入的是 OpenAI 分组的 Responses（含 WebSocket）、Chat Completions、Messages；
// 其余请求原样交给主节点转发（开发计划 WP10 逐步接入）。中间件链对照本地 /v1 网关链（routes/gateway.go）：
// 全局 IP 黑名单、Key 鉴权、用户黑名单、自动分组、未分组拦截合成准入中间件；组合平台、TypeSafe 分组在准入时
// 回"暂不支持"。
func RegisterRoutes(r *gin.Engine, h *handler.OpenAIGatewayHandler, d *Dispatcher, cfg *config.Config) {
	bodyLimit := middleware2.RequestBodyLimit(cfg.Gateway.MaxBodySize)
	chain := []gin.HandlerFunc{
		bodyLimit,
		middleware2.ClientRequestID(),
		handler.InboundEndpointMiddleware(),
		d.AdmitMiddleware(),
		middleware2.GroupModelAllowlist(),
	}
	openAIOnly := func(next gin.HandlerFunc) gin.HandlerFunc {
		return func(c *gin.Context) {
			if g, ok := middleware2.GetAPIKeyFromContext(c); !ok || g.Group == nil || g.Group.Platform != service.PlatformOpenAI {
				d.HandOff(c)
				return
			}
			next(c)
		}
	}
	responses := openAIOnly(func(c *gin.Context) {
		if !service.IsForwardableOpenAIResponsesRequestPath(c) || service.IsOpenAIResponsesInputTokensRequestPath(c) {
			// 不可转发的子路径与 input_tokens 由主节点照原逻辑处理。
			d.HandOff(c)
			return
		}
		h.Responses(c)
	})
	for _, prefix := range []string{"/v1", ""} {
		g := r.Group(prefix, chain...)
		g.POST("/responses", responses)
		g.POST("/responses/*subpath", responses)
		g.POST("/chat/completions", openAIOnly(h.ChatCompletions))
		// Responses WebSocket（Codex）：与本地一样是 GET /responses 的升级请求。
		g.GET("/responses", openAIOnly(h.ResponsesWebSocket))
		if prefix == "/v1" {
			// 与本地一致：只有 /v1/messages（OpenAI 兼容分组走 OpenAI 网关的 Messages）。
			g.POST("/messages", openAIOnly(h.Messages))
		}
	}
	r.NoRoute(bodyLimit, func(c *gin.Context) {
		body, err := readBody(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "Failed to read request body"}})
			return
		}
		if d.deps.HandOff == nil {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		d.deps.HandOff(c, body)
	})
}

// NoopGatewayCache 是从节点上的网关缓存：粘性会话在主节点（选号时读、释放时写），这里一律"没有"；
// Grok 视频计费认领、推理内容缓存还没接入（开发计划 2.2，WP10）。
type NoopGatewayCache struct{}

var _ service.GatewayCache = NoopGatewayCache{}

func (NoopGatewayCache) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, service.ErrStickySessionNotFound
}
func (NoopGatewayCache) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}
func (NoopGatewayCache) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}
func (NoopGatewayCache) DeleteSessionAccountID(context.Context, int64, string) error { return nil }
func (NoopGatewayCache) SetGrokVideoPendingBilling(context.Context, string, []byte, time.Duration) error {
	return nil
}
func (NoopGatewayCache) GetGrokVideoPendingBilling(context.Context, string) ([]byte, error) {
	return nil, nil
}
func (NoopGatewayCache) ClaimGrokVideoBilled(context.Context, string, time.Duration) (bool, error) {
	return false, nil
}
func (NoopGatewayCache) ReleaseGrokVideoBilled(context.Context, string) error { return nil }
func (NoopGatewayCache) SetReasoningContent(context.Context, string, string, time.Duration) error {
	return nil
}
func (NoopGatewayCache) GetReasoningContent(context.Context, string) (string, error) {
	return "", service.ErrReasoningContentNotFound
}

// NewEngine 创建从节点的 HTTP 引擎：与单机相同的恢复和请求日志中间件（请求 ID 从这里来，进扣费记录）。
func NewEngine() *gin.Engine {
	r := gin.New()
	r.Use(middleware2.Recovery(), middleware2.RequestLogger())
	return r
}
