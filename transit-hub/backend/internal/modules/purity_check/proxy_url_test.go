package purity_check

import "testing"

// 检测器只认 http:// 代理，账号那边绑的却是 xray 的 socks5 端口。
// 这个换算一旦出错，症状不是「报错」而是「查不出原因」：透传 socks5 会让检测器
// 抛 ValueError，再被它自己的脱敏逻辑盖成「本地运行发生未分类异常」，
// 前端只看得到一个 http 400。
func TestDetectorProxyURL(t *testing.T) {
	cases := []struct {
		name        string
		configured  string
		accountVal  string
		want        string
		expectError bool
	}{
		{name: "没绑代理就直连", configured: "http://bridge:10809", accountVal: "", want: ""},
		{name: "本来就是 http 就原样用", configured: "http://bridge:10809", accountVal: "http://1.2.3.4:8080", want: "http://1.2.3.4:8080"},
		{name: "https 也能直接用", configured: "", accountVal: "https://1.2.3.4:8443", want: "https://1.2.3.4:8443"},
		{name: "socks5 换成配好的 http 入口", configured: "http://172.18.0.1:10809", accountVal: "socks5://172.18.0.1:10808", want: "http://172.18.0.1:10809"},
		{name: "socks5 但没配替身就报错", configured: "", accountVal: "socks5://172.18.0.1:10808", expectError: true},
		// 没配替身时绝不能退化成直连：那等于拿本机出口 IP 去测一个只在代理后
		// 可用的上游，探针会全 401/403，结论完全是假的。
		{name: "socks4 同样不许静默直连", configured: "", accountVal: "socks4://1.2.3.4:1080", expectError: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := &Service{httpProxyURL: tc.configured}
			got, err := service.detectorProxyURL(tc.accountVal)
			if tc.expectError {
				if err == nil {
					t.Fatalf("期望报错，实际返回 %q", got)
				}
				if got != "" {
					t.Fatalf("报错时不应给出代理地址，实际 %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			if got != tc.want {
				t.Fatalf("代理地址 = %q，期望 %q", got, tc.want)
			}
		})
	}
}
