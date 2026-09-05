package service

import "testing"

// 会话绑定的核心取舍：同网段内换 IP 不该踢人，跨网段才踢。
//
// 之前用精确 IP 匹配，家宽动态 IP 续租、运营商 NAT 出口漂移、Wi-Fi 与移动网络
// 切换都会让哈希变化，而校验失败会撤销整个 token family，用户所有设备一起登出，
// 表现为「挂着页面过一阵子就提示登录失效」。
func TestSessionBindingHash_IgnoresAddressChangeWithinSameSubnet(t *testing.T) {
	const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64)"

	cases := []struct {
		name  string
		a     SessionBinding
		b     SessionBinding
		equal bool
	}{
		{
			name:  "IPv4 同 /24 内换地址视为同一会话",
			a:     SessionBinding{IP: "203.0.113.7", UserAgent: ua},
			b:     SessionBinding{IP: "203.0.113.200", UserAgent: ua},
			equal: true,
		},
		{
			name:  "IPv4 跨 /24 视为不同会话",
			a:     SessionBinding{IP: "203.0.113.7", UserAgent: ua},
			b:     SessionBinding{IP: "203.0.114.7", UserAgent: ua},
			equal: false,
		},
		{
			name:  "IPv6 同 /48 内换地址视为同一会话",
			a:     SessionBinding{IP: "2001:db8:1::1", UserAgent: ua},
			b:     SessionBinding{IP: "2001:db8:1:ffff::abcd", UserAgent: ua},
			equal: true,
		},
		{
			name:  "IPv6 跨 /48 视为不同会话",
			a:     SessionBinding{IP: "2001:db8:1::1", UserAgent: ua},
			b:     SessionBinding{IP: "2001:db8:2::1", UserAgent: ua},
			equal: false,
		},
		{
			// UA 仍然精确比对：换浏览器/换设备是真正值得警惕的信号。
			name:  "同 IP 换 UA 视为不同会话",
			a:     SessionBinding{IP: "203.0.113.7", UserAgent: ua},
			b:     SessionBinding{IP: "203.0.113.7", UserAgent: "curl/8.4.0"},
			equal: false,
		},
		{
			// RemoteAddr 直传时会带端口，剥掉端口后应当与不带端口的等价。
			name:  "带端口与不带端口等价",
			a:     SessionBinding{IP: "203.0.113.7:51515", UserAgent: ua},
			b:     SessionBinding{IP: "203.0.113.7", UserAgent: ua},
			equal: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.a.Hash() == tc.b.Hash()
			if got != tc.equal {
				t.Fatalf("Hash 相等性 = %v, 期望 %v\n  a=%q -> %s\n  b=%q -> %s",
					got, tc.equal, tc.a.IP, tc.a.Hash(), tc.b.IP, tc.b.Hash())
			}
		})
	}
}

// LegacyHash 必须保持「精确 IP」语义，它是识别旧会话的唯一依据；
// 一旦它也变成网段粒度，改算法前签发的会话就无法被认出，全体用户会被登出一次。
func TestSessionBindingLegacyHash_StaysExactPerAddress(t *testing.T) {
	const ua = "Mozilla/5.0"
	a := SessionBinding{IP: "203.0.113.7", UserAgent: ua}
	b := SessionBinding{IP: "203.0.113.200", UserAgent: ua}

	if a.LegacyHash() == b.LegacyHash() {
		t.Fatal("LegacyHash 对同 /24 内的不同地址给出了相同结果，无法再识别旧会话")
	}
	if a.Hash() == a.LegacyHash() {
		t.Fatal("新旧算法结果相同，说明网段归一没有生效")
	}
}

func TestSessionBindingHash_EmptyBindingYieldsEmptyHash(t *testing.T) {
	var nilBinding *SessionBinding
	if got := nilBinding.Hash(); got != "" {
		t.Fatalf("nil 绑定应得到空哈希，实际 %q", got)
	}
	// 空哈希在校验侧意味着「跳过绑定检查」，因此 IP 与 UA 都缺失时必须是空串，
	// 不能退化成一个所有人相同的固定哈希。
	empty := SessionBinding{}
	if got := empty.Hash(); got != "" {
		t.Fatalf("空绑定应得到空哈希，实际 %q", got)
	}
}

func TestNormalizeIPForBinding_UnparsableInputStaysStrict(t *testing.T) {
	// 解析不出来时保持原样，避免因为格式意外把绑定校验放宽成一句空话。
	if got := normalizeIPForBinding("not-an-ip"); got != "not-an-ip" {
		t.Fatalf("无法解析的输入应原样返回，实际 %q", got)
	}
	if got := normalizeIPForBinding("  "); got != "" {
		t.Fatalf("空白输入应返回空串，实际 %q", got)
	}
}
