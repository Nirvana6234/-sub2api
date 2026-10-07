package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func newPricingGateResolver(t *testing.T, catalog string) *ModelPricingResolver {
	t.Helper()
	pricingSvc := &PricingService{}
	data, err := pricingSvc.parsePricingData([]byte(catalog))
	require.NoError(t, err)
	pricingSvc.pricingData = data
	return NewModelPricingResolver(nil, NewBillingService(&config.Config{}, pricingSvc))
}

const pricingGateCatalog = `{
  "priced-model": {"input_cost_per_token": 1e-6, "output_cost_per_token": 2e-6, "litellm_provider": "openai", "mode": "chat"},
  "zero-model": {"input_cost_per_token": 0, "output_cost_per_token": 0, "litellm_provider": "openai", "mode": "chat"},
  "orphan-cache-model": {
    "input_cost_per_token": 1e-6, "output_cost_per_token": 2e-6,
    "cache_creation_input_token_cost_above_200k_tokens": 2.5e-6,
    "litellm_provider": "gemini", "mode": "chat"
  },
  "based-cache-model": {
    "input_cost_per_token": 1e-6, "output_cost_per_token": 2e-6,
    "cache_creation_input_token_cost": 1.25e-6,
    "cache_creation_input_token_cost_above_200k_tokens": 2.5e-6,
    "litellm_provider": "gemini", "mode": "chat"
  }
}`

func TestCheckBillablePricing(t *testing.T) {
	resolver := newPricingGateResolver(t, pricingGateCatalog)
	key := &APIKey{Group: &Group{ID: 1, RateMultiplier: 1}}

	t.Run("有价放行", func(t *testing.T) {
		require.NoError(t, CheckBillablePricing(context.Background(), resolver, key, "priced-model"))
	})
	t.Run("目录里没有就拒绝", func(t *testing.T) {
		err := CheckBillablePricing(context.Background(), resolver, key, "no-such-model")
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrModelPricingUnavailable))
	})
	t.Run("目录单价为 0 就拒绝", func(t *testing.T) {
		require.Error(t, CheckBillablePricing(context.Background(), resolver, key, "zero-model"))
	})
	t.Run("任一候选名有价即放行", func(t *testing.T) {
		require.NoError(t, CheckBillablePricing(context.Background(), resolver, key, "alias-without-price", "priced-model"))
	})
	t.Run("缓存分档缺基础价的条目被排除，请求被拒", func(t *testing.T) {
		require.Error(t, CheckBillablePricing(context.Background(), resolver, key, "orphan-cache-model"))
		require.NoError(t, CheckBillablePricing(context.Background(), resolver, key, "based-cache-model"))
	})
	t.Run("免费分组（倍率 0）不拦", func(t *testing.T) {
		free := &APIKey{Group: &Group{ID: 2, RateMultiplier: 0}}
		require.NoError(t, CheckBillablePricing(context.Background(), resolver, free, "no-such-model"))
	})
	t.Run("图片模型走自己的计价，不在这里拦", func(t *testing.T) {
		require.NoError(t, CheckBillablePricing(context.Background(), resolver, key, "gpt-image-2"))
	})
	t.Run("没有候选或没有 resolver 时不拦", func(t *testing.T) {
		require.NoError(t, CheckBillablePricing(context.Background(), resolver, key))
		require.NoError(t, CheckBillablePricing(context.Background(), nil, key, "no-such-model"))
	})
}
