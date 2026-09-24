package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func trialConfigForTest() *GuestTrialConfig {
	cfg := DefaultGuestTrialConfig()
	cfg.Enabled = true
	cfg.APIKeyID = 42
	cfg.Models = []string{"gpt-5.4-mini", "claude-haiku-4-5"}
	cfg.MaxInputChars = 20
	cfg.MaxOutputTokens = 256
	return cfg
}

func decodeTrialBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("invalid rebuilt body: %v", err)
	}
	return out
}

func TestSanitizeGuestTrialChatBodyRebuildsTextOnlyRequest(t *testing.T) {
	body := []byte(`{"model":"GPT-5.4-MINI","messages":[{"role":"user","content":[{"type":"text","text":"你好"}]}],"stream":true,"temperature":0.3,"metadata":{"x":1},"max_tokens":99999}`)
	out, model, err := SanitizeGuestTrialChatBody(body, trialConfigForTest())
	if err != nil {
		t.Fatal(err)
	}
	if model != "gpt-5.4-mini" {
		t.Fatalf("model should take the allowlist spelling, got %q", model)
	}
	got := decodeTrialBody(t, out)
	if got["max_tokens"].(float64) != 256 || got["max_completion_tokens"].(float64) != 256 {
		t.Fatalf("output cap not forced: %v", got)
	}
	if _, ok := got["metadata"]; ok {
		t.Fatal("unknown fields must be dropped")
	}
	if got["temperature"].(float64) != 0.3 {
		t.Fatal("sampling params should pass through")
	}
	if got["stream_options"] == nil {
		t.Fatal("streaming requests must ask upstream for usage")
	}
	messages := got["messages"].([]any)
	if messages[0].(map[string]any)["content"] != "你好" {
		t.Fatalf("content should be flattened to plain text: %v", messages)
	}
}

func TestSanitizeGuestTrialChatBodyDefaultsToFirstModel(t *testing.T) {
	_, model, err := SanitizeGuestTrialChatBody([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), trialConfigForTest())
	if err != nil || model != "gpt-5.4-mini" {
		t.Fatalf("model=%q err=%v", model, err)
	}
}

func TestSanitizeGuestTrialChatBodyRejections(t *testing.T) {
	cases := map[string]struct {
		body string
		want error
	}{
		"image part":       {`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]}]}`, ErrGuestTrialTextOnly},
		"file part":        {`{"messages":[{"role":"user","content":[{"type":"file","file":{}}]}]}`, ErrGuestTrialTextOnly},
		"tools":            {`{"messages":[{"role":"user","content":"hi"}],"tools":[]}`, ErrGuestTrialTextOnly},
		"audio modality":   {`{"messages":[{"role":"user","content":"hi"}],"modalities":["audio"]}`, ErrGuestTrialTextOnly},
		"tool role":        {`{"messages":[{"role":"tool","content":"hi"}]}`, ErrGuestTrialTextOnly},
		"model not listed": {`{"model":"gpt-image-2","messages":[{"role":"user","content":"hi"}]}`, ErrGuestTrialModelNotAllowed},
		"too long":         {`{"messages":[{"role":"user","content":"` + strings.Repeat("字", 21) + `"}]}`, ErrGuestTrialInputTooLong},
		"no messages":      {`{"messages":[]}`, ErrGuestTrialInvalidRequest},
		"not json":         {`hello`, ErrGuestTrialInvalidRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := SanitizeGuestTrialChatBody([]byte(tc.body), trialConfigForTest())
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNormalizeGuestTrialConfig(t *testing.T) {
	cfg := normalizeGuestTrialConfig(&GuestTrialConfig{
		Enabled:         true,
		Models:          []string{" gpt-5.4-mini ", "GPT-5.4-MINI", "", "claude"},
		DailyPerVisitor: -1,
		DailyGlobal:     99999999,
	})
	if len(cfg.Models) != 2 || cfg.Models[0] != "gpt-5.4-mini" {
		t.Fatalf("models not deduplicated/trimmed: %v", cfg.Models)
	}
	if cfg.DailyPerVisitor != GuestTrialDefaultDailyPerVisitor || cfg.DailyGlobal != guestTrialMaxDailyGlobal {
		t.Fatalf("limits not clamped: %+v", cfg)
	}
	if err := validateGuestTrialConfig(cfg); err == nil {
		t.Fatal("enabled config without an API key must be rejected")
	}
}

// --- service flow ---

type fakeTrialQuota struct {
	reason    GuestTrialQuotaDenyReason
	used      int
	consumed  int
	verified  map[string]bool
	failStore bool
}

func (q *fakeTrialQuota) Consume(context.Context, GuestTrialQuotaKeys, GuestTrialQuotaLimits) (GuestTrialQuotaDenyReason, int, error) {
	if q.failStore {
		return GuestTrialQuotaGlobalFull, 0, errors.New("redis down")
	}
	q.consumed++
	return q.reason, q.used + 1, nil
}

func (q *fakeTrialQuota) VisitorUsed(context.Context, GuestTrialQuotaKeys) (int, error) {
	return q.used, nil
}

func (q *fakeTrialQuota) MarkVerified(_ context.Context, subject string, _ time.Duration) error {
	q.verified[subject] = true
	return nil
}

func (q *fakeTrialQuota) IsVerified(_ context.Context, subject string) (bool, error) {
	if q.failStore {
		return false, errors.New("redis down")
	}
	return q.verified[subject], nil
}

type fakeTrialCaptcha struct{ err error }

func (f fakeTrialCaptcha) VerifyCaptcha(context.Context, CaptchaProof, string) error { return f.err }

type fakeCaptchaStatus struct{ enabled bool }

func (f fakeCaptchaStatus) GetCaptchaProviderConfig(context.Context) (CaptchaProviderConfig, error) {
	return CaptchaProviderConfig{Tencent: TencentCaptchaConfig{Enabled: f.enabled}}, nil
}

const testTrialDevice = "abcdefghijklmnop1234"

func newTrialServiceForTest(cfg *GuestTrialConfig, quota *fakeTrialQuota, captchaEnabled bool) *GuestTrialService {
	if quota.verified == nil {
		quota.verified = map[string]bool{}
	}
	return &GuestTrialService{
		quota:   quota,
		captcha: fakeTrialCaptcha{},
		status:  fakeCaptchaStatus{enabled: captchaEnabled},
		now:     time.Now,
		loadCfg: func(context.Context) (*GuestTrialConfig, error) { return cfg, nil },
	}
}

var trialChatBody = []byte(`{"messages":[{"role":"user","content":"hi"}]}`)

func TestGuestTrialPrepareChatHappyPath(t *testing.T) {
	quota := &fakeTrialQuota{used: 4}
	svc := newTrialServiceForTest(trialConfigForTest(), quota, false)
	prepared, err := svc.PrepareChat(context.Background(), testTrialDevice, "1.1.1.1", trialChatBody)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.APIKeyID != 42 || prepared.Remaining != GuestTrialDefaultDailyPerVisitor-5 || quota.consumed != 1 {
		t.Fatalf("unexpected result: %+v consumed=%d", prepared, quota.consumed)
	}
}

func TestGuestTrialPrepareChatGates(t *testing.T) {
	disabled := trialConfigForTest()
	disabled.Enabled = false
	if _, err := newTrialServiceForTest(disabled, &fakeTrialQuota{}, false).PrepareChat(context.Background(), testTrialDevice, "ip", trialChatBody); !errors.Is(err, ErrGuestTrialDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	if _, err := newTrialServiceForTest(trialConfigForTest(), &fakeTrialQuota{}, false).PrepareChat(context.Background(), "short", "ip", trialChatBody); !errors.Is(err, ErrGuestTrialInvalidDevice) {
		t.Fatalf("device: %v", err)
	}
	if _, err := newTrialServiceForTest(trialConfigForTest(), &fakeTrialQuota{reason: GuestTrialQuotaVisitorFull}, false).PrepareChat(context.Background(), testTrialDevice, "ip", trialChatBody); !errors.Is(err, ErrGuestTrialVisitorLimit) {
		t.Fatalf("visitor quota: %v", err)
	}
	if _, err := newTrialServiceForTest(trialConfigForTest(), &fakeTrialQuota{reason: GuestTrialQuotaGlobalFull}, false).PrepareChat(context.Background(), testTrialDevice, "ip", trialChatBody); !errors.Is(err, ErrGuestTrialGlobalLimit) {
		t.Fatalf("global quota: %v", err)
	}
	// 存储故障时关闭试用（成本闸门不能 fail-open）
	if _, err := newTrialServiceForTest(trialConfigForTest(), &fakeTrialQuota{failStore: true}, false).PrepareChat(context.Background(), testTrialDevice, "ip", trialChatBody); !errors.Is(err, ErrGuestTrialUnavailable) {
		t.Fatalf("store failure must fail closed: %v", err)
	}
}

func TestGuestTrialInvalidBodyDoesNotConsumeQuota(t *testing.T) {
	quota := &fakeTrialQuota{}
	svc := newTrialServiceForTest(trialConfigForTest(), quota, false)
	_, err := svc.PrepareChat(context.Background(), testTrialDevice, "ip", []byte(`{"messages":[{"role":"user","content":[{"type":"image_url"}]}]}`))
	if !errors.Is(err, ErrGuestTrialTextOnly) || quota.consumed != 0 {
		t.Fatalf("err=%v consumed=%d", err, quota.consumed)
	}
}

func TestGuestTrialCaptchaFlow(t *testing.T) {
	quota := &fakeTrialQuota{}
	svc := newTrialServiceForTest(trialConfigForTest(), quota, true)
	ctx := context.Background()

	state, _ := svc.State(ctx, testTrialDevice, "1.1.1.1")
	if !state.CaptchaRequired {
		t.Fatal("captcha should be required before verification")
	}
	if _, err := svc.PrepareChat(ctx, testTrialDevice, "1.1.1.1", trialChatBody); !errors.Is(err, ErrGuestTrialCaptchaRequired) {
		t.Fatalf("chat before captcha: %v", err)
	}
	if err := svc.Verify(ctx, testTrialDevice, "1.1.1.1", CaptchaProof{TencentTicket: "t", TencentRandstr: "r"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PrepareChat(ctx, testTrialDevice, "1.1.1.1", trialChatBody); err != nil {
		t.Fatalf("chat after captcha: %v", err)
	}
	// 验证结果绑定设备 + IP：换 IP 需要重新验证
	if _, err := svc.PrepareChat(ctx, testTrialDevice, "2.2.2.2", trialChatBody); !errors.Is(err, ErrGuestTrialCaptchaRequired) {
		t.Fatalf("verification must be bound to the IP: %v", err)
	}

	// 站点没启用验证码服务商时，即使配置要求也不拦
	relaxed := newTrialServiceForTest(trialConfigForTest(), &fakeTrialQuota{}, false)
	if _, err := relaxed.PrepareChat(ctx, testTrialDevice, "3.3.3.3", trialChatBody); err != nil {
		t.Fatalf("no captcha provider configured: %v", err)
	}

	failing := newTrialServiceForTest(trialConfigForTest(), &fakeTrialQuota{}, true)
	failing.captcha = fakeTrialCaptcha{err: ErrTencentCaptchaVerificationFailed}
	if err := failing.Verify(ctx, testTrialDevice, "4.4.4.4", CaptchaProof{}); !errors.Is(err, ErrTencentCaptchaVerificationFailed) {
		t.Fatalf("failed captcha must surface: %v", err)
	}
}

func TestGuestTrialStateHidesEverythingWhenDisabled(t *testing.T) {
	cfg := trialConfigForTest()
	cfg.Enabled = false
	state, err := newTrialServiceForTest(cfg, &fakeTrialQuota{}, true).State(context.Background(), testTrialDevice, "ip")
	if err != nil || state.Enabled || len(state.Models) != 0 {
		t.Fatalf("disabled state leaked details: %+v %v", state, err)
	}
}

func TestGuestTrialClosedInBackendMode(t *testing.T) {
	svc := newTrialServiceForTest(trialConfigForTest(), &fakeTrialQuota{}, false)
	svc.backendMode = func(context.Context) bool { return true }
	if _, err := svc.PrepareChat(context.Background(), testTrialDevice, "ip", trialChatBody); !errors.Is(err, ErrGuestTrialDisabled) {
		t.Fatalf("backend mode must close the trial: %v", err)
	}
}
