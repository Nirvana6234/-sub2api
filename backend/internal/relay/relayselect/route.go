package relayselect

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/Wei-Shaw/sub2api/internal/relay/keycodec"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// resolveComposite 给组合平台分组按公开模型选目标（本地 compositeTargetPlatformMiddleware 同一个选法）；
// 不是组合平台分组、没有模型时返回未匹配。
func (s *selector) resolveComposite(ctx context.Context, apiKey *service.APIKey, model, path string) (service.CompositeRouteDecision, error) {
	if apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformComposite || model == "" || s.deps.Composite == nil {
		return service.CompositeRouteDecision{}, nil
	}
	return s.deps.Composite.Resolve(ctx, apiKey.Group.ID, model, service.CompositeRouteEndpointForPath(path))
}

// openAICompatiblePlatforms 是 OpenAI 网关的 Responses / Chat / Messages 入口能服务的分组平台（本地 isOpenAIResponsesCompatibleGatewayPlatform）。
var openAICompatiblePlatforms = []string{
	service.PlatformOpenAI, service.PlatformGrok, service.PlatformKimi, service.PlatformZhipu, service.PlatformDeepseek,
	service.PlatformMiniMax, service.PlatformOpenCodeGo,
}

func isOpenAICompatiblePlatform(platform string) bool {
	return slices.Contains(openAICompatiblePlatforms, platform)
}

// openAIRequestPlatform 是这次请求按 OpenAI 网关调度时的平台（本地 openAICompatibleRequestPlatform）：组合平台分组看选定的目标，
// 其余看分组平台；text 为 false 的入口（Embeddings、图片、alpha search）只服务 OpenAI。ok 为 false 时这个入口接不了。
func openAIRequestPlatform(apiKey *service.APIKey, decision service.CompositeRouteDecision, text bool) (platform string, ok bool) {
	platform = groupPlatformOf(apiKey)
	if platform == service.PlatformComposite {
		if !decision.Matched {
			return "", false
		}
		platform = decision.TargetPlatform
	}
	if text {
		ok = isOpenAICompatiblePlatform(platform)
	} else {
		ok = platform == service.PlatformOpenAI
	}
	return service.NormalizeOpenAICompatiblePlatform(platform), ok
}

// openAICompatAccountServed 报告从节点能不能转发这个账号：OpenAI 平台的账号都接；Grok、国产兼容平台只接 API Key 账号
// （Grok OAuth 的凭据刷新与失败处理还在主节点）。
func openAICompatAccountServed(requestPlatform string, a *service.Account) bool {
	if requestPlatform == service.PlatformOpenAI {
		return true
	}
	return a != nil && a.Type == service.AccountTypeAPIKey
}

// compositeServedBy 报告组合平台分组这次选定的目标是不是 target（入口自己接的平台）；不是组合平台分组时都接。
func compositeServedBy(apiKey *service.APIKey, decision service.CompositeRouteDecision, target string) bool {
	if apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformComposite {
		return true
	}
	return decision.Matched && decision.TargetPlatform == target
}

// ResolveRoute 见 RelayControl.ResolveRoute：Key 按准入同一段复查，自动分组 Key 按模型选分组（带了已定的分组时
// 只核对），组合平台分组按模型选目标。
func (s *selector) ResolveRoute(ctx context.Context, nodeID int64, req *relayv1.ResolveRouteRequest) (*relayv1.ResolveRouteResponse, error) {
	ctx = withCallingNode(ctx, nodeID)
	adm, rej, err := s.admitAPIKey(ctx, req.GetApiKey(), req.GetClientIp(), req.GetMethod(), req.GetPath(), nil,
		autoGroupChoice{pinned: req.GetAutoGroupId(), model: req.GetModel()}, relayServedFor(req.GetPath())...)
	if err != nil {
		return nil, err
	}
	if rej != nil {
		return &relayv1.ResolveRouteResponse{Result: &relayv1.ResolveRouteResponse_Rejection{Rejection: rej.GetRejection()}}, nil
	}
	s.admitted.note(nodeID, adm.APIKey.User.ID, s.now())
	decision, err := s.resolveComposite(ctx, adm.APIKey, req.GetModel(), req.GetPath())
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(decision)
	if err != nil {
		return nil, err
	}
	resolution := &relayv1.RouteResolution{CompositeDecision: raw}
	if adm.APIKey.AutoGroup {
		if resolution.ApiKey, err = keycodec.EncodeAPIKey(adm.APIKey); err != nil {
			return nil, err
		}
		if resolution.Subscription, err = keycodec.EncodeSubscription(adm.Billing.Subscription); err != nil {
			return nil, err
		}
	}
	return &relayv1.ResolveRouteResponse{Result: &relayv1.ResolveRouteResponse_Resolution{Resolution: resolution}}, nil
}
