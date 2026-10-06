package nodegw

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/relay/node"
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
	// TLSProfiles 是随配置快照下发的 TLS 指纹模板（Anthropic OAuth 账号开了 TLS 指纹伪装时用）；nil 时用空模板集。
	TLSProfiles *service.TLSFingerprintProfileService
	// Moderation 是本机的安全审计（NewModeration）；nil 表示不审计（测试）。
	Moderation *Moderation
	// Ops 是本机的运维服务（只写请求错误日志到本机存储，NewNodeOpsRepository）；nil 表示不记错误日志（测试）。
	Ops *service.OpsService
}

// NewOpenAIHandler 组装从节点上的 OpenAI 处理函数：与单机同一个处理函数和转发服务，换上远程选号、
// 远程上游错误决策、远程账号事件、只在内存的响应状态存储。
func NewOpenAIHandler(d GatewayDeps) *handler.OpenAIGatewayHandler {
	gw := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, NoopGatewayCache{}, d.Config,
		nil, nil, nil, nil, nil, d.HTTPUpstream, nil, nil, nil, nil, nil, nil, d.Settings, nil)
	gw.SetUpstreamErrorDecider(d.Decider)
	gw.SetAccountReporter(d.Reporter)
	if gr, ok := d.Reporter.(service.GrokAccountReporter); ok {
		// Grok 转发路径上写账号状态的几处：从节点没有仓储，事实作为账号事件交给主节点执行。
		gw.SetGrokAccountReporter(gr)
	}
	gw.SetOpenAIWSStateStore(NewStateStore())
	if d.Dispatcher.deps.CyberEnabled == nil {
		d.Dispatcher.deps.CyberEnabled = func(ctx context.Context) bool {
			on, _ := gw.CyberSessionBlockRuntime(ctx)
			return on
		}
	}
	var moderation *service.ContentModerationService
	if d.Moderation != nil {
		moderation = d.Moderation.Service
	}
	h := handler.NewOpenAIGatewayHandler(gw, nil, nil, nil, nil, d.ErrorPassthrough, moderation, d.Ops, d.Config)
	h.SetRelayDispatcher(d.Dispatcher)
	if d.Moderation != nil {
		// 安全审计在从节点本地判定（设计 3.4），与单机同一个协调器。
		h.SetSecurityAuditCoordinator(d.Moderation.Coordinator)
	}
	return h
}

// RouteOption 是 RegisterRoutes 的可选部件。
type RouteOption func(*routeOptions)

type routeOptions struct {
	asyncImages *handler.AsyncImageHandler
	paw         *PawNode
}

// WithAsyncImages 注册异步图片任务的提交与查询入口（nil 时这些入口交给主节点）。
func WithAsyncImages(h *handler.AsyncImageHandler) RouteOption {
	return func(o *routeOptions) { o.asyncImages = h }
}

// RegisterRoutes 注册从节点的网关路由。已接入的是 OpenAI 分组的 Responses（含 WebSocket）、Chat Completions、Messages；
// 其余请求原样交给主节点转发（开发计划 WP10 逐步接入）。中间件链对照本地 /v1 网关链（routes/gateway.go）：
// 全局 IP 黑名单、Key 鉴权、用户黑名单、未分组拦截合成准入中间件；自动分组、组合平台按模型问主节点
// （AutoGroupMiddleware、CompositeRouteMiddleware）；TypeSafe 等还没接入的分组在准入时回"暂不支持"。
//
// gh 是 Anthropic 分组的 Messages 处理函数（NewAnthropicHandler）；nil 时 Anthropic 分组交给主节点转发。
func RegisterRoutes(r *gin.Engine, h *handler.OpenAIGatewayHandler, d *Dispatcher, cfg *config.Config, gh *handler.GatewayHandler, opts ...RouteOption) {
	var ro routeOptions
	for _, opt := range opts {
		opt(&ro)
	}
	bodyLimit := middleware2.RequestBodyLimit(cfg.Gateway.MaxBodySize)
	chain := []gin.HandlerFunc{
		bodyLimit,
		middleware2.ClientRequestID(),
		handler.InboundEndpointMiddleware(),
		d.AdmitMiddleware(),
		d.AutoGroupMiddleware(),
		middleware2.GroupModelAllowlist(),
		d.CompositeRouteMiddleware(),
	}
	// byPlatform 与本地路由一样按分组平台分：OpenAI 分组走 OpenAI 网关，Anthropic / Gemini / Antigravity 平台的分组和没有分组的 Key
	// 走 Anthropic 网关（gateway，nil 时交给主节点），其余平台（Grok、国产兼容平台等）还没接入。
	byPlatform := func(openAI, gateway gin.HandlerFunc) gin.HandlerFunc {
		return func(c *gin.Context) {
			key, ok := middleware2.GetAPIKeyFromContext(c)
			switch {
			case ok && isOpenAICompatiblePlatform(servedPlatform(c, key)):
				openAI(c)
			case ok && gateway != nil && gh != nil && servesAnthropicRoutes(c, key):
				gateway(c)
			default:
				d.HandOff(c)
			}
		}
	}
	// openAIOnly：只服务 OpenAI 分组的入口（WebSocket、Embeddings、图片、alpha search），其余平台交给主节点。
	openAIOnly := func(next gin.HandlerFunc) gin.HandlerFunc {
		return func(c *gin.Context) {
			if key, ok := middleware2.GetAPIKeyFromContext(c); !ok || servedPlatform(c, key) != service.PlatformOpenAI {
				d.HandOff(c)
				return
			}
			next(c)
		}
	}
	// onPlatform：只服务指定平台（解析后的目标平台）的入口，其余（含还没选目标的组合平台分组）交给主节点：本地同样回 404 / 403，
	// 或由主节点按组合平台的规则处理。
	onPlatform := func(platform string, next gin.HandlerFunc) gin.HandlerFunc {
		return func(c *gin.Context) {
			if key, ok := middleware2.GetAPIKeyFromContext(c); !ok || servedPlatform(c, key) != platform {
				d.HandOff(c)
				return
			}
			next(c)
		}
	}
	// 图片入口与本地一样按分组平台分：OpenAI 分组走 OpenAI 图片，Grok 分组走 Grok 媒体，其余交给主节点（本地回 404）。
	images := func(c *gin.Context) {
		if key, ok := middleware2.GetAPIKeyFromContext(c); ok && servedPlatform(c, key) == service.PlatformGrok {
			h.GrokImages(c)
			return
		}
		openAIOnly(h.Images)(c)
	}
	responses := byPlatform(func(c *gin.Context) {
		if !service.IsForwardableOpenAIResponsesRequestPath(c) {
			// 不可转发的子路径由主节点照原逻辑处理。
			d.HandOff(c)
			return
		}
		if service.IsOpenAIResponsesInputTokensRequestPath(c) {
			h.ResponsesInputTokens(c)
			return
		}
		h.Responses(c)
	}, func(c *gin.Context) {
		if c.Param("subpath") != "" {
			// Anthropic 网关只接 /responses 本身，子路径交给主节点照本地逻辑处理。
			d.HandOff(c)
			return
		}
		gh.Responses(c)
	})
	chatCompletions := byPlatform(h.ChatCompletions, func(c *gin.Context) { gh.ChatCompletions(c) })
	// 与本地 countTokensHandler 一样按分组平台分：OpenAI 分组走 OpenAI 网关的 count_tokens，Anthropic / Gemini / Antigravity 平台的分组走
	// Messages 处理函数的 count_tokens；其余平台（Grok 本地估算、国产兼容平台）还没接入。
	countTokens := byPlatform(func(c *gin.Context) {
		// Grok 在本地估算（不选账号、不计费，不用问主节点）；OpenAI 与国产兼容平台选账号转发。
		if key, ok := middleware2.GetAPIKeyFromContext(c); ok && servedPlatform(c, key) == service.PlatformGrok {
			h.GrokCountTokens(c)
			return
		}
		h.CountTokens(c)
	}, func(c *gin.Context) { gh.CountTokens(c) })
	for _, prefix := range []string{"/v1", ""} {
		g := r.Group(prefix, chain...)
		g.POST("/messages/count_tokens", countTokens)
		g.POST("/responses", responses)
		g.POST("/responses/*subpath", responses)
		g.POST("/chat/completions", chatCompletions)
		g.POST("/images/generations", images)
		g.POST("/images/edits", images)
		if ro.asyncImages != nil {
			// 异步图片任务：提交在本机执行，任务状态在主节点；轮询落在任何节点都向主节点查。
			g.POST("/images/generations/async", ro.asyncImages.Submit)
			g.POST("/images/edits/async", ro.asyncImages.Submit)
			g.GET("/images/tasks/:task_id", ro.asyncImages.Get)
		}
		// Grok 视频（创建、编辑、延伸、状态、内容；本地 routes/gateway.go 的 video*Handler）。
		grok := func(next gin.HandlerFunc) gin.HandlerFunc { return onPlatform(service.PlatformGrok, next) }
		g.POST("/videos", grok(h.GrokVideoGeneration))
		g.POST("/videos/generations", grok(h.GrokVideoGeneration))
		g.POST("/videos/edits", grok(h.GrokVideoEdit))
		g.POST("/videos/extensions", grok(h.GrokVideoExtension))
		g.GET("/videos/generations/:request_id/content", grok(h.GrokVideoContent))
		g.GET("/videos/edits/:request_id/content", grok(h.GrokVideoContent))
		g.GET("/videos/extensions/:request_id/content", grok(h.GrokVideoContent))
		g.GET("/videos/generations/:request_id", grok(h.GrokVideoStatus))
		g.GET("/videos/edits/:request_id", grok(h.GrokVideoStatus))
		g.GET("/videos/extensions/:request_id", grok(h.GrokVideoStatus))
		g.GET("/videos/:request_id", grok(h.GrokVideoStatus))
		g.GET("/videos/:request_id/content", grok(h.GrokVideoContent))
		// xAI 语音（tts、stt、custom-voices）和 Realtime WebSocket，只有 Grok 分组。
		voice := func(endpoint string) gin.HandlerFunc {
			return grok(func(c *gin.Context) { h.GrokVoice(c, endpoint) })
		}
		customVoice := grok(func(c *gin.Context) { h.GrokVoice(c, grokCustomVoiceEndpoint(c)) })
		g.POST("/tts", voice("tts"))
		g.POST("/stt", voice("stt"))
		g.POST("/custom-voices", voice("custom-voices"))
		g.GET("/custom-voices", voice("custom-voices"))
		g.GET("/custom-voices/:voice_id/audio", customVoice)
		g.GET("/custom-voices/:voice_id", customVoice)
		g.PATCH("/custom-voices/:voice_id", customVoice)
		g.DELETE("/custom-voices/:voice_id", customVoice)
		g.GET("/realtime", grok(h.GrokRealtime))
		g.POST("/embeddings", middleware2.RequestBodyLimit(cfg.Gateway.TextMaxBodySize), openAIOnly(h.Embeddings))
		g.POST("/alpha/search", middleware2.RequestBodyLimit(cfg.Gateway.TextMaxBodySize), openAIOnly(h.AlphaSearch))
		// Responses WebSocket（Codex）：与本地一样是 GET /responses 的升级请求。
		g.GET("/responses", openAIOnly(h.ResponsesWebSocket))
		if prefix == "/v1" {
			// TypeSafe 的 Jev 判断请求：非 TypeSafe 分组本地处理函数自己回 404，不用问主节点。
			g.POST("/systemone", func(c *gin.Context) {
				if gh == nil {
					d.HandOff(c)
					return
				}
				gh.SystemOne(c)
			})
			// 与本地一致：只有 /v1/messages（OpenAI 兼容分组走 OpenAI 网关的 Messages）。
			g.POST("/messages", func(c *gin.Context) {
				// 与本地 /v1/messages 一样按分组平台分：OpenAI 分组走 OpenAI 网关的 Messages，Anthropic 分组走 Messages。
				key, ok := middleware2.GetAPIKeyFromContext(c)
				switch {
				case ok && isOpenAICompatiblePlatform(servedPlatform(c, key)):
					h.Messages(c)
				case ok && gh != nil && servesAnthropicRoutes(c, key):
					gh.Messages(c)
				default:
					d.HandOff(c)
				}
			})
		}
	}
	// Seedance 任务入口（OpenAI 分组；本地 rootRoute 同样四个前缀）。
	for _, prefix := range []string{"/api/v3", "/v3", "/v1", ""} {
		sg := r.Group(prefix, chain...)
		sg.POST("/contents/generations/tasks", onPlatform(service.PlatformOpenAI, h.SeedanceTasks))
		sg.GET("/contents/generations/tasks/:task_id", onPlatform(service.PlatformOpenAI, h.SeedanceTasks))
		sg.DELETE("/contents/generations/tasks/:task_id", onPlatform(service.PlatformOpenAI, h.SeedanceTasks))
	}
	// Codex 直连路径 /backend-api/codex/*：与本地一样的链路，responses（含子路径、WebSocket）和 alpha search 走 OpenAI 网关。
	// 实时会话（/realtime/calls、/:call_id）、模型列表等仍交给主节点。
	codexDirect := r.Group("/backend-api/codex", chain...)
	codexDirect.POST("/responses", responses)
	codexDirect.POST("/responses/*subpath", responses)
	codexDirect.POST("/alpha/search", middleware2.RequestBodyLimit(cfg.Gateway.TextMaxBodySize), openAIOnly(h.AlphaSearch))
	codexDirect.GET("/responses", openAIOnly(h.ResponsesWebSocket))
	if gh != nil {
		// Grok 分组的独立搜索入口：非 Grok 分组本地处理函数自己回 400，不用问主节点。
		for _, prefix := range []string{"/v1", ""} {
			searchChain := r.Group(prefix, chain...)
			searchChain.POST("/web_search", func(c *gin.Context) { gh.WebSearch(c) })
			searchChain.POST("/x_search", func(c *gin.Context) { gh.XSearch(c) })
		}
	}
	if gh != nil {
		// Gemini 原生入口（SDK / CLI 直连）：链路对照本地 /v1beta 组（准入含 Google 格式的鉴权与错误、自动分组按 URL 模型、
		// 分组模型白名单、组合平台选目标）。模型列表等 GET 请求不在这里，仍交给主节点。
		gemini := r.Group("/v1beta", bodyLimit, middleware2.ClientRequestID(), handler.InboundEndpointMiddleware(),
			d.AdmitMiddleware(), d.AutoGroupMiddleware(), middleware2.GroupModelAllowlist(), d.CompositeGeminiRouteMiddleware())
		gemini.POST("/models/*modelAction", gh.GeminiV1BetaModels)

		// Antigravity 专用入口（强制 Antigravity 平台，不看分组平台；本地 /antigravity/v1、/antigravity/v1beta 组）：链路同上但没有
		// 组合平台选目标。强制平台由节点路由和主节点各按路径定。模型列表、用量等 GET 请求仍交给主节点。
		forced := middleware2.ForcePlatform(service.PlatformAntigravity)
		antigravityChain := func() []gin.HandlerFunc {
			return []gin.HandlerFunc{bodyLimit, middleware2.ClientRequestID(), handler.InboundEndpointMiddleware(), forced,
				d.AdmitMiddleware(), d.AutoGroupMiddleware(), middleware2.GroupModelAllowlist()}
		}
		antigravityV1 := r.Group("/antigravity/v1", antigravityChain()...)
		antigravityV1.POST("/messages", gh.Messages)
		antigravityV1.POST("/messages/count_tokens", gh.CountTokens)
		antigravityV1Beta := r.Group("/antigravity/v1beta", antigravityChain()...)
		antigravityV1Beta.POST("/models/*modelAction", gh.GeminiV1BetaModels)
	}
	if ro.paw != nil {
		ro.paw.register(r, h, gh, d, cfg)
	}
	r.NoRoute(bodyLimit, func(c *gin.Context) {
		if !handOffAllowed(c.Request.URL.Path) {
			// 设计 8.3：从节点只开放网关接口，登录态接口、管理后台、支付、网页、小白端的配置接口一律 404，不转给主节点。
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Not found"}})
			return
		}
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

// handOffPrefixes 是可以交给主节点的路径前缀（设计 8.3 的 API Key 一行 + 8.4 的非转发接口）；
// handOffRoots 是不带 /v1 前缀的根路径别名（本地路由同样注册的那些）。
var (
	handOffPrefixes = []string{"/v1/", "/v1beta/", "/backend-api/codex/", "/antigravity/", "/api/v3/", "/v3/"}
	handOffRoots    = []string{"/responses", "/chat/completions", "/models", "/messages", "/images", "/videos", "/embeddings", "/tts", "/stt",
		"/custom-voices", "/realtime", "/alpha/search", "/contents/generations/tasks", "/web_search", "/x_search", "/live", "/usage"}
)

// handOffAllowed 报告这个路径可以交给主节点转发或执行。
func handOffAllowed(path string) bool {
	for _, p := range handOffPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	for _, root := range handOffRoots {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return path == "/v1" || path == "/v1beta" || path == "/backend-api/codex" || path == "/antigravity"
}

// grokCustomVoiceEndpoint 是自定义语音路径对应的入口名（本地 routes 同名函数）。
func grokCustomVoiceEndpoint(c *gin.Context) string {
	endpoint := "custom-voices/" + c.Param("voice_id")
	if strings.HasSuffix(c.FullPath(), "/:voice_id/audio") {
		endpoint += "/audio"
	}
	return endpoint
}

// servesAnthropicRoutes 报告这次请求走 Anthropic 网关（Messages、count_tokens）：Anthropic、Gemini、Antigravity 平台的分组（含组合平台
// 选到 Anthropic / Gemini 的），或没有分组的 Key（后台允许未分组 Key 调度时，本地同样走 Anthropic 网关）。
func servesAnthropicRoutes(c *gin.Context, key *service.APIKey) bool {
	if key == nil {
		return false
	}
	switch servedPlatform(c, key) {
	case service.PlatformAnthropic, service.PlatformGemini, service.PlatformAntigravity:
		return true
	}
	return key.Group == nil
}

// relayStickyCache 是 Antigravity 转发服务在从节点上的缓存：除了转发路径上清粘性会话绑定（限流、重试失败时）作为账号事件交给
// 主节点（只认这次请求自己的会话键），其余同 NoopGatewayCache。
type relayStickyCache struct {
	NoopGatewayCache
	reporter *node.RemoteAccountReporter
}

func (c relayStickyCache) DeleteSessionAccountID(ctx context.Context, _ int64, sessionKey string) error {
	if a := attemptFrom(ctx); a != nil && c.reporter != nil {
		c.reporter.StickySessionCleared(a.accountID, sessionKey)
	}
	return nil
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

// isOpenAICompatiblePlatform 报告 OpenAI 网关的 Responses / Chat / Messages 入口服务的分组平台（本地 isOpenAIResponsesCompatibleGatewayPlatform）。
func isOpenAICompatiblePlatform(platform string) bool {
	switch platform {
	case service.PlatformOpenAI, service.PlatformGrok, service.PlatformKimi, service.PlatformZhipu, service.PlatformDeepseek,
		service.PlatformMiniMax, service.PlatformOpenCodeGo:
		return true
	}
	return false
}
