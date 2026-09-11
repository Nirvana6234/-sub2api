package my_sites

import (
	"testing"

	"transithub/backend/internal/modules/upstream"
)

func costPtr(v float64) *float64 { return &v }
func strPtr(v string) *string    { return &v }

func testAccounts() []upstream.AdminGroupAccountInfo {
	return []upstream.AdminGroupAccountInfo{
		{ID: "119", Name: "订阅-822-mcgrox", CostRateMultiplier: costPtr(0.04), CostRateSource: "manual"},
		{ID: "145", Name: "A-tntapi-0.16x", CostRateMultiplier: costPtr(0.16), CostRateSource: "probe"},
		{ID: "134", Name: "B-tntapi-0.65x", CostRateMultiplier: costPtr(0.65), CostRateSource: "column"},
		{ID: "137", Name: "签到-muyuan", CostRateMultiplier: nil, CostRateSource: "none"},
		{ID: "999", Name: "来源不明", CostRateMultiplier: costPtr(0.5), CostRateSource: "guessed"},
	}
}

func TestResolveTargetCostMultiplier(t *testing.T) {
	accounts := testAccounts()

	t.Run("绑定了有手工倍率的账号", func(t *testing.T) {
		// 生产 mcgrox.top：上游标称 0.8，手工成本 0.04。必须拿到 0.04。
		rate, source := resolveTargetCostMultiplier(
			UpstreamGroupRef{SiteID: "s1", GroupName: "0.8倍率订阅池", Sub2APIAccountID: strPtr("119")},
			accounts,
		)
		if rate == nil || *rate != 0.04 {
			t.Fatalf("expected manual rate 0.04, got %v", rate)
		}
		if source != CostSourceManual {
			t.Fatalf("expected source manual, got %q", source)
		}
	})

	t.Run("探测来源与列值来源都采信", func(t *testing.T) {
		rate, source := resolveTargetCostMultiplier(
			UpstreamGroupRef{SiteID: "s2", GroupName: "GPT特价", Sub2APIAccountID: strPtr("145")}, accounts)
		if rate == nil || *rate != 0.16 || source != CostSourceProbe {
			t.Fatalf("probe source not honored: rate=%v source=%q", rate, source)
		}
		rate, source = resolveTargetCostMultiplier(
			UpstreamGroupRef{SiteID: "s2", GroupName: "B组", Sub2APIAccountID: strPtr("134")}, accounts)
		if rate == nil || *rate != 0.65 || source != CostSourceColumn {
			t.Fatalf("column source not honored: rate=%v source=%q", rate, source)
		}
	})

	t.Run("未绑定账号视为无数据", func(t *testing.T) {
		rate, source := resolveTargetCostMultiplier(
			UpstreamGroupRef{SiteID: "s1", GroupName: "0.8倍率订阅池"}, accounts)
		if rate != nil || source != CostSourceNone {
			t.Fatalf("unbound target must be unknown, got rate=%v source=%q", rate, source)
		}
	})

	t.Run("空字符串绑定视为未绑定", func(t *testing.T) {
		rate, source := resolveTargetCostMultiplier(
			UpstreamGroupRef{SiteID: "s1", GroupName: "g", Sub2APIAccountID: strPtr("   ")}, accounts)
		if rate != nil || source != CostSourceNone {
			t.Fatalf("blank binding must be unknown, got rate=%v source=%q", rate, source)
		}
	})

	t.Run("账号无成本声明时不回退1.0", func(t *testing.T) {
		// 生产账号 137：探测连续失败，列上只剩建表默认值。
		rate, source := resolveTargetCostMultiplier(
			UpstreamGroupRef{SiteID: "s3", GroupName: "g", Sub2APIAccountID: strPtr("137")}, accounts)
		if rate != nil {
			t.Fatalf("undeclared cost must stay unknown, got %v", *rate)
		}
		if source != CostSourceNone {
			t.Fatalf("expected none, got %q", source)
		}
	})

	t.Run("绑定的账号已不存在", func(t *testing.T) {
		rate, source := resolveTargetCostMultiplier(
			UpstreamGroupRef{SiteID: "s4", GroupName: "g", Sub2APIAccountID: strPtr("404")}, accounts)
		if rate != nil || source != CostSourceNone {
			t.Fatalf("missing account must be unknown, got rate=%v source=%q", rate, source)
		}
	})

	t.Run("来源不在白名单时不采信数字", func(t *testing.T) {
		rate, source := resolveTargetCostMultiplier(
			UpstreamGroupRef{SiteID: "s5", GroupName: "g", Sub2APIAccountID: strPtr("999")}, accounts)
		if rate != nil {
			t.Fatalf("unknown source must not be trusted, got %v", *rate)
		}
		if source != CostSourceNone {
			t.Fatalf("expected none, got %q", source)
		}
	})
}

func TestNormalizeSub2APIAccountID(t *testing.T) {
	if got := normalizeSub2APIAccountID(nil); got != nil {
		t.Fatalf("nil must stay nil, got %v", got)
	}
	if got := normalizeSub2APIAccountID(strPtr("  ")); got != nil {
		t.Fatalf("blank must collapse to nil, got %q", *got)
	}
	got := normalizeSub2APIAccountID(strPtr("  142 "))
	if got == nil || *got != "142" {
		t.Fatalf("expected trimmed 142, got %v", got)
	}
}
