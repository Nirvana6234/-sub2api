package purity_check

import "testing"

// TestSummarizeReport 覆盖三种真实会遇到的报告形状。
// 摘要解析必须对「字段缺失」「字段为 null」「多出未知字段」全部宽容——
// 检测器版本会迭代，解析一挂就等于整份报告的结论读不出来。
func TestSummarizeReport(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    Report
	}{
		{
			// 生产实录形状：上游全部请求失败时 fingerprint_model 是 null，
			// 直接用 string 接会解析失败，必须用指针。
			name: "指纹不明确时 fingerprint_model 为 null",
			payload: `{"overall_verdict":"Juice证据不足；指纹证据不明确",
				"outcome_code":"juice_insufficient_fingerprint_unclear",
				"juice_verdict_state":"insufficient","fingerprint_model":null,
				"fingerprint_verdict_state":"unclear","fingerprint_claim_mismatch":false,
				"official":true,"preset":"low"}`,
			want: Report{
				OverallVerdict:          "Juice证据不足；指纹证据不明确",
				OutcomeCode:             "juice_insufficient_fingerprint_unclear",
				JuiceVerdictState:       "insufficient",
				FingerprintModel:        "",
				FingerprintVerdictState: "unclear",
				Official:                true,
			},
		},
		{
			// 我们要在列表页标红的那种：指纹强指向的型号和申报的不是一个。
			name: "指纹与申报不一致",
			payload: `{"overall_verdict":"与申报不一致","outcome_code":"fingerprint_mismatch",
				"juice_verdict_state":"mismatch","fingerprint_model":"gpt-5.6-luna",
				"fingerprint_verdict_state":"strong","fingerprint_claim_mismatch":true,
				"official":true}`,
			want: Report{
				OverallVerdict:           "与申报不一致",
				OutcomeCode:              "fingerprint_mismatch",
				JuiceVerdictState:        "mismatch",
				FingerprintModel:         "gpt-5.6-luna",
				FingerprintVerdictState:  "strong",
				FingerprintClaimMismatch: true,
				Official:                 true,
			},
		},
		{
			// 检测器以后加字段不能让解析失败：只认识的字段取出来，其余忽略。
			name: "多出未知字段仍能解析",
			payload: `{"overall_verdict":"通过","official":false,
				"brand_new_field_from_v5":{"nested":[1,2,3]},"another":"x"}`,
			want: Report{OverallVerdict: "通过", Official: false},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := summarizeReport([]byte(tc.payload))
			if !sameSummary(got, tc.want) {
				t.Errorf("摘要不符\n得到: %+v\n期望: %+v", got, tc.want)
			}
		})
	}
}

// TestSummarizeReportFailureHint 覆盖上游错误提示的拼装。
//
// 重点是不要重复写状态码：检测器的 safe_message 通常已经带了「（HTTP 503）」，
// 无条件再追加会得到「上游返回HTTP错误（HTTP 503）（HTTP 503）」——生产上真出现过。
func TestSummarizeReportFailureHint(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name: "safe_message 已含状态码时不重复追加",
			payload: `{"network_error_details":[{"category_cn":"上游HTTP错误",
				"safe_message":"上游返回HTTP错误（HTTP 503）","http_status":503}]}`,
			want: "上游返回HTTP错误（HTTP 503）",
		},
		{
			name: "safe_message 未含状态码时补上",
			payload: `{"network_error_details":[{"category_cn":"上游HTTP错误",
				"safe_message":"上游拒绝了请求","http_status":429}]}`,
			want: "上游拒绝了请求（HTTP 429）",
		},
		{
			name: "没有 safe_message 时退回分类名",
			payload: `{"network_error_details":[{"category_cn":"网络连接或传输错误",
				"safe_message":"","http_status":null}]}`,
			want: "网络连接或传输错误",
		},
		{
			// 连接类错误没有 http_status，不该凭空造一个。
			name: "连接失败无状态码",
			payload: `{"network_error_details":[{"category_cn":"网络连接或传输错误",
				"safe_message":"网络连接或传输失败","http_status":null}]}`,
			want: "网络连接或传输失败",
		},
		{
			name:    "没有错误明细时为空",
			payload: `{"network_summary":{"successful":19,"logical_tasks":19}}`,
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summarizeReport([]byte(tc.payload)).FailureHint; got != tc.want {
				t.Errorf("提示不符\n得到: %q\n期望: %q", got, tc.want)
			}
		})
	}
}

// TestSummarizeReportInvalidJSON 确认报告不是合法 JSON 时不 panic，
// 只是摘要为空——原文照旧存下来，前端还能展开看。
func TestSummarizeReportInvalidJSON(t *testing.T) {
	if got := summarizeReport([]byte(`{"overall_verdict":`)); !sameSummary(got, Report{}) {
		t.Errorf("非法 JSON 应返回空摘要，实际 %+v", got)
	}
}

// sameSummary 比较摘要字段。Report 里有 []byte 的 Payload 所以不能直接用 ==，
// 而 summarizeReport 本来也不填 Payload。
func sameSummary(a Report, b Report) bool {
	return a.OverallVerdict == b.OverallVerdict &&
		a.OutcomeCode == b.OutcomeCode &&
		a.JuiceVerdictState == b.JuiceVerdictState &&
		a.FingerprintModel == b.FingerprintModel &&
		a.FingerprintVerdictState == b.FingerprintVerdictState &&
		a.FingerprintClaimMismatch == b.FingerprintClaimMismatch &&
		a.Official == b.Official
}
