package httpserver

import (
	"strings"
	"testing"

	"transithub/backend/internal/modules/my_sites"
)

func impactWithCost(cost float64) my_sites.GroupImpact {
	return my_sites.GroupImpact{
		OwnGroups:    []my_sites.OwnGroupRate{{Name: "Codex 快速", Multiplier: 0.6}},
		CostCNY:      cost,
		Days:         7,
		CostResolved: true,
	}
}

// 预警要能直接回答「这次变价对我影响多大」，所以影响面必须真的出现在正文里。
func TestFormatMultiplierChangeIncludesImpact(t *testing.T) {
	msg := formatMultiplierChange("tkapi", "Codex｜快速通道｜", 0.055, 0.065, "", impactWithCost(700))

	for _, want := range []string{
		"Codex 快速", // 对接的自有分组
		"0.6x",     // 我方当前倍率
		"¥700.00",  // 近 7 天成本
		"¥100.00",  // 日均
		"+18.2%",   // 倍率涨幅
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("预警正文缺少 %q：\n%s", want, msg)
		}
	}

	// 0.055 → 0.065 是涨 18.18%，700 元的用量对应约 +127.27 元。
	if !strings.Contains(msg, "+¥127.27") {
		t.Fatalf("成本影响估算不对：\n%s", msg)
	}
}

func TestFormatMultiplierChangeDownward(t *testing.T) {
	msg := formatMultiplierChange("tkapi", "g", 0.1, 0.05, "", impactWithCost(200))
	if !strings.Contains(msg, "下降") {
		t.Fatalf("降价应标成下降：\n%s", msg)
	}
	if !strings.Contains(msg, "-¥100.00") {
		t.Fatalf("降价的成本影响应为负：\n%s", msg)
	}
}

// 成本查不到时必须说清原因。显示 ¥0.00 是最坏的选择：
// 「没绑账号」和「真没跑量」会被读成同一件事，前者要去补绑定，后者不用管。
func TestFormatMultiplierChangeUnresolvedCost(t *testing.T) {
	impact := my_sites.GroupImpact{
		OwnGroups:            []my_sites.OwnGroupRate{{Name: "Codex 快速", Multiplier: 0.6}},
		Days:                 7,
		CostResolved:         false,
		CostUnresolvedReason: my_sites.ReasonUnbound,
	}
	msg := formatMultiplierChange("tkapi", "g", 0.055, 0.065, "", impact)
	if strings.Contains(msg, "¥0.00") {
		t.Fatalf("查不到成本时不能显示成 0 元：\n%s", msg)
	}
	if !strings.Contains(msg, "未绑成本账号") {
		t.Fatalf("应说明查不到成本的原因：\n%s", msg)
	}
}

// 完全没有影响面数据时（例如映射读取失败），预警本身仍然要发出去，
// 只是补充信息退化成占位说明——绝不能因为查不到附加信息就吞掉预警。
func TestFormatMultiplierChangeWithoutImpact(t *testing.T) {
	msg := formatMultiplierChange("tkapi", "g", 0.055, 0.065, "", my_sites.GroupImpact{})
	if !strings.Contains(msg, "0.0550x → 0.0650x") {
		t.Fatalf("核心变更信息必须保留：\n%s", msg)
	}
	if !strings.Contains(msg, "未找到对接的自有分组") {
		t.Fatalf("没有对接关系时应如实说明：\n%s", msg)
	}
}

// 自定义模板里没写新占位符时，行为必须和以前完全一样。
func TestFormatMultiplierChangeKeepsCustomTemplate(t *testing.T) {
	msg := formatMultiplierChange("tkapi", "g", 0.055, 0.065, "{siteName}/{groupName}/{oldRate}/{newRate}", impactWithCost(700))
	if msg != "tkapi/g/0.0550/0.0650" {
		t.Fatalf("自定义模板被改动了：%s", msg)
	}
}

func TestTrimFloat(t *testing.T) {
	cases := map[float64]string{0.055: "0.055", 1: "1", 0.6: "0.6", 1.5: "1.5"}
	for input, want := range cases {
		if got := trimFloat(input); got != want {
			t.Fatalf("trimFloat(%v) = %q，期望 %q", input, got, want)
		}
	}
}
