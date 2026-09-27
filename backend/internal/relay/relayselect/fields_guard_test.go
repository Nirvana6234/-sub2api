package relayselect

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// 开发计划 2.2 的字段守卫：入账要用的每一个值都要说清楚在主从分流下从哪来。
//
//   - voucher:<消息>.<字段>：由选号定下，写进扣费凭证（签名，节点改不了），入账时恢复。
//   - node：只有转发的从节点知道的事实，进扣费队列记录（WP8）。
//   - node-price：同上，但会影响价格，WP8 入账时必须核对或限定，不能照单全收。
//   - recompute：入账时由凭证里的值和节点上报的事实重新算。
//   - master：主节点自己的依赖，不传。
//   - quote：计价输入，进报价（WP8 与"按报价计价"一起补）。
//   - not-billing：与入账无关。
//
// 入账输入结构体、账号的请求级字段、入账 ctx 搬运的值，新增时这里的测试会失败，提醒决定它归哪一类。
var usageInputFields = map[string]string{
	// OpenAIRecordUsageInput / RecordUsageInput / CyberPolicyUsageInput 共有的。
	"Result":             "node",
	"APIKey":             "voucher:Voucher.api_key_id",
	"User":               "voucher:Voucher.user_id",
	"Account":            "voucher:Voucher.account_id",
	"Subscription":       "voucher:SelectionContext.subscription_id",
	"InboundEndpoint":    "node",
	"UpstreamEndpoint":   "node",
	"UserAgent":          "node",
	"IPAddress":          "node",
	"SessionID":          "node",
	"RequestPayloadHash": "node",
	"APIKeyService":      "master",
	"QuotaPlatform":      "voucher:SelectionContext.quota_platform",
	"PricingAt":          "voucher:SelectionContext.pricing_at_unix_ms",
	"CyberBlocked":       "node",
	"NativeCompactionV2": "node",
	// RecordUsageInput：粘性会话切换时把 input 按缓存读计价，会压低价格。
	"ForceCacheBilling": "node-price",
	// CyberPolicyUsageInput：上游 response.failed 报告的用量。
	"RequestID":    "node",
	"Model":        "node",
	"Stream":       "node",
	"InputTokens":  "node",
	"OutputTokens": "node",
	// ChannelUsageFields（内嵌）。
	"ChannelID":          "voucher:SelectionContext.channel_id",
	"OriginalModel":      "voucher:Voucher.requested_model",
	"ChannelMappedModel": "voucher:SelectionContext.channel_mapped_model",
	"BillingModelSource": "voucher:SelectionContext.billing_model_source",
	"ModelMappingChain":  "recompute",
}

// 账号上只对这一次请求有效的字段（不持久化，json:"-"）：入账从库里重新取账号时会丢，影响计费的要进凭证。
var accountRequestScopedFields = map[string]string{
	"ContributionRouteSource":            "voucher:SelectionContext.contribution_route_source",
	"ContributionRoomID":                 "voucher:SelectionContext.contribution_room_id",
	"ContributionRateMultiplierOverride": "voucher:SelectionContext.contribution_rate_multiplier_override",
	"ContributionConcurrencyOverride":    "not-billing",
}

// 入账跑在 detached worker 上，能从请求 ctx 读到的只有 handler 的 usageRecordContext 搬过去的值
// （debb1d42 的教训：没搬的值入账时本来就读不到）。
var usageContextValues = map[string]string{
	"ctxkey.ClientRequestID":            "node",
	"ctxkey.RequestID":                  "node",
	"PropagateFallbackPoolUsageContext": "voucher:SelectionContext.fallback_target_group_id",
}

var guardCategories = map[string]bool{"node": true, "node-price": true, "recompute": true, "master": true, "quote": true, "not-billing": true}

func protoMessages() map[string]protoreflect.MessageDescriptor {
	return map[string]protoreflect.MessageDescriptor{
		"Voucher":          (&relayv1.Voucher{}).ProtoReflect().Descriptor(),
		"SelectionContext": (&relayv1.SelectionContext{}).ProtoReflect().Descriptor(),
		"Quote":            (&relayv1.Quote{}).ProtoReflect().Descriptor(),
	}
}

// requireClassified 检查一个归类：类别已知；进凭证的，proto 里确实有这个字段（改名、删掉会失败）。
func requireClassified(t *testing.T, what, class string) {
	t.Helper()
	if target, ok := strings.CutPrefix(class, "voucher:"); ok {
		msg, field, found := strings.Cut(target, ".")
		require.True(t, found, "%s: %q", what, class)
		desc := protoMessages()[msg]
		require.NotNil(t, desc, "%s: unknown message %s", what, msg)
		require.NotNil(t, desc.Fields().ByName(protoreflect.Name(field)), "%s: %s has no field %s", what, msg, field)
		return
	}
	require.True(t, guardCategories[class], "%s: unknown category %q", what, class)
}

func structFields(t reflect.Type) []reflect.StructField {
	var out []reflect.StructField
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			out = append(out, structFields(f.Type)...)
			continue
		}
		out = append(out, f)
	}
	return out
}

func TestUsageInputFieldsAreClassified(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(service.OpenAIRecordUsageInput{}),
		reflect.TypeOf(service.RecordUsageInput{}),
		reflect.TypeOf(service.CyberPolicyUsageInput{}),
	} {
		for _, f := range structFields(typ) {
			class, ok := usageInputFields[f.Name]
			require.True(t, ok, "%s.%s is new: decide where it comes from on relay nodes (voucher / node / ...) and add it to usageInputFields", typ.Name(), f.Name)
			requireClassified(t, typ.Name()+"."+f.Name, class)
		}
	}
}

func TestAccountRequestScopedFieldsAreClassified(t *testing.T) {
	typ := reflect.TypeOf(service.Account{})
	seen := map[string]bool{}
	for _, f := range structFields(typ) {
		if f.Tag.Get("json") != "-" {
			continue
		}
		class, ok := accountRequestScopedFields[f.Name]
		require.True(t, ok, "Account.%s is request scoped: decide whether billing needs it and add it to accountRequestScopedFields", f.Name)
		requireClassified(t, "Account."+f.Name, class)
		seen[f.Name] = true
	}
	for name := range accountRequestScopedFields {
		require.True(t, seen[name], "Account.%s is no longer request scoped; update accountRequestScopedFields", name)
	}
}

// usageRecordContext 搬运的值必须恰好是 usageContextValues 列出的这些。
func TestUsageRecordContextCarriesOnlyClassifiedValues(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "../../handler/openai_gateway_handler.go", nil, 0)
	require.NoError(t, err)
	var fn *ast.FuncDecl
	for _, d := range file.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == "usageRecordContext" {
			fn = f
		}
	}
	require.NotNil(t, fn, "handler.usageRecordContext moved; point this guard at the new place")

	found := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch {
		case sel.Sel.Name == "Value" && len(call.Args) == 1:
			if key, ok := call.Args[0].(*ast.SelectorExpr); ok {
				if pkg, ok := key.X.(*ast.Ident); ok {
					found[pkg.Name+"."+key.Sel.Name] = true
				}
			}
		case sel.Sel.Name == "WithValue" && len(call.Args) == 3:
			// 只允许把读到的值原样放回同一个 key。
			if key, ok := call.Args[1].(*ast.SelectorExpr); ok {
				if pkg, ok := key.X.(*ast.Ident); ok {
					found[pkg.Name+"."+key.Sel.Name] = true
				}
			}
		case strings.HasPrefix(sel.Sel.Name, "Propagate"):
			found[sel.Sel.Name] = true
		}
		return true
	})
	for name := range found {
		class, ok := usageContextValues[name]
		require.True(t, ok, "usageRecordContext now carries %s into billing: classify it in usageContextValues", name)
		requireClassified(t, name, class)
	}
	for name := range usageContextValues {
		require.True(t, found[name], "usageRecordContext no longer carries %s; update usageContextValues", name)
	}
}
