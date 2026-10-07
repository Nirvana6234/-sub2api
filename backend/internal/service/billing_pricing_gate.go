package service

import (
	"context"
	"fmt"
	"strings"
)

// CheckBillablePricing 在转发之前确认「这次请求有价可收」。
//
// 背景：目录里没有价格的模型，扣费阶段只会记 0 元（OpenAI 网关的
// pricing_missing_record_zero_cost）或扣费失败，用户等于白用。这里把它前移成
// 请求入口的硬拦截——任何候选模型名（请求名、渠道映射名）能解析出有效价格就放行，
// 全都解析不出来才拒绝。
//
// 放行的情况：
//   - 分组价卡 / 渠道定价明确配置了该模型（管理员显式定价，含按次、图片、视频模式）；
//   - 非 token 计费模式；
//   - 目录或回退价里该模型有大于 0 的输入或输出单价；
//   - 分组倍率为 0（管理员明确设定免费分组）；
//   - 图片 / 视频生成模型：它们走自己的图片、视频计价路径，不在这里判断。
func CheckBillablePricing(ctx context.Context, resolver *ModelPricingResolver, apiKey *APIKey, models ...string) error {
	if resolver == nil || apiKey == nil {
		return nil
	}
	group := apiKey.Group
	if group != nil && group.RateMultiplier == 0 {
		return nil
	}
	var groupID *int64
	if group != nil {
		gid := group.ID
		groupID = &gid
	} else if apiKey.GroupID != nil {
		groupID = apiKey.GroupID
	}

	seen := make(map[string]struct{}, len(models))
	checked := 0
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		if IsGPTImageGenerationModel(model) || isGrokVideoBillingModel(model) {
			return nil
		}
		checked++
		resolved := resolver.Resolve(ctx, PricingInput{Model: model, GroupID: groupID, Group: group})
		if resolvedPricingIsBillable(resolved) {
			return nil
		}
	}
	if checked == 0 {
		return nil
	}
	return fmt.Errorf("%w for model: %s", ErrModelPricingUnavailable, strings.Join(sortedKeysOf(seen), ","))
}

func resolvedPricingIsBillable(resolved *ResolvedPricing) bool {
	if resolved == nil {
		return false
	}
	if resolved.Mode != "" && resolved.Mode != BillingModeToken {
		return true
	}
	if resolved.Source == PricingSourceChannel || resolved.Source == PricingSourceGroup {
		return true
	}
	base := resolved.BasePricing
	return base != nil && (base.InputPricePerToken > 0 || base.OutputPricePerToken > 0)
}

func sortedKeysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// CheckBillablePricing 为 GatewayService 暴露的入口，候选为请求名与渠道映射名。
func (s *GatewayService) CheckBillablePricing(ctx context.Context, apiKey *APIKey, models ...string) error {
	if s == nil {
		return nil
	}
	return CheckBillablePricing(ctx, s.resolver, apiKey, models...)
}

// CheckBillablePricing 为 OpenAIGatewayService 暴露的入口。
func (s *OpenAIGatewayService) CheckBillablePricing(ctx context.Context, apiKey *APIKey, models ...string) error {
	if s == nil {
		return nil
	}
	return CheckBillablePricing(ctx, s.resolver, apiKey, models...)
}
