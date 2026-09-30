//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 从节点：转发路径上按分组查渠道读主节点给的功能配置，不查渠道服务；没有给时照原来查（单机、主节点）。
func TestChannelForGroupReadsRelayFeatures(t *testing.T) {
	ctx := WithRelayChannelFeatures(context.Background(), map[string]any{
		featureKeyBedrockCCCompat:            true,
		featureKeyWebSearchEmulation:         map[string]any{PlatformAnthropic: true},
		featureKeyCodexImageGenerationBridge: map[string]any{PlatformOpenAI: true},
	})
	ch, err := channelForGroup(ctx, nil, 7)
	require.NoError(t, err)
	require.True(t, ch.IsBedrockCCCompatEnabled(PlatformAnthropic))
	require.True(t, ch.IsWebSearchEmulationEnabled(PlatformAnthropic))
	require.False(t, ch.IsWebSearchEmulationEnabled(PlatformOpenAI))

	groupID := int64(7)
	gw := &GatewayService{}
	require.True(t, gw.isBedrockCCCompatEnabled(ctx, &Account{Platform: PlatformAnthropic}, &groupID))

	openAI := &OpenAIGatewayService{}
	require.True(t, openAI.isCodexImageGenerationBridgeEnabled(ctx, &Account{Platform: PlatformOpenAI}, &APIKey{GroupID: &groupID}),
		"the channel-level override from the master applies on the node")

	// 分组没有渠道：主节点给的是空的。
	none := WithRelayChannelFeatures(context.Background(), nil)
	ch, err = channelForGroup(none, nil, 7)
	require.NoError(t, err)
	require.Nil(t, ch)
	require.False(t, gw.isBedrockCCCompatEnabled(none, &Account{Platform: PlatformAnthropic}, &groupID))

	// 没有给（单机）且没有渠道服务：与原来一样当作没有渠道。
	ch, err = channelForGroup(context.Background(), nil, 7)
	require.NoError(t, err)
	require.Nil(t, ch)
}
