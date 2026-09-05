package service

import (
	"math"
	"strings"
)

// 「稀缺能力保护」：同一分组里，只支持窄模型集合的账号应当优先被用掉，把同时支持
// 更多模型的账号留给那些非它不可的请求。
//
// 举个会出问题的场景：分组里既有只支持 5.5 的号，也有 5.5/5.6 都支持的号。调度只按
// 优先级、负载、成本排序，不看「这个号还能干什么别的」，于是 5.5 的流量可能把双能力
// 账号的配额烧掉；等 5.6 请求来了发现无号可用，而那些只支持 5.5 的号一直闲着。
//
// 这里用「分档」而不是加权：候选中若存在更专精的一档，就只用这一档。这样不需要调
// 权重，行为也可预测。回落是自动的——专精那档一旦被限流、配额耗尽或负载打满，早在
// 本函数之前的可调度性筛选里就已经出局，候选集自然只剩更宽的号。

// modelBreadthUnbounded 表示「可服务任意模型」，即最不专精。
const modelBreadthUnbounded = math.MaxInt32

// accountModelBreadth 返回账号可服务模型集合的宽度，数值越小越专精。
//
// 判定依据是账号的 model_mapping：空映射意味着放行所有模型（见 IsModelSupported），
// 透传模式同理。含 "*" 通配的映射虽然条目数少，实际覆盖面却是无界的，必须按最宽处理，
// 否则一个 {"*": "..."} 的账号会被误判成「最专精」而抢走所有流量。
func accountModelBreadth(account *Account) int {
	if account == nil {
		return modelBreadthUnbounded
	}
	if account.IsOpenAIPassthroughEnabled() {
		return modelBreadthUnbounded
	}
	mapping := account.GetModelMapping()
	if len(mapping) == 0 {
		return modelBreadthUnbounded
	}
	for pattern := range mapping {
		if strings.Contains(pattern, "*") {
			return modelBreadthUnbounded
		}
	}
	return len(mapping)
}

// keepMostSpecialized 就地保留最专精的一档候选。
//
// items 必须是已经过模型支持与可调度性筛选的候选集——本函数只做「同样能服务本次请求
// 的账号里，谁更专精」的取舍，不承担任何可用性判断。
//
// 全部候选宽度相同时（最常见的情况）原样返回，不产生任何行为变化。
func keepMostSpecialized[T any](items []T, accountOf func(T) *Account) []T {
	if len(items) < 2 {
		return items
	}
	best := modelBreadthUnbounded
	for _, item := range items {
		if b := accountModelBreadth(accountOf(item)); b < best {
			best = b
		}
	}
	if best == modelBreadthUnbounded {
		// 没有任何账号是受限的，无从比较专精度。
		return items
	}
	kept := items[:0]
	for _, item := range items {
		if accountModelBreadth(accountOf(item)) == best {
			kept = append(kept, item)
		}
	}
	return kept
}

// preferSpecializedAccountsEnabled 报告是否启用稀缺能力保护。默认关闭。
func (s *GatewayService) preferSpecializedAccountsEnabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.PreferSpecializedAccounts
}

func (s *OpenAIGatewayService) preferSpecializedAccountsEnabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.PreferSpecializedAccounts
}
