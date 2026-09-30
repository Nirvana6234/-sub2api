package service

import "context"

type relayChannelFeaturesCtxKey struct{}

// relayChannelFeatures 是主从分流的从节点上这次请求的分组所属渠道的功能配置（选号时主节点给的）。
type relayChannelFeatures struct {
	features map[string]any
}

// WithRelayChannelFeatures 把主节点选号时给的渠道功能配置放进请求 ctx（从节点用）：转发路径上按分组查渠道的地方
// （联网搜索模拟"跟随渠道"、Bedrock CC 兼容、Codex 生图桥接的渠道级开关）读它，不查渠道服务（渠道在主节点）。
// features 为 nil 表示分组没有渠道。
func WithRelayChannelFeatures(ctx context.Context, features map[string]any) context.Context {
	return context.WithValue(ctx, relayChannelFeaturesCtxKey{}, relayChannelFeatures{features: features})
}

// channelForGroup 是转发路径上查分组所属渠道：从节点上用主节点给的功能配置，否则查渠道服务（没有时为 nil）。
func channelForGroup(ctx context.Context, cs *ChannelService, groupID int64) (*Channel, error) {
	if v, ok := ctx.Value(relayChannelFeaturesCtxKey{}).(relayChannelFeatures); ok {
		if v.features == nil {
			return nil, nil
		}
		return &Channel{FeaturesConfig: v.features}, nil
	}
	if cs == nil {
		return nil, nil
	}
	return cs.GetChannelForGroup(ctx, groupID)
}

// ChannelFeaturesForGroup 是分组所属渠道的功能配置（副本；没有渠道时为 nil）。主节点选号时给从节点。
func (s *OpenAIGatewayService) ChannelFeaturesForGroup(ctx context.Context, groupID int64) (map[string]any, error) {
	if s == nil || s.channelService == nil {
		return nil, nil
	}
	ch, err := s.channelService.GetChannelForGroup(ctx, groupID)
	if err != nil || ch == nil || ch.FeaturesConfig == nil {
		return nil, err
	}
	return deepCopyFeaturesConfig(ch.FeaturesConfig), nil
}
