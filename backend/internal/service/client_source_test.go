package service

import (
	"context"
	"testing"
)

func TestClientSourceDefaultsToWeb(t *testing.T) {
	// 缺失、空白、大小写、不认识的值——全部按网页端处理。认不出来就走最严格的
	// 策略，忘了登记一种新来源的后果是多校验一道，不是悄悄少校验一道。
	for _, tc := range []struct {
		name, in, want string
	}{
		{"missing", "", ClientSourceWeb},
		{"blank", "   ", ClientSourceWeb},
		{"web", "web", ClientSourceWeb},
		{"unknown", "mobile", ClientSourceWeb},
		{"desktop", "desktop", ClientSourceDesktop},
		{"desktop mixed case", " Desktop ", ClientSourceDesktop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeClientSource(tc.in); got != tc.want {
				t.Fatalf("NormalizeClientSource(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestClientSourceFromContextWithoutInjectionIsWeb(t *testing.T) {
	// 没走过登录入口的 context（OAuth 回调、passkey、后台任务）必须拿到网页端，
	// 否则等于给没声明来源的路径静默开了豁免。
	if got := ClientSourceFromContext(context.Background()); got != ClientSourceWeb {
		t.Fatalf("ClientSourceFromContext = %q, want %q", got, ClientSourceWeb)
	}
	//nolint:staticcheck // 显式覆盖 nil context，调用方给什么都不该 panic。
	if got := ClientSourceFromContext(nil); got != ClientSourceWeb {
		t.Fatalf("ClientSourceFromContext(nil) = %q, want %q", got, ClientSourceWeb)
	}
}

func TestOnlyDesktopSkipsSessionBinding(t *testing.T) {
	if clientSourceSkipsSessionBinding(ClientSourceWeb) {
		t.Fatal("网页端不应跳过会话绑定")
	}
	if clientSourceSkipsSessionBinding("mobile") {
		t.Fatal("不认识的来源不应跳过会话绑定")
	}
	if !clientSourceSkipsSessionBinding(ClientSourceDesktop) {
		t.Fatal("桌面客户端应跳过会话绑定")
	}
}

func TestAClientWithoutAUserAgentIsExemptWithoutDeclaringASource(t *testing.T) {
	// 兼容已经发出去的 0.5 客户端：它们不发 source，也不发 User-Agent。没有 UA 的
	// 指纹只剩 IP，本来就是纯 IP 锁——这条让老客户端不用升级，在下一次轮转时自动
	// 转成豁免会话。浏览器必然带 UA，网页端不受影响。
	noUA := &SessionBinding{IP: "203.0.113.7"}
	if !skipSessionBinding(ClientSourceWeb, noUA) {
		t.Fatal("没有 User-Agent 的请求应豁免会话绑定（老客户端兼容）")
	}

	browser := &SessionBinding{IP: "203.0.113.7", UserAgent: "Mozilla/5.0"}
	if skipSessionBinding(ClientSourceWeb, browser) {
		t.Fatal("带 User-Agent 的网页端不应豁免")
	}

	// 老客户端在新版服务端上的完整效果：签发时不写指纹。
	svc := &AuthService{}
	if got := svc.sessionBindingHashFor(WithSessionBinding(context.Background(), noUA)); got != "" {
		t.Fatalf("老客户端会话不应写入指纹，得到 %q", got)
	}
}

func TestDesktopSessionCarriesNoBindingFingerprint(t *testing.T) {
	// 豁免是靠「不写指纹」实现的：两处校验点都在指纹为空时放行，所以这一条
	// 就是整个豁免的支点。写进去了，豁免就没了。
	binding := &SessionBinding{IP: "203.0.113.7", UserAgent: "Mozilla/5.0"}
	svc := &AuthService{}

	web := svc.sessionBindingHashFor(WithClientSource(WithSessionBinding(context.Background(), binding), ClientSourceWeb))
	if web == "" {
		t.Fatal("网页端必须写入指纹，否则等于全局关掉了会话绑定")
	}

	desktop := svc.sessionBindingHashFor(WithClientSource(WithSessionBinding(context.Background(), binding), ClientSourceDesktop))
	if desktop != "" {
		t.Fatalf("桌面客户端不应写入指纹，得到 %q", desktop)
	}
}
