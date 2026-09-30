package relayselect

import (
	"context"
	"encoding/json"

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

// compositeServedByNode 报告组合平台分组这次选定的目标从节点能不能接（从节点目前只有 OpenAI 平台的处理函数）。
func compositeServedByNode(apiKey *service.APIKey, decision service.CompositeRouteDecision) bool {
	if apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformComposite {
		return true
	}
	return decision.Matched && decision.TargetPlatform == service.PlatformOpenAI
}

// ResolveRoute 见 RelayControl.ResolveRoute：Key 按准入同一段复查，自动分组 Key 按模型选分组（带了已定的分组时
// 只核对），组合平台分组按模型选目标。
func (s *selector) ResolveRoute(ctx context.Context, nodeID int64, req *relayv1.ResolveRouteRequest) (*relayv1.ResolveRouteResponse, error) {
	adm, rej, err := s.admitAPIKey(ctx, req.GetApiKey(), req.GetClientIp(), req.GetMethod(), req.GetPath(), nil,
		autoGroupChoice{pinned: req.GetAutoGroupId(), model: req.GetModel()})
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
