package service

import "strings"

// modelVendorPlatform 按模型名前缀猜模型所属厂商（平台常量）；认不出返回空串。
//
// 只用于"这个分组该列出哪些模型"这类展示决策：账号映射里常有跨厂商的兼容别名
// （例如 GLM 账号把 gpt-5.6-sol 映射到 glm-5.3，好让 Codex 直接用），
// 这些别名不是该厂商的模型，不应出现在分组的模型列表里。
func modelVendorPlatform(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case m == "":
		return ""
	case strings.HasPrefix(m, "claude"):
		return PlatformAnthropic
	case strings.HasPrefix(m, "gpt"), strings.HasPrefix(m, "chatgpt"), strings.HasPrefix(m, "codex"),
		strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"),
		strings.HasPrefix(m, "dall-e"), strings.HasPrefix(m, "text-embedding"):
		return PlatformOpenAI
	case strings.HasPrefix(m, "gemini"), strings.HasPrefix(m, "imagen"), strings.HasPrefix(m, "veo"):
		return PlatformGemini
	case strings.HasPrefix(m, "grok"):
		return PlatformGrok
	case strings.HasPrefix(m, "glm"), strings.HasPrefix(m, "chatglm"), strings.HasPrefix(m, "cogview"):
		return PlatformZhipu
	case strings.HasPrefix(m, "kimi"), strings.HasPrefix(m, "moonshot"):
		return PlatformKimi
	case strings.HasPrefix(m, "deepseek"):
		return PlatformDeepseek
	case strings.HasPrefix(m, "minimax"), strings.HasPrefix(m, "abab"):
		return PlatformMiniMax
	case strings.HasPrefix(m, "jev"):
		return PlatformTypeSafe
	default:
		return ""
	}
}

// groupListsModelVendor 报告某厂商的模型能否出现在该平台分组的模型列表里。
// 认不出厂商的模型名（如自定义的 "Kun"）一律保留。
func groupListsModelVendor(groupPlatform, vendor string) bool {
	if vendor == "" {
		return true
	}
	switch groupPlatform {
	case PlatformComposite, PlatformOpenCodeGo, "":
		return true
	case PlatformAntigravity:
		return vendor == PlatformAnthropic || vendor == PlatformGemini
	default:
		return vendor == groupPlatform
	}
}

// groupListsModel 报告模型名是否属于该平台分组应列出的厂商。
func groupListsModel(groupPlatform, model string) bool {
	return groupListsModelVendor(strings.TrimSpace(groupPlatform), modelVendorPlatform(model))
}
