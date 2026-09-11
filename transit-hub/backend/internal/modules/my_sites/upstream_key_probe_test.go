package my_sites

import (
	"reflect"
	"testing"

	"transithub/backend/internal/modules/upstream"
)

// 测试会真实计费。挑模型时必须优先小模型——同一个分组里用 mini 验证链路
// 通不通，结论和用 o1-pro 完全一样，成本差一到两个数量级。
func TestPickProbeModel(t *testing.T) {
	cases := []struct {
		name   string
		models []string
		want   string
	}{
		{name: "优先 mini", models: []string{"o1-pro", "gpt-5.6", "gpt-4o-mini"}, want: "gpt-4o-mini"},
		{name: "没有 mini 用 flash", models: []string{"gemini-2.5-pro", "gemini-2.5-flash"}, want: "gemini-2.5-flash"},
		{name: "没有 haiku 之外的提示词就用 haiku", models: []string{"claude-opus-4", "claude-haiku-4-5"}, want: "claude-haiku-4-5"},
		// 一个候选都匹配不上时退回排序后的第一个，保证行为稳定可预期，
		// 不要因为上游返回顺序变化就每次测不同模型。
		{name: "无匹配退回字典序第一个", models: []string{"zeta", "alpha", "beta"}, want: "alpha"},
		{name: "空列表返回空串", models: nil, want: ""},
		{name: "忽略空白项", models: []string{"  ", "gpt-4o-mini"}, want: "gpt-4o-mini"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickProbeModel(tc.models); got != tc.want {
				t.Fatalf("pickProbeModel = %q，期望 %q", got, tc.want)
			}
		})
	}
}

func TestSelectCredential(t *testing.T) {
	keys := []upstream.Sub2APIKeyItem{
		{ID: "1", Key: "sk-one", GroupName: "0x"},
		{ID: "2", Key: "sk-two", GroupID: "g-2", GroupName: "特价"},
		// 没有明文 key 的条目不能被选中：拿它去测必然是一次无意义的 401。
		{ID: "3", Key: "", GroupName: "特价"},
	}

	t.Run("按 keyID 精确取", func(t *testing.T) {
		got, ok := selectCredential(keys, "2", "", "")
		if !ok || got.ID != "2" {
			t.Fatalf("期望取到 key 2，实际 ok=%v id=%q", ok, got.ID)
		}
	})

	t.Run("keyID 为空时按分组回退", func(t *testing.T) {
		got, ok := selectCredential(keys, "", "", "特价")
		if !ok || got.ID != "2" {
			t.Fatalf("期望回退到 key 2，实际 ok=%v id=%q", ok, got.ID)
		}
	})

	t.Run("跳过没有明文 key 的条目", func(t *testing.T) {
		onlyEmpty := []upstream.Sub2APIKeyItem{{ID: "3", Key: "", GroupName: "特价"}}
		if _, ok := selectCredential(onlyEmpty, "", "", "特价"); ok {
			t.Fatal("空 key 不应该被选中")
		}
	})

	t.Run("分组对不上就报找不到", func(t *testing.T) {
		if _, ok := selectCredential(keys, "", "", "不存在的分组"); ok {
			t.Fatal("分组不匹配时不应返回凭据")
		}
	})
}

func TestSampleModels(t *testing.T) {
	models := []string{"m9", "m8", "m7", "m6", "m5", "m4", "m3", "m2", "m1", "  "}
	got := sampleModels(models)
	want := []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sampleModels = %v，期望 %v", got, want)
	}
}
