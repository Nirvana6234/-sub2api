package service

import (
	"context"
	"strings"
)

// 客户端来源。在登录（出示密码）那一刻确定，随会话家族保存，之后不再从请求里读。
//
// 为什么绑在登录上而不是每个请求自报：自报等于「谁都能给自己开豁免」。绑在登录上
// 之后，想拿到一个带豁免的会话就必须知道密码——而知道密码的人本来也不需要绕过会话
// 绑定。偷到 refresh token 的人同样改不了它：轮转时读的是家族记录（RefreshTokenData），
// 不是刷新请求。
const (
	// ClientSourceWeb 网页端。缺失或无法识别的来源都按它处理，所以现有前端、
	// 旧版客户端和 OAuth 回调一行都不用改，行为与加这个字段之前完全一致。
	ClientSourceWeb = "web"

	// ClientSourceDesktop 桌面客户端（共飞-ChatGPT助手）。
	ClientSourceDesktop = "desktop"
)

type clientSourceCtxKey struct{}

// WithClientSource 把登录请求声明的来源注入 context，供签发 token 的路径读取。
func WithClientSource(ctx context.Context, source string) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, clientSourceCtxKey{}, NormalizeClientSource(source))
}

// ClientSourceFromContext 取出来源；未注入时返回网页端。
func ClientSourceFromContext(ctx context.Context) string {
	if ctx == nil {
		return ClientSourceWeb
	}
	source, _ := ctx.Value(clientSourceCtxKey{}).(string)
	return NormalizeClientSource(source)
}

// NormalizeClientSource 归一化来源；不认识的值一律按网页端处理。
//
// 保守方向是刻意的：认不出来就走最严格的那套策略，而不是放宽。新增一种来源时
// 忘了在这里登记，后果是「多校验了一道」，不是「悄悄少校验了一道」。
func NormalizeClientSource(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case ClientSourceDesktop:
		return ClientSourceDesktop
	default:
		return ClientSourceWeb
	}
}

// skipSessionBinding 报告这次签发是否不写会话指纹（不写 = 该会话豁免 IP/UA 绑定，
// 因为两处校验点都在指纹为空时放行）。
//
// 两条各自独立成立的理由：
//
//  1. 来源声明为桌面客户端——见 clientSourceSkipsSessionBinding。
//
//  2. **请求根本没有 User-Agent。** 这既是原则也是兼容：指纹是
//     sha256(IP网段 + "\n" + UA)，没有 UA 时它退化成一把 /24 级别的纯 IP 锁——
//     同网段重放照样通过，挡住的只有正常换网的用户，全是代价没有收益。而浏览器
//     必然带 UA，所以网页端不受这条影响。
//
//     兼容的那一面：0.5 及更早的桌面客户端既不发 source、也不发 User-Agent
//     （.NET 的 HttpClient 默认不带，与 curl、浏览器、reqwest 都不同）。靠这条，
//     **已经发出去的客户端不用升级**就能在下一次刷新轮转时自动转成豁免会话。
func skipSessionBinding(source string, binding *SessionBinding) bool {
	if clientSourceSkipsSessionBinding(source) {
		return true
	}
	return binding != nil && strings.TrimSpace(binding.UserAgent) == ""
}

// clientSourceSkipsSessionBinding 报告该来源的会话是否不做 IP/UA 绑定。
//
// 桌面客户端豁免。它 7×24 挂在托盘里、每 15~60 秒轮询一次，而绑定是**逐请求**
// 校验的：宽带重拨、Wi-Fi 与移动网络切换、睡眠唤醒换网都会换网段，换完之后的
// 第一次轮询就会被判成「会话被搬到了别的网络环境」，撤销整条登录链。持续在线
// 在这套校验里不产生任何信任积累，正常用网就会被误杀。
//
// 而它拦得住的东西也有限：这个客户端不发 User-Agent，指纹里只剩 IP，实际上就是
// 一把 /24 级别的 IP 锁——同网段重放照样通过。真正的盗用防护是 refresh token 的
// 轮转与复用检测，那部分不受这里影响。
func clientSourceSkipsSessionBinding(source string) bool {
	return NormalizeClientSource(source) == ClientSourceDesktop
}
