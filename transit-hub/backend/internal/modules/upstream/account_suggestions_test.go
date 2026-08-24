package upstream

import "testing"

func float64Ptr(v float64) *float64 { return &v }

func TestNormalizeUpstreamHost(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"带 www 前缀", "https://www.mcgrox.top", "mcgrox.top"},
		{"不带 www", "https://mcgrox.top", "mcgrox.top"},
		{"带 /v1 路径", "https://agentrouter.org/v1", "agentrouter.org"},
		{"带端口", "https://tntapi.com:8443/v1", "tntapi.com"},
		{"裸域名", "tntapi.com", "tntapi.com"},
		{"裸域名带路径", "tntapi.com/v1", "tntapi.com"},
		{"大小写混杂", "HTTPS://TntApi.COM", "tntapi.com"},
		{"末尾点", "https://tntapi.com./v1", "tntapi.com"},
		{"空串", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeUpstreamHost(tc.in); got != tc.want {
				t.Fatalf("normalizeUpstreamHost(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSuggestAccountsByBaseURL(t *testing.T) {
	accounts := []AdminGroupAccountInfo{
		{ID: "119", Name: "订阅-822-mcgrox", BaseURL: "https://www.mcgrox.top",
			CostRateMultiplier: float64Ptr(0.04), CostRateSource: "manual"},
		{ID: "145", Name: "A-tntapi-0.16x", BaseURL: "https://tntapi.com",
			CostRateMultiplier: float64Ptr(0.16), CostRateSource: "probe"},
		{ID: "135", Name: "A-tntapi-1x", BaseURL: "https://tntapi.com",
			CostRateMultiplier: nil, CostRateSource: "none"},
		{ID: "142", Name: "签到-agentrouter", BaseURL: "https://agentrouter.org/v1",
			CostRateMultiplier: float64Ptr(1), CostRateSource: "probe"},
	}

	t.Run("www 前缀差异不影响匹配", func(t *testing.T) {
		got := SuggestAccountsByBaseURL("https://mcgrox.top", accounts)
		if len(got) != 1 || got[0].ID != "119" {
			t.Fatalf("unexpected suggestions: %#v", got)
		}
		if got[0].CostRateSource != "manual" || got[0].CostRateMultiplier == nil ||
			*got[0].CostRateMultiplier != 0.04 {
			t.Fatalf("cost fields not carried over: %#v", got[0])
		}
	})

	t.Run("同域名多账号全部返回由人来选", func(t *testing.T) {
		got := SuggestAccountsByBaseURL("https://tntapi.com/v1", accounts)
		if len(got) != 2 {
			t.Fatalf("expected both tntapi accounts, got %#v", got)
		}
		if got[0].ID != "145" || got[1].ID != "135" {
			t.Fatalf("suggestion order should follow input: %#v", got)
		}
	})

	t.Run("无成本声明的候选也要列出", func(t *testing.T) {
		got := SuggestAccountsByBaseURL("tntapi.com", accounts)
		var found bool
		for _, item := range got {
			if item.ID == "135" {
				found = true
				if item.CostRateMultiplier != nil {
					t.Fatalf("account 135 should carry nil cost, got %#v", item)
				}
			}
		}
		if !found {
			t.Fatal("account without declared cost must still be selectable")
		}
	})

	t.Run("无匹配时返回空而不是全量", func(t *testing.T) {
		if got := SuggestAccountsByBaseURL("https://never-seen.example", accounts); got != nil {
			t.Fatalf("expected nil, got %#v", got)
		}
	})

	t.Run("站点地址不可解析时返回空", func(t *testing.T) {
		if got := SuggestAccountsByBaseURL("   ", accounts); got != nil {
			t.Fatalf("expected nil for blank base url, got %#v", got)
		}
	})
}
