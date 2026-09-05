package httpserver

import (
	"strings"
	"testing"

	"transithub/backend/internal/modules/my_sites"
)

// 线上库里存的就是这份 markdown 默认模板。它必须被认成「内置」并自动升级，
// 否则用户要先进设置页手动保存一次才能看到影响面——那这次改动等于白做。
const liveDefaultMultiplierTemplate = "🟠 **倍率变更预警**\n\n🏷️ **站点：** {siteName}\n📦 **分组：** {groupName}\n📊 **倍率：** {oldRate}x → **{newRate}x**（{changeDirection}）\n\n🔎 请确认成本变化，并检查下游定价策略。"

func TestBuiltInMultiplierTemplateUpgrades(t *testing.T) {
	if !isBuiltInMultiplierTemplate(liveDefaultMultiplierTemplate) {
		t.Fatal("线上现役默认模板没有被识别成内置模板，升级不会生效")
	}

	msg := formatMultiplierChange("tkapi", "Codex｜快速通道｜", 0.055, 0.065,
		liveDefaultMultiplierTemplate, impactWithCost(700))

	if !strings.Contains(msg, "¥700.00") {
		t.Fatalf("内置模板应升级到带成本的新版：\n%s", msg)
	}
	if !strings.Contains(msg, "Codex 快速") {
		t.Fatalf("内置模板应升级到带自有分组的新版：\n%s", msg)
	}
	// 升级后仍然是 markdown 风格：库里存的 format 就是 markdown，
	// 换成纯文本会让通知突然掉格式。
	if !strings.Contains(msg, "**") {
		t.Fatalf("markdown 内置模板升级后应保持加粗：\n%s", msg)
	}
}

// 用户手写过的模板一个字都不能动。
func TestHandWrittenMultiplierTemplateUntouched(t *testing.T) {
	custom := "我自己的模板 {siteName} {oldRate}->{newRate}"
	if isBuiltInMultiplierTemplate(custom) {
		t.Fatal("手写模板被误判成内置模板")
	}
	msg := formatMultiplierChange("tkapi", "g", 0.055, 0.065, custom, impactWithCost(700))
	if msg != "我自己的模板 tkapi 0.0550->0.0650" {
		t.Fatalf("手写模板被改动了：%s", msg)
	}
}

// 空模板走纯文本默认值，不该被升级逻辑带成 markdown。
func TestEmptyTemplateUsesPlainDefault(t *testing.T) {
	msg := formatMultiplierChange("tkapi", "g", 0.055, 0.065, "", my_sites.GroupImpact{})
	if strings.Contains(msg, "**") {
		t.Fatalf("空模板应使用纯文本默认值：\n%s", msg)
	}
}
