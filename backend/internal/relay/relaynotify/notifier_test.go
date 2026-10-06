package relaynotify

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type memSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memSettings) GetValue(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[key]; ok {
		return v, nil
	}
	return "", service.ErrSettingNotFound
}

func (s *memSettings) Set(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}

// reversibleEncryptor 让测试能确认存进设置里的不是明文。
type reversibleEncryptor struct{}

func (reversibleEncryptor) Encrypt(p string) (string, error) {
	return "enc:" + base64.StdEncoding.EncodeToString([]byte(p)), nil
}

func (reversibleEncryptor) Decrypt(c string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(c, "enc:"))
	return string(b), err
}

type alertRecorder struct {
	mu       sync.Mutex
	created  []string
	resolved []int64
}

func (a *alertRecorder) CreateAlert(_ context.Context, severity, title, _ string, dims map[string]any) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.created = append(a.created, severity+"|"+title)
	return int64(len(a.created)), nil
}

func (a *alertRecorder) ResolveAlert(_ context.Context, id int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resolved = append(a.resolved, id)
	return nil
}

type emailRecorder struct {
	mu   sync.Mutex
	sent []string
}

func (e *emailRecorder) Recipients(context.Context) []string { return []string{"ops@example.com"} }
func (e *emailRecorder) Send(_ context.Context, to, subject, _ string) error {
	e.mu.Lock()
	e.sent = append(e.sent, to+"|"+subject)
	e.mu.Unlock()
	return nil
}

type nodeSource map[int64]master.NodeContext

func (s nodeSource) NodeContext(_ context.Context, id int64) (master.NodeContext, bool) {
	nc, ok := s[id]
	return nc, ok
}

type feishuServer struct {
	*httptest.Server
	mu       sync.Mutex
	bodies   []map[string]any
	failNext int
}

func newFeishuServer(t *testing.T) *feishuServer {
	t.Helper()
	fs := &feishuServer{}
	fs.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		defer fs.mu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if fs.failNext > 0 {
			fs.failNext--
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fs.bodies = append(fs.bodies, body)
		_, _ = w.Write([]byte(`{"code":0,"msg":"success"}`))
	}))
	t.Cleanup(fs.Close)
	return fs
}

func (fs *feishuServer) texts() []string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	var out []string
	for _, b := range fs.bodies {
		out = append(out, b["content"].(map[string]any)["text"].(string))
	}
	return out
}

type notifyEnv struct {
	*Notifier
	settings *memSettings
	alerts   *alertRecorder
	email    *emailRecorder
	feishu   *feishuServer
	clock    time.Time
	sleeps   []time.Duration
}

func newNotifyEnv(t *testing.T) *notifyEnv {
	t.Helper()
	env := &notifyEnv{settings: &memSettings{m: map[string]string{}}, alerts: &alertRecorder{}, email: &emailRecorder{}, feishu: newFeishuServer(t),
		clock: time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)}
	env.Notifier = New(Options{
		Settings: env.settings, Encryptor: reversibleEncryptor{}, Alerts: env.alerts, Email: env.email,
		AllowedHosts: []string{"127.0.0.1"},
		HTTPClient:   &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}},
		SiteURL:      func(context.Context) string { return "https://panel.example.com/" },
		Now:          func() time.Time { return env.clock },
		Sleep:        func(d time.Duration) { env.sleeps = append(env.sleeps, d) },
	})
	env.SetContextSource(nodeSource{
		7: {Name: "tokyo-1", IP: "203.0.113.7", AssignedUsers: 12, ActiveKeys: 8, Inflight: 30},
		8: {Name: "osaka-1", IP: "203.0.113.8"},
		9: {Name: "seoul-1", IP: "203.0.113.9"},
	})
	return env
}

func (e *notifyEnv) configureFeishu(t *testing.T) {
	t.Helper()
	on, hook, secret := true, e.feishu.URL+"/open-apis/bot/v2/hook/abc", "s3cret"
	_, err := e.SetConfig(context.Background(), ConfigUpdate{FeishuEnabled: &on, WebhookURL: &hook, Secret: &secret})
	require.NoError(t, err)
}

func offline(node int64) master.Event {
	return master.Event{Kind: master.EventNodeOffline, Severity: master.SeverityCritical, NodeID: node}
}

// 设计 13：飞书 Webhook 地址和签名密钥加密保存、管理页不回传明文；地址必须是飞书 / Lark 机器人的 Webhook。
func TestFeishuSecretsAreEncryptedAndNeverReturned(t *testing.T) {
	ctx := context.Background()
	e := newNotifyEnv(t)
	e.configureFeishu(t)

	stored := e.settings.m[SettingKeyConfig]
	require.NotContains(t, stored, "s3cret")
	require.NotContains(t, stored, "open-apis", "the webhook address is encrypted at rest too")
	cfg, err := e.Config(ctx)
	require.NoError(t, err)
	require.True(t, cfg.FeishuEnabled)
	require.True(t, cfg.WebhookConfigured)
	require.True(t, cfg.SecretConfigured)
	require.Equal(t, "127.0.0.1:"+e.feishu.URL[strings.LastIndex(e.feishu.URL, ":")+1:], cfg.WebhookHost, "only the host is shown")
	raw, _ := json.Marshal(cfg)
	require.NotContains(t, string(raw), "s3cret")
	require.NotContains(t, string(raw), "abc")
	require.Len(t, cfg.Events, len(eventSpecs))

	bad := "https://evil.example.com/open-apis/bot/v2/hook/abc"
	_, err = e.SetConfig(ctx, ConfigUpdate{WebhookURL: &bad})
	require.ErrorIs(t, err, ErrInvalidConfig, "only Feishu or Lark bot addresses are accepted")
	plain := "http://open.feishu.cn/open-apis/bot/v2/hook/abc"
	_, err = e.SetConfig(ctx, ConfigUpdate{WebhookURL: &plain})
	require.ErrorIs(t, err, ErrInvalidConfig, "https only")
	wrongPath := "https://open.feishu.cn/somewhere/else"
	_, err = e.SetConfig(ctx, ConfigUpdate{WebhookURL: &wrongPath})
	require.ErrorIs(t, err, ErrInvalidConfig)
	good := "https://open.feishu.cn/open-apis/bot/v2/hook/xyz"
	_, err = e.SetConfig(ctx, ConfigUpdate{WebhookURL: &good})
	require.NoError(t, err)

	// 清除地址后不能再保持开启。
	empty := ""
	_, err = e.SetConfig(ctx, ConfigUpdate{WebhookURL: &empty})
	require.ErrorIs(t, err, ErrInvalidConfig)
	off := false
	_, err = e.SetConfig(ctx, ConfigUpdate{FeishuEnabled: &off, WebhookURL: &empty})
	require.NoError(t, err)

	_, err = e.SetConfig(ctx, ConfigUpdate{Events: map[string]EventSetting{"no_such_event": {}}})
	require.ErrorIs(t, err, ErrInvalidConfig)
	tooLong := 9999
	_, err = e.SetConfig(ctx, ConfigUpdate{MergeWindowSeconds: &tooLong})
	require.ErrorIs(t, err, ErrInvalidConfig)
}

// 设计 13：飞书消息带签名（时间戳 + HMAC-SHA256 的 Base64），内容是节点名称、IP、事件时间、分配人数、活跃 Key、进行中的请求、
// 系统已采取的动作和管理页链接，不含用户信息；同时记进运维告警。
func TestOfflineNotificationContentSignatureAndAlert(t *testing.T) {
	ctx := context.Background()
	e := newNotifyEnv(t)
	e.configureFeishu(t)
	hb := e.clock.Add(-12 * time.Second)
	e.SetContextSource(nodeSource{7: {Name: "tokyo-1", IP: "203.0.113.7", LastHeartbeat: &hb, AssignedUsers: 12, ActiveKeys: 8, Inflight: 30}})

	e.Notify(ctx, offline(7))
	require.Empty(t, e.feishu.texts(), "waits for the merge window")
	e.Flush(ctx)

	require.Len(t, e.feishu.bodies, 1)
	body := e.feishu.bodies[0]
	require.Equal(t, "text", body["msg_type"])
	ts, sign := body["timestamp"].(string), body["sign"].(string)
	require.Equal(t, feishuSign(ts, "s3cret"), sign)
	require.NotEqual(t, feishuSign(ts, "other"), sign)
	text := e.feishu.texts()[0]
	for _, want := range []string{"[严重] 从节点离线", "tokyo-1（203.0.113.7）", "10-06 12:00:00", "最后心跳 11:59:48", "分配用户 12", "活跃 Key 8", "进行中请求 30",
		"系统已采取：已停止给它分配新用户和新 Key", "https://panel.example.com/admin/relay"} {
		require.Contains(t, text, want)
	}
	require.NotContains(t, text, "@", "no user emails or credentials")
	require.Equal(t, []string{"P0|从节点离线"}, e.alerts.created, "recorded in the existing ops alerts")
	require.Equal(t, []string{"ops@example.com|[严重] 从节点离线"}, e.email.sent, "critical events also go by email")
}

// 恢复事件把对应的运维告警标为已恢复；提示类事件只通知，不占运维告警。
func TestRecoveryResolvesTheOpsAlert(t *testing.T) {
	ctx := context.Background()
	e := newNotifyEnv(t)
	e.configureFeishu(t)
	e.Notify(ctx, offline(7))
	e.Notify(ctx, master.Event{Kind: master.EventNodeOnline, Severity: master.SeverityInfo, NodeID: 7})
	require.Equal(t, []int64{1}, e.alerts.resolved)
	e.Notify(ctx, master.Event{Kind: master.EventNodeRegistered, Severity: master.SeverityInfo, NodeID: 8})
	require.Len(t, e.alerts.created, 1, "info events do not create alerts")
}

// 飞书发送失败时重试（最多 3 次，之间等待），仍失败改发邮件；没配飞书时想发飞书的事件改发邮件。
func TestFeishuRetriesThenFallsBackToEmail(t *testing.T) {
	ctx := context.Background()
	e := newNotifyEnv(t)
	e.configureFeishu(t)

	e.feishu.failNext = 2
	e.Notify(ctx, master.Event{Kind: master.EventNodeRegistered, Severity: master.SeverityInfo, NodeID: 8})
	e.Flush(ctx)
	require.Len(t, e.feishu.bodies, 1, "the third attempt got through")
	require.Len(t, e.sleeps, 2, "waited between attempts")
	require.Empty(t, e.email.sent, "no fallback needed (and info events are not emailed)")

	e.feishu.failNext = 99
	e.Notify(ctx, master.Event{Kind: master.EventNodeRegistered, Severity: master.SeverityInfo, NodeID: 8})
	e.Flush(ctx)
	require.Len(t, e.feishu.bodies, 1)
	require.Len(t, e.email.sent, 1, "all attempts failed: sent by email instead")

	// 没配飞书：飞书渠道的事件改发邮件。
	e2 := newNotifyEnv(t)
	e2.Notify(ctx, master.Event{Kind: master.EventNodeRegistered, Severity: master.SeverityInfo, NodeID: 8})
	e2.Flush(ctx)
	require.Len(t, e2.email.sent, 1)

	// 测试发送：失败原样报错、不改发邮件；没配时报没配。
	require.ErrorIs(t, e2.SendTest(ctx), ErrFeishuNotConfigured)
	e.feishu.failNext = 99
	require.Error(t, e.SendTest(ctx))
	e.feishu.failNext = 0
	require.NoError(t, e.SendTest(ctx))
}

// 避免刷屏：多台同时出事合并成一条；同一类超过 3 台只列前 3 台。
func TestSimultaneousEventsAreMergedIntoOneMessage(t *testing.T) {
	ctx := context.Background()
	e := newNotifyEnv(t)
	e.configureFeishu(t)
	e.Notify(ctx, offline(7))
	e.Notify(ctx, offline(8))
	e.Notify(ctx, offline(9))
	e.Notify(ctx, master.Event{Kind: master.EventMasterCapReached, Severity: master.SeverityCritical})
	e.Flush(ctx)

	texts := e.feishu.texts()
	require.Len(t, texts, 1, "one message for everything that happened in the window")
	require.Contains(t, texts[0], "主从分流：4 条事件")
	require.Contains(t, texts[0], "从节点离线（严重） ×3")
	require.Contains(t, texts[0], "tokyo-1")
	require.Contains(t, texts[0], "osaka-1")
	require.Contains(t, texts[0], "主节点转发达到上限")
	require.Len(t, e.email.sent, 1, "and one email")

	// 大量注册合并：只列前 3 台，其余只给总数。
	e2 := newNotifyEnv(t)
	e2.configureFeishu(t)
	for i := int64(1); i <= 7; i++ {
		e2.Notify(ctx, master.Event{Kind: master.EventNodeRegistered, Severity: master.SeverityInfo, NodeID: i})
	}
	e2.Flush(ctx)
	require.Len(t, e2.feishu.texts(), 1)
	require.Contains(t, e2.feishu.texts()[0], "等共 7 条")
	require.Equal(t, 3, strings.Count(e2.feishu.texts()[0], "\n- 节点"))
}

// 避免刷屏：10 分钟内反复上下线合并成一条"不稳定"，之后一段时间不再单独通知它的上下线；运维告警照记。
func TestFlappingNodeIsReportedOnceAsUnstable(t *testing.T) {
	ctx := context.Background()
	e := newNotifyEnv(t)
	e.configureFeishu(t)
	online := master.Event{Kind: master.EventNodeOnline, Severity: master.SeverityInfo, NodeID: 7}

	for i := 0; i < 2; i++ {
		e.Notify(ctx, offline(7))
		e.Flush(ctx)
		e.Notify(ctx, online)
		e.Flush(ctx)
		e.clock = e.clock.Add(time.Minute)
	}
	require.Len(t, e.feishu.texts(), 4, "the first two cycles are announced normally")

	e.Notify(ctx, offline(7)) // 第三次离线：合并成一条"不稳定"，这次离线本身不再单独发
	e.Flush(ctx)
	texts := e.feishu.texts()
	require.Len(t, texts, 5)
	require.Contains(t, texts[4], "反复上下线（不稳定）")
	require.NotContains(t, texts[4], "从节点离线")

	// 静默期内的上下线不再单独发。
	e.Notify(ctx, online)
	e.Notify(ctx, offline(7))
	e.Flush(ctx)
	require.Len(t, e.feishu.texts(), 5)
	require.GreaterOrEqual(t, len(e.alerts.created), 4, "ops alerts are still recorded")

	// 别的节点不受影响；静默期过后恢复正常。
	e.Notify(ctx, offline(8))
	e.Flush(ctx)
	require.Len(t, e.feishu.texts(), 6)
	e.clock = e.clock.Add(11 * time.Minute)
	e.Notify(ctx, offline(7))
	e.Flush(ctx)
	require.Len(t, e.feishu.texts(), 7)
}

// 每类事件可以单独关、单独选渠道（设计 13 的表：默认开关）。
func TestEventSettingsControlWhetherAndWhereItIsSent(t *testing.T) {
	ctx := context.Background()
	e := newNotifyEnv(t)
	e.configureFeishu(t)

	// 默认关的事件不发。
	e.Notify(ctx, master.Event{Kind: master.EventNodeOverloaded, Severity: master.SeverityWarning, NodeID: 7})
	e.Notify(ctx, master.Event{Kind: "unknown_kind", NodeID: 7})
	e.Flush(ctx)
	require.Empty(t, e.feishu.texts())
	require.Empty(t, e.alerts.created)

	on, off := true, false
	_, err := e.SetConfig(ctx, ConfigUpdate{Events: map[string]EventSetting{
		master.EventNodeOverloaded: {Enabled: &on},
		master.EventNodeOffline:    {Email: &off},
		master.EventNodeRegistered: {Enabled: &off},
	}})
	require.NoError(t, err)
	e.Notify(ctx, master.Event{Kind: master.EventNodeOverloaded, Severity: master.SeverityWarning, NodeID: 7})
	e.Flush(ctx)
	require.Len(t, e.feishu.texts(), 1, "switched on")

	e.Notify(ctx, offline(7))
	e.Flush(ctx)
	require.Len(t, e.feishu.texts(), 2)
	for _, sent := range e.email.sent {
		require.NotContains(t, sent, "从节点离线", "the email channel is off for this event")
	}
	e.Notify(ctx, master.Event{Kind: master.EventNodeRegistered, Severity: master.SeverityInfo, NodeID: 8})
	e.Flush(ctx)
	require.Len(t, e.feishu.texts(), 2, "switched off")

	cfg, err := e.Config(ctx)
	require.NoError(t, err)
	got := map[string]PublicEvent{}
	for _, ev := range cfg.Events {
		got[ev.Kind] = ev
	}
	require.True(t, got[master.EventNodeOverloaded].Enabled)
	require.False(t, got[master.EventNodeOverloaded].DefaultEnabled)
	require.False(t, got[master.EventNodeRegistered].Enabled)
	require.False(t, got[master.EventNodeOffline].Email)
	require.True(t, got[master.EventNodeOffline].Feishu)
}

// 设计 13 的事件表：每一类事件都有默认设置，且严重程度、默认开关与表一致（"关"的只有压力和版本落后两类）。
func TestEveryEventInTheDesignTableHasADefault(t *testing.T) {
	for _, kind := range []string{
		master.EventNodeOffline, master.EventNodeOnline, master.EventNodeUnreachable, master.EventNodeDNSMismatch, master.EventNodeCertFailed,
		master.EventNodeCertExpiring, master.EventAllRelaysDownFallback, master.EventAllRelaysDownOutage, master.EventMasterRatioChanged,
		master.EventRelayRecovered, master.EventMasterCapReached, master.EventIdentityDuplicated, master.EventCertificateRecovered,
		master.EventNodeRegistered, master.EventPendingPurged, master.EventUserPinReleased, master.EventBillingBacklog, master.EventNodeBlocked,
		master.EventNodeSelectFlood, master.EventNodeUnreleased, master.EventNodeIPChanged, master.EventNodeOverloaded, master.EventNodeStaleVersion,
	} {
		spec, ok := specFor(kind)
		require.True(t, ok, kind)
		require.NotEmpty(t, spec.Title, kind)
	}
	off := map[string]bool{master.EventNodeOverloaded: true, master.EventNodeStaleVersion: true}
	for _, s := range eventSpecs {
		require.Equal(t, !off[s.Kind], s.DefaultOn, s.Kind)
	}
	spec, _ := specFor(master.EventAllRelaysDownFallback)
	require.Equal(t, master.SeverityEmergency, spec.Severity, "emergency")
	spec, _ = specFor(master.EventNodeOnline)
	require.Equal(t, master.SeverityInfo, spec.Severity)
}
