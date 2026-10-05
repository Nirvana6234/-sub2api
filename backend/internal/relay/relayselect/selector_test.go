package relayselect

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/relay/accountcodec"
	"github.com/Wei-Shaw/sub2api/internal/relay/keycodec"
	"github.com/Wei-Shaw/sub2api/internal/relay/keystore"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sealbox"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// ---- 假仓储 ----

type fakeKeys struct {
	service.APIKeyRepository
	keys map[string]*service.APIKey
}

func (r fakeKeys) GetByKey(_ context.Context, key string) (*service.APIKey, error) {
	if k, ok := r.keys[key]; ok {
		clone := *k
		return &clone, nil
	}
	return nil, service.ErrAPIKeyNotFound
}

func (r fakeKeys) GetByKeyForAuth(ctx context.Context, key string) (*service.APIKey, error) {
	return r.GetByKey(ctx, key)
}

type fakeAccounts struct {
	service.AccountRepository
	accounts []service.Account
}

func (r fakeAccounts) GetByID(_ context.Context, id int64) (*service.Account, error) {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			a := r.accounts[i]
			return &a, nil
		}
	}
	return nil, service.ErrNoAvailableAccounts
}

func (r fakeAccounts) forPlatform(platform string) []service.Account {
	var out []service.Account
	for _, a := range r.accounts {
		if a.Platform == platform {
			out = append(out, a)
		}
	}
	return out
}

// ListSchedulableByGroupIDAndPlatform：写了 AccountGroups 的账号只在这些分组里（没写的在所有分组里）。
func (r fakeAccounts) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, platform string) ([]service.Account, error) {
	var out []service.Account
	for _, a := range r.forPlatform(platform) {
		in := len(a.AccountGroups) == 0
		for _, g := range a.AccountGroups {
			in = in || g.GroupID == groupID
		}
		if in {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r fakeAccounts) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]service.Account, error) {
	var out []service.Account
	for _, p := range platforms {
		accounts, _ := r.ListSchedulableByGroupIDAndPlatform(ctx, groupID, p)
		out = append(out, accounts...)
	}
	return out, nil
}

// ListSchedulableUngroupedByPlatforms：没有写 AccountGroups 的账号当作"没有分组"（未分组 Key 选的）。
func (r fakeAccounts) ListSchedulableUngroupedByPlatforms(_ context.Context, platforms []string) ([]service.Account, error) {
	var out []service.Account
	for _, p := range platforms {
		for _, a := range r.forPlatform(p) {
			if len(a.AccountGroups) == 0 {
				out = append(out, a)
			}
		}
	}
	return out, nil
}

func (r fakeAccounts) ListSchedulableByPlatforms(_ context.Context, platforms []string) ([]service.Account, error) {
	var out []service.Account
	for _, p := range platforms {
		out = append(out, r.forPlatform(p)...)
	}
	return out, nil
}

func (r fakeAccounts) ListModelAvailabilityCandidates(_ context.Context, _ *int64, platforms []string, _ bool) ([]service.Account, error) {
	var out []service.Account
	for _, p := range platforms {
		out = append(out, r.forPlatform(p)...)
	}
	return out, nil
}

func (r fakeAccounts) ListSchedulableByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	return r.forPlatform(platform), nil
}

func (r fakeAccounts) ListSchedulableUngroupedByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	return r.forPlatform(platform), nil
}

type memSettings struct{ values map[string]string }

func (s memSettings) Get(context.Context, string) (*service.Setting, error) {
	return nil, service.ErrSettingNotFound
}
func (s memSettings) GetValue(_ context.Context, key string) (string, error) {
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", service.ErrSettingNotFound
}
func (s memSettings) Set(context.Context, string, string) error { return errors.New("read only") }
func (s memSettings) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := s.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}
func (s memSettings) SetMultiple(context.Context, map[string]string) error {
	return errors.New("read only")
}
func (s memSettings) GetAll(context.Context) (map[string]string, error) { return s.values, nil }
func (s memSettings) Delete(context.Context, string) error              { return errors.New("read only") }

// countingSlots 记用户槽和账号槽的占用和释放。
type countingSlots struct {
	service.ConcurrencyCache
	held     atomic.Int64
	accounts atomic.Int64
	// userAcquires 是用户槽一共占过几次。
	userAcquires atomic.Int64
	// accountLimit 大于 0 时账号槽最多占这么多（测"账号忙"）。
	accountLimit atomic.Int64

	leaseMu sync.Mutex
	leases  map[string]int64 // lease id -> api key id
}

func (c *countingSlots) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	if limit := c.accountLimit.Load(); limit > 0 && c.accounts.Load() >= limit {
		return false, nil
	}
	c.accounts.Add(1)
	return true, nil
}

func (c *countingSlots) AcquireOpenAIWSIngressLease(_ context.Context, apiKeyID int64, maxConnections int, leaseID string) (bool, error) {
	c.leaseMu.Lock()
	defer c.leaseMu.Unlock()
	if c.leases == nil {
		c.leases = map[string]int64{}
	}
	n := 0
	for _, k := range c.leases {
		if k == apiKeyID {
			n++
		}
	}
	if n >= maxConnections {
		return false, nil
	}
	c.leases[leaseID] = apiKeyID
	return true, nil
}

func (c *countingSlots) RefreshOpenAIWSIngressLease(_ context.Context, apiKeyID int64, leaseID string) (bool, error) {
	c.leaseMu.Lock()
	defer c.leaseMu.Unlock()
	return c.leases[leaseID] == apiKeyID, nil
}

func (c *countingSlots) ReleaseOpenAIWSIngressLease(_ context.Context, _ int64, leaseID string) error {
	c.leaseMu.Lock()
	defer c.leaseMu.Unlock()
	delete(c.leases, leaseID)
	return nil
}

func (c *countingSlots) ReleaseAccountSlot(context.Context, int64, string) error {
	c.accounts.Add(-1)
	return nil
}

func (c *countingSlots) AcquireUserSlot(context.Context, int64, int, string) (bool, error) {
	c.userAcquires.Add(1)
	c.held.Add(1)
	return true, nil
}

func (c *countingSlots) ReleaseUserSlot(context.Context, int64, string) error {
	c.held.Add(-1)
	return nil
}

type balanceCache struct {
	service.BillingCache
	balance float64
}

func (b balanceCache) GetUserBalance(context.Context, int64) (float64, error) { return b.balance, nil }

// ---- 环境 ----

type world struct {
	keys    fakeKeys
	sel     *selector
	slots   *countingSlots
	nodeKey *ecdh.PrivateKey
	pub     *sign.PublicKeys
	leases  *master.MemoryLeaseStore
	quotas  *master.Quotas
	// identity 是主节点的身份缓存（指纹、伪装会话 ID）。
	identity *memIdentity
	// groups 是主节点分组仓储里的分组（解析兜底分组用；用例往里加）。
	groups *memGroups
}

const testNode = int64(21)

// testGatewayCache 给测试世界的网关服务装上缓存（粘性会话等）；nil 时不装。用例用 useGatewayCache 设置。
var testGatewayCache service.GatewayCache

func useGatewayCache(t *testing.T, c service.GatewayCache) {
	t.Helper()
	testGatewayCache = c
	t.Cleanup(func() { testGatewayCache = nil })
}

func openAIGroup(id int64) *service.Group {
	return &service.Group{ID: id, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1}
}

func testKey(key string, id int64, group *service.Group) *service.APIKey {
	k := &service.APIKey{ID: id, Key: key, UserID: 3, Status: service.StatusActive,
		User: &service.User{ID: 3, Status: service.StatusActive, Balance: 10, Concurrency: 5}}
	if group != nil {
		k.GroupID, k.Group = &group.ID, group
	}
	return k
}

func newWorld(t *testing.T, runMode string, accounts ...service.Account) *world {
	return newWorldWithBalance(t, runMode, 10, accounts...)
}

func newWorldWithBalance(t *testing.T, runMode string, balance float64, accounts ...service.Account) *world {
	t.Helper()
	return newWorldOn(t, &config.Config{RunMode: runMode}, balance, testNode, accounts...)
}

// newWorldOn 同 newWorldWithBalance，可指定配置和节点 ID（端到端测试用主节点登记的节点）。
func newWorldOn(t *testing.T, cfg *config.Config, balance float64, nodeID int64, accounts ...service.Account) *world {
	t.Helper()
	anthropic := openAIGroup(9)
	anthropic.Platform = service.PlatformAnthropic
	gemini := openAIGroup(10)
	gemini.Platform = service.PlatformGemini
	keys := fakeKeys{keys: map[string]*service.APIKey{
		"sk-a":         testKey("sk-a", 11, openAIGroup(5)),
		"sk-b":         testKey("sk-b", 12, openAIGroup(5)),
		"sk-anthropic": testKey("sk-anthropic", 13, anthropic),
		"sk-gemini":    testKey("sk-gemini", 14, gemini),
	}}
	slots := &countingSlots{}
	concurrency := service.NewConcurrencyService(slots)
	billing := service.NewBillingCacheService(balanceCache{balance: balance}, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	var gatewayCache service.GatewayCache
	if testGatewayCache != nil {
		gatewayCache = testGatewayCache
	}
	gateway := service.NewOpenAIGatewayService(fakeAccounts{accounts: accounts}, nil, nil, nil, nil, nil, gatewayCache, cfg,
		nil, concurrency, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	kek := make([]byte, keystore.KEKLength)
	_, err := rand.Read(kek)
	require.NoError(t, err)
	store, err := keystore.Open(t.TempDir(), kek)
	require.NoError(t, err)
	_, err = store.EnsureActive(keystore.PurposeVoucher)
	require.NoError(t, err)
	ring, err := store.Ring(keystore.PurposeVoucher)
	require.NoError(t, err)
	signer, err := sign.NewSigner(ring.Active)
	require.NoError(t, err)
	pub, _, err := sign.PublicKeysFromRing(ring)
	require.NoError(t, err)

	nodeKey, err := sealbox.GenerateKey()
	require.NoError(t, err)
	leases := master.NewMemoryLeaseStore()
	quotas, err := master.NewQuotas(context.Background(), leases, "epoch-1", time.Now)
	require.NoError(t, err)

	identity := &memIdentity{fingerprints: map[int64]*service.Fingerprint{}, masked: map[int64]string{}}
	groupRepo := &memGroups{byID: map[int64]*service.Group{}}
	anthropicGateway := service.NewGatewayService(fakeAccounts{accounts: accounts}, groupRepo, nil, nil, nil, nil, nil, gatewayCache, cfg,
		nil, concurrency, nil, nil, billing, service.NewIdentityService(identity), nil, nil,
		service.NewClaudeTokenProvider(nil, seededTokens{}, nil), nil, nil, service.NewDigestSessionStore(), nil, nil, nil, nil, nil, nil, nil)
	geminiCompat := service.NewGeminiMessagesCompatService(fakeAccounts{accounts: accounts}, groupRepo, gatewayCache, nil,
		service.NewGeminiTokenProvider(nil, seededTokens{}, nil), nil, nil, nil, cfg)

	sel := newSelector(Deps{
		Config: cfg, APIKeys: service.NewAPIKeyService(keys, nil, nil, nil, nil, nil, cfg),
		Settings: service.NewSettingService(memSettings{values: map[string]string{}}, cfg),
		Billing:  billing, Gateway: gateway, AnthropicGateway: anthropicGateway, Concurrency: concurrency, Gemini: geminiCompat,
	}, master.SelectEnv{
		Epoch:  "epoch-1",
		Quotas: quotas,
		IssueVoucher: func(v *relayv1.Voucher) ([]byte, *relayv1.Voucher, error) {
			return sign.IssueVoucher(signer, v, time.Now())
		},
		NodeEncryptionKey: func(id int64) (*ecdh.PublicKey, bool) {
			return nodeKey.PublicKey(), id == nodeID
		},
		ConfigVersion: func(context.Context, int64) (string, error) { return "cfg-v1", nil },
		VerifyVoucher: func(raw []byte, nodeID int64) (*relayv1.Voucher, error) {
			return sign.VerifyVoucher(raw, pub, nodeID, time.Now())
		},
	})
	t.Cleanup(sel.Close)
	return &world{keys: keys, sel: sel, slots: slots, nodeKey: nodeKey, pub: pub, leases: leases, quotas: quotas, identity: identity, groups: groupRepo}
}

func apiKeyAccount(id int64, name string) service.Account {
	return service.Account{ID: id, Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 2, Credentials: map[string]any{"api_key": "SECRET-" + name, "base_url": "https://up.example"}}
}

func responsesRequest(requestID string, attempt uint32, key string) *relayv1.SelectRequest {
	return &relayv1.SelectRequest{RequestId: requestID, Attempt: attempt, Credential: &relayv1.SelectRequest_ApiKey{ApiKey: key},
		Method: "POST", Path: "/v1/responses", Endpoint: relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_RESPONSES, Model: "gpt-5", ClientIp: "5.6.7.8"}
}

func (w *world) waitReleased(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool { return w.slots.held.Load() == 0 }, 2*time.Second, 5*time.Millisecond, "the user slot is given back")
	require.Eventually(t, func() bool { return w.slots.accounts.Load() == 0 }, 2*time.Second, 5*time.Millisecond, "account slots are given back")
}

// ---- 用例 ----

// 选中的账号、加密下发的凭据、签好的凭证；释放时放槽、记下响应归属，后续续链能通过归属检查。
func TestSelectResponsesHappyPath(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeSimple, apiKeyAccount(1, "one"))

	resp, err := w.sel.Select(ctx, testNode, responsesRequest("r1", 1, "sk-a"))
	require.NoError(t, err)
	sel := resp.GetSelection()
	require.NotNil(t, sel, "rejection: %+v", resp.GetRejection())
	require.Equal(t, int64(1), sel.GetAccount().GetId())
	require.Equal(t, int64(3), sel.GetUserId())
	require.Equal(t, int64(5), sel.GetGroupId())
	require.Equal(t, "cfg-v1", sel.GetConfigVersion())
	require.Equal(t, "gpt-5", sel.GetForwardModel())
	require.Equal(t, int64(1), w.slots.held.Load(), "the user slot is held for the whole request")

	cache := accountcodec.NewSecretCache()
	open := func(sealed, aad []byte) ([]byte, error) { return sealbox.Open(w.nodeKey, sealed, aad) }
	account, err := accountcodec.Decode(sel.GetAccount(), testNode, open, cache)
	require.NoError(t, err)
	require.Equal(t, "SECRET-one", account.Credentials["api_key"])

	v, err := sign.VerifyVoucher(sel.GetVoucher(), w.pub, testNode, time.Now())
	require.NoError(t, err)
	require.Equal(t, sel.GetSelectionId(), v.GetSelectionId())
	require.Equal(t, int64(1), v.GetAccountId())
	require.Equal(t, sel.GetPricingAtUnixMs(), v.GetContext().GetPricingAtUnixMs())
	_, err = sign.VerifyVoucher(sel.GetVoucher(), w.pub, testNode+1, time.Now())
	require.Error(t, err, "only the selecting node can report it")

	// 同一个账号再次被选中：凭据版本相同，不再下发。
	again, err := w.sel.Select(ctx, testNode, responsesRequest("r2", 1, "sk-a"))
	require.NoError(t, err)
	require.Empty(t, again.GetSelection().GetAccount().GetSealedCredentials(), "the node already has this credential version")
	require.Equal(t, int64(2), w.slots.accounts.Load(), "account slots are held after the call returns, until the node releases them")
	_, err = accountcodec.Decode(again.GetSelection().GetAccount(), testNode, open, cache)
	require.NoError(t, err)
	creds, err := w.sel.FetchCredentials(ctx, testNode, &relayv1.FetchCredentialsRequest{SelectionId: again.GetSelection().GetSelectionId()})
	require.NoError(t, err)
	require.NotEmpty(t, creds.GetAccount().GetSealedCredentials(), "a node that lost its cache can fetch them for a live selection")
	_, err = w.sel.FetchCredentials(ctx, testNode+1, &relayv1.FetchCredentialsRequest{SelectionId: again.GetSelection().GetSelectionId()})
	require.ErrorIs(t, err, master.ErrSelectionNotFound, "another node cannot use this selection")

	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), RequestDone: true, ResponseIds: []string{"resp_one"}})
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: again.GetSelection().GetSelectionId(), RequestDone: true})
	w.waitReleased(t)
	_, err = w.sel.FetchCredentials(ctx, testNode, &relayv1.FetchCredentialsRequest{SelectionId: sel.GetSelectionId()})
	require.ErrorIs(t, err, master.ErrSelectionNotFound, "released selections are gone")

	// 续链：本人（同一用户的另一个 Key 也算）可以，别人不行。
	cont := responsesRequest("r3", 1, "sk-b")
	cont.PreviousResponseId = "resp_one"
	resp, err = w.sel.Select(ctx, testNode, cont)
	require.NoError(t, err)
	require.NotNil(t, resp.GetSelection(), "the response owner was recorded on release: %+v", resp.GetRejection())
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: resp.GetSelection().GetSelectionId(), RequestDone: true})
	w.waitReleased(t)

	cont = responsesRequest("r4", 1, "sk-a")
	cont.PreviousResponseId = "resp_unknown"
	resp, err = w.sel.Select(ctx, testNode, cont)
	require.NoError(t, err)
	require.Equal(t, int32(400), resp.GetRejection().GetStatus())
	require.Equal(t, "previous_response_id is not available for this user", resp.GetRejection().GetMessage())
	require.Equal(t, int64(0), w.slots.held.Load(), "rejected before any slot is taken")
}

func TestSelectRejectionsAndUnsupportedRequests(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeSimple, apiKeyAccount(1, "one"))

	resp, err := w.sel.Select(ctx, testNode, responsesRequest("r1", 1, "sk-nope"))
	require.NoError(t, err)
	rej := resp.GetRejection()
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_RAW, rej.GetFormat())
	require.Equal(t, int32(401), rej.GetStatus())
	require.JSONEq(t, `{"code":"INVALID_API_KEY","message":"Invalid API key"}`, string(rej.GetBody()))
	require.Equal(t, "invalid_api_key", rej.GetIngressRejectReason())

	resp, err = w.sel.Select(ctx, testNode, responsesRequest("r2", 1, "sk-anthropic"))
	require.NoError(t, err)
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_UNSUPPORTED, resp.GetRejection().GetFormat(), "non-OpenAI groups stay on the master for now")

	unknown := responsesRequest("r3", 1, "sk-a")
	unknown.Endpoint = relayv1.SelectEndpoint_SELECT_ENDPOINT_UNSPECIFIED
	resp, err = w.sel.Select(ctx, testNode, unknown)
	require.NoError(t, err)
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_UNSUPPORTED, resp.GetRejection().GetFormat())
	require.Equal(t, int64(0), w.slots.held.Load())
}

// 准入：拒绝与选号时同样按中间件写法；通过时回白名单化的 Key 快照，不占任何槽。
func TestAdmit(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeSimple, apiKeyAccount(1, "one"))
	admit := func(key string) *relayv1.AdmitResponse {
		resp, err := w.sel.Admit(ctx, testNode, &relayv1.AdmitRequest{Credential: &relayv1.AdmitRequest_ApiKey{ApiKey: key}, ClientIp: "5.6.7.8", Method: "POST", Path: "/v1/responses"})
		require.NoError(t, err)
		return resp
	}

	rej := admit("sk-nope").GetRejection()
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_RAW, rej.GetFormat())
	require.Equal(t, int32(401), rej.GetStatus())
	require.JSONEq(t, `{"code":"INVALID_API_KEY","message":"Invalid API key"}`, string(rej.GetBody()))

	// Gemini 原生入口（/v1beta）的拒绝按 Google 格式写。
	googleResp, err := w.sel.Admit(ctx, testNode, &relayv1.AdmitRequest{Credential: &relayv1.AdmitRequest_ApiKey{ApiKey: "sk-nope"}, ClientIp: "5.6.7.8", Method: "POST", Path: "/v1beta/models/gemini-2.5-pro:generateContent"})
	require.NoError(t, err)
	require.Equal(t, int32(401), googleResp.GetRejection().GetStatus())
	require.JSONEq(t, `{"error":{"code":401,"message":"Invalid API key","status":"UNAUTHENTICATED"}}`, string(googleResp.GetRejection().GetBody()))

	// Anthropic 分组照常准入（哪个入口接由从节点的路由按分组平台分）；还没接入的平台回"暂不支持"。
	require.NotNil(t, admit("sk-anthropic").GetAdmission())
	require.NotNil(t, admit("sk-gemini").GetAdmission(), "Gemini groups are admitted too (their Messages entry is served; other entries are routed by the node)")
	grok := openAIGroup(12)
	grok.Platform = service.PlatformGrok
	w.keys.keys["sk-grok"] = testKey("sk-grok", 19, grok)
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_UNSUPPORTED, admit("sk-grok").GetRejection().GetFormat())

	adm := admit("sk-a").GetAdmission()
	require.NotNil(t, adm)
	key, err := keycodec.DecodeAPIKey(adm.GetApiKey(), "sk-a")
	require.NoError(t, err)
	require.Equal(t, int64(11), key.ID)
	require.Equal(t, int64(5), key.Group.ID)
	require.Equal(t, 5, key.User.Concurrency)
	require.Zero(t, key.User.Balance, "balances stay on the master")
	require.Empty(t, adm.GetSubscription())
	require.Equal(t, int64(0), w.slots.held.Load())
}

// 换号：主节点按请求记已排除的账号；全部用完时让从节点按它最近的上游错误写出，用户槽随请求结束放掉。
func TestSelectFailoverExhaustion(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeSimple, apiKeyAccount(1, "one"), apiKeyAccount(2, "two"))

	first, err := w.sel.Select(ctx, testNode, responsesRequest("r1", 1, "sk-a"))
	require.NoError(t, err)
	used := first.GetSelection().GetAccount().GetId()
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: first.GetSelection().GetSelectionId()})

	retry := responsesRequest("r1", 2, "sk-a")
	retry.ExcludedAccountIds = []int64{used}
	second, err := w.sel.Select(ctx, testNode, retry)
	require.NoError(t, err)
	require.NotEqual(t, used, second.GetSelection().GetAccount().GetId(), "the failed account is not selected again")
	require.Equal(t, int64(1), w.slots.held.Load(), "one user slot for the whole request")
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: second.GetSelection().GetSelectionId()})

	last := responsesRequest("r1", 3, "sk-a")
	last.ExcludedAccountIds = []int64{second.GetSelection().GetAccount().GetId()}
	resp, err := w.sel.Select(ctx, testNode, last)
	require.NoError(t, err)
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_FAILOVER_EXHAUSTED, resp.GetRejection().GetFormat())
	require.False(t, resp.GetRejection().GetContinuationUnsupported())
	w.waitReleased(t)
}

// previous_response_id 只能走 API Key 账号：只有 OAuth 账号时按"续链不支持"结束（与本地一致）。
func TestSelectContinuationSkipsOAuthAccounts(t *testing.T) {
	ctx := context.Background()
	oauth := apiKeyAccount(1, "oauth")
	oauth.Type = service.AccountTypeOAuth
	oauth.Credentials = map[string]any{"access_token": "SECRET-at"}
	w := newWorld(t, config.RunModeSimple, oauth)
	w.sel.deps.Gateway.BindRelayHTTPResponse(ctx, 5, 1, "resp_prev", 3, 11)

	req := responsesRequest("r1", 1, "sk-a")
	req.PreviousResponseId = "resp_prev"
	resp, err := w.sel.Select(ctx, testNode, req)
	require.NoError(t, err)
	require.Equal(t, relayv1.RejectionFormat_REJECTION_FORMAT_FAILOVER_EXHAUSTED, resp.GetRejection().GetFormat())
	require.True(t, resp.GetRejection().GetContinuationUnsupported())
	w.waitReleased(t)
}

// 额度：节点手里没有时随选号给；节点报告手里有时不再给。余额全部锁在这台节点上也照样能选号。
func TestSelectGrantsQuotaWhenTheNodeHasNone(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeStandard, apiKeyAccount(1, "one"))

	resp, err := w.sel.Select(ctx, testNode, responsesRequest("r1", 1, "sk-a"))
	require.NoError(t, err)
	sel := resp.GetSelection()
	require.NotNil(t, sel, "rejection: %+v", resp.GetRejection())
	require.Len(t, sel.GetQuotaScopes(), 1)
	require.Equal(t, service.QuotaDimBalance, sel.GetQuotaScopes()[0].GetDimension())
	require.Len(t, sel.GetGrants(), 1)
	granted := sel.GetGrants()[0].GetGranted()
	require.Equal(t, master.ToMicros(5), granted, "half of the balance")
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), RequestDone: true})
	w.waitReleased(t)

	held := []*relayv1.HeldQuota{{Scope: sel.GetQuotaScopes()[0], Unused: granted}}
	req := responsesRequest("r2", 1, "sk-a")
	req.HeldQuota = held
	resp, err = w.sel.Select(ctx, testNode, req)
	require.NoError(t, err)
	require.Empty(t, resp.GetSelection().GetGrants(), "the node still holds enough")

	refill, err := w.sel.RefillQuota(ctx, testNode, &relayv1.RefillQuotaRequest{SelectionId: resp.GetSelection().GetSelectionId(), HeldQuota: held, Need: 1})
	require.NoError(t, err)
	require.NotEmpty(t, refill.GetGrants(), "an early refill tops the lease up")
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: resp.GetSelection().GetSelectionId(), RequestDone: true})
	w.waitReleased(t)

	_, err = w.sel.RefillQuota(ctx, testNode, &relayv1.RefillQuotaRequest{SelectionId: resp.GetSelection().GetSelectionId(), Need: 1})
	require.ErrorIs(t, err, master.ErrSelectionNotFound, "refills need a live selection")
}

// 释放消息丢了：占用超过上限的请求被定时清理，关闭时放掉全部。
func TestStaleRequestsAreReapedAndCloseReleasesEverything(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeSimple, apiKeyAccount(1, "one"))
	_, err := w.sel.Select(ctx, testNode, responsesRequest("r1", 1, "sk-a"))
	require.NoError(t, err)
	require.Equal(t, int64(1), w.slots.held.Load())
	require.Equal(t, int64(1), w.slots.accounts.Load())

	var mu sync.Mutex
	later := time.Now().Add(holdLimit + time.Minute)
	w.sel.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return later }
	w.sel.reap()
	require.Equal(t, int64(0), w.slots.held.Load())
	require.Equal(t, int64(0), w.slots.accounts.Load())

	w.sel.now = time.Now
	_, err = w.sel.Select(ctx, testNode, responsesRequest("r2", 1, "sk-a"))
	require.NoError(t, err)
	require.Equal(t, int64(1), w.slots.accounts.Load())
	w.sel.Close()
	require.Equal(t, int64(0), w.slots.held.Load())
	require.Equal(t, int64(0), w.slots.accounts.Load())
	_, err = w.sel.Select(ctx, testNode, responsesRequest("r3", 1, "sk-a"))
	require.Error(t, err, "a closed selector does not select")
}

// 调用被取消（从节点不等了）：这次请求在主节点上结束，用户槽立刻放掉，不等定时清理。
func TestCancelledSelectReleasesTheUserSlot(t *testing.T) {
	w := newWorld(t, config.RunModeSimple, apiKeyAccount(1, "one"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := w.sel.Select(ctx, testNode, responsesRequest("r1", 1, "sk-a"))
	require.Error(t, err)
	require.Equal(t, int64(0), w.slots.held.Load())
	w.sel.mu.Lock()
	defer w.sel.mu.Unlock()
	require.Empty(t, w.sel.requests)
}

// 余额很少时整笔锁给了这台节点：主节点的余额预检不能把这台自己手里的当成"被别处锁走"。
func TestSelectWhenTheWholeBalanceIsLockedOnThisNode(t *testing.T) {
	ctx := context.Background()
	w := newWorldWithBalance(t, config.RunModeStandard, 0.05, apiKeyAccount(1, "one"))
	w.sel.deps.Billing.SetRelayReservedBalanceReader(w.quotas)

	resp, err := w.sel.Select(ctx, testNode, responsesRequest("r1", 1, "sk-a"))
	require.NoError(t, err)
	sel := resp.GetSelection()
	require.NotNil(t, sel, "rejection: %+v", resp.GetRejection())
	require.Equal(t, master.ToMicros(0.05), sel.GetGrants()[0].GetGranted(), "below 0.1 everything is locked to the node")
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), RequestDone: true})
	w.waitReleased(t)

	req := responsesRequest("r2", 1, "sk-a")
	req.HeldQuota = []*relayv1.HeldQuota{{Scope: sel.GetQuotaScopes()[0], Unused: master.ToMicros(0.05)}}
	resp, err = w.sel.Select(ctx, testNode, req)
	require.NoError(t, err)
	require.NotNil(t, resp.GetSelection(), "the node serves from what it holds: %+v", resp.GetRejection())

	other := responsesRequest("r3", 1, "sk-a")
	resp, err = w.sel.Select(ctx, testNode, other)
	require.NoError(t, err)
	require.Equal(t, int32(403), resp.GetRejection().GetStatus(), "a node holding nothing sees no spendable balance")
}

// 额度给出之后从节点不等了：原样收回，钱不白锁；之前没送到节点的那笔不影响节点之后按累计值退回。
func TestUndeliveredGrantsAreGivenBack(t *testing.T) {
	w := newWorld(t, config.RunModeStandard, apiKeyAccount(1, "one"))
	ctx, cancel := context.WithCancel(context.Background())
	w.sel.env.ConfigVersion = func(context.Context, int64) (string, error) {
		cancel() // 从节点在主节点做回复的时候放弃了
		return "cfg-v1", nil
	}
	_, err := w.sel.Select(ctx, testNode, responsesRequest("r1", 1, "sk-a"))
	require.Error(t, err)
	require.Zero(t, w.leases.ReservedBalance(3), "the grant the node never saw is given back")
	require.Equal(t, int64(0), w.slots.held.Load())
	require.Equal(t, int64(0), w.slots.accounts.Load(), "the account slot nobody will use is released")
}

func TestNoQuotaIsLockedWhenTheReplyCannotBeBuilt(t *testing.T) {
	w := newWorld(t, config.RunModeStandard, apiKeyAccount(1, "one"))
	_, err := w.sel.Select(context.Background(), testNode+1, responsesRequest("r1", 1, "sk-a"))
	require.Error(t, err, "this node has no encryption key")
	require.Zero(t, w.leases.ReservedBalance(3))
	require.Equal(t, int64(0), w.slots.held.Load())
}

func TestUngrantKeepsTheNodesCumulativeReturnsWorking(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeStandard, apiKeyAccount(1, "one"))
	resp, err := w.sel.Select(ctx, testNode, responsesRequest("r1", 1, "sk-a"))
	require.NoError(t, err)
	first := resp.GetSelection().GetGrants()[0]
	require.Equal(t, master.ToMicros(5), w.leases.ReservedBalance(3))

	// 同一份租约上又给了一笔，但没送到节点：收回后回到节点知道的数。
	extra, err := w.quotas.Acquire(ctx, master.AcquireRequest{UserID: 3, NodeID: testNode, Need: 1, Wants: []master.QuotaWant{{
		Scope: master.LeaseScope{Dimension: service.QuotaDimBalance}, Headroom: 10, NodeUnused: first.GetGranted(),
	}}})
	require.NoError(t, err)
	require.Equal(t, first.GetLeaseId(), extra[0].LeaseID)
	w.sel.ungrant(3, testNode, []*relayv1.QuotaGrant{extra[0].Proto(3)})
	require.Equal(t, first.GetGranted(), w.leases.ReservedBalance(3))

	// 节点按它知道的累计值退回 1：照常生效。
	_, returned, err := w.quotas.ApplyReturn(ctx, testNode, &relayv1.LeaseReturn{LeaseId: first.GetLeaseId(), UserId: 3, ReturnedTotal: master.ToMicros(1)})
	require.NoError(t, err)
	require.Equal(t, master.ToMicros(1), returned)
	require.Equal(t, first.GetGranted()-master.ToMicros(1), w.leases.ReservedBalance(3))
}

// 节点报告的"手里还有"不能超过主节点记着的、锁在这台上的：锁在别的节点上的钱不能借此绕过余额预检。
func TestNodeCannotClaimBalanceLockedElsewhere(t *testing.T) {
	ctx := context.Background()
	w := newWorldWithBalance(t, config.RunModeStandard, 0.05, apiKeyAccount(1, "one"))
	w.sel.deps.Billing.SetRelayReservedBalanceReader(w.quotas)
	balance := master.LeaseScope{Dimension: service.QuotaDimBalance}
	_, err := w.quotas.Acquire(ctx, master.AcquireRequest{UserID: 3, NodeID: testNode + 1, Need: 1, Wants: []master.QuotaWant{{Scope: balance, Headroom: 0.05}}})
	require.NoError(t, err)

	req := responsesRequest("r1", 1, "sk-a")
	req.HeldQuota = []*relayv1.HeldQuota{{Scope: &relayv1.QuotaScope{Dimension: service.QuotaDimBalance}, Unused: master.ToMicros(0.05)}}
	resp, err := w.sel.Select(ctx, testNode, req)
	require.NoError(t, err)
	require.Equal(t, int32(403), resp.GetRejection().GetStatus(), "the money is locked on another node")
	require.Equal(t, int64(0), w.slots.held.Load())
}

// 超过占用上限的长请求：选号记录已被清理，释放时按凭证记响应归属，续链照常可用；伪造的凭证不认。
func TestLongRequestStillRecordsTheResponseOwner(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeSimple, apiKeyAccount(1, "one"))
	resp, err := w.sel.Select(ctx, testNode, responsesRequest("r1", 1, "sk-a"))
	require.NoError(t, err)
	sel := resp.GetSelection()
	later := time.Now().Add(holdLimit + time.Minute)
	w.sel.now = func() time.Time { return later }
	w.sel.reap()
	w.sel.now = time.Now

	// 伪造：换一张别的选号的凭证。
	other, err := w.sel.Select(ctx, testNode, responsesRequest("r2", 1, "sk-a"))
	require.NoError(t, err)
	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), ResponseIds: []string{"resp_forged"}, Voucher: other.GetSelection().GetVoucher()})
	w.sel.release(testNode+1, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), ResponseIds: []string{"resp_wrong_node"}, Voucher: sel.GetVoucher()})
	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), RequestDone: true, ResponseIds: []string{"resp_long"}, Voucher: sel.GetVoucher()})

	owned, err := w.sel.deps.Gateway.ValidateOpenAIHTTPResponseOwner(ctx, 5, "resp_long", 3, 11)
	require.NoError(t, err)
	require.True(t, owned)
	for _, id := range []string{"resp_forged", "resp_wrong_node"} {
		owned, _ = w.sel.deps.Gateway.ValidateOpenAIHTTPResponseOwner(ctx, 5, id, 3, 11)
		require.False(t, owned, id)
	}
}

// 上游错误决策：只认这台节点正在用的账号，用主节点选号记录里的账号，状态记在主节点上。
func TestUpstreamErrorDecisionOnTheMaster(t *testing.T) {
	ctx := context.Background()
	oauth := apiKeyAccount(1, "oauth")
	oauth.Type = service.AccountTypeOAuth
	oauth.Credentials = map[string]any{"access_token": "SECRET-at"}
	w := newWorld(t, config.RunModeSimple, oauth)

	ask := func(nodeID int64, selectionID string, accountID int64, kind relayv1.UpstreamErrorKind) (*relayv1.UpstreamErrorResponse, error) {
		return w.sel.UpstreamError(ctx, nodeID, &relayv1.UpstreamErrorRequest{Kind: kind, AccountId: accountID, SelectionId: selectionID, StatusCode: 429})
	}
	_, err := ask(testNode, "", 1, relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_RESPONSE)
	require.ErrorIs(t, err, master.ErrSelectionNotFound, "no live selection uses this account")

	resp, err := w.sel.Select(ctx, testNode, responsesRequest("r1", 1, "sk-a"))
	require.NoError(t, err)
	selID := resp.GetSelection().GetSelectionId()

	_, err = ask(testNode+1, "", 1, relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_RESPONSE)
	require.ErrorIs(t, err, master.ErrSelectionNotFound, "another node cannot touch this account")
	_, err = ask(testNode, selID, 2, relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_RESPONSE)
	require.ErrorIs(t, err, master.ErrSelectionNotFound, "the selection chose a different account")

	// OAuth 瞬时 429：第一次打开同账号重试窗口（记在主节点上），之后问重试时拿到窗口截止时间。
	out, err := ask(testNode, selID, 1, relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_RESPONSE)
	require.NoError(t, err)
	require.False(t, out.GetShouldDisable())
	out, err = ask(testNode, "", 1, relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_OAUTH429_RETRY)
	require.NoError(t, err)
	require.True(t, out.GetRetrySameAccount())
	require.InDelta(t, time.Now().Add(2*time.Minute).UnixMilli(), out.GetRetryDeadlineUnixMs(), float64(5*time.Second/time.Millisecond))

	// 只走限流服务的判定（Anthropic 直通的非 JSON 响应）：同样只对这台节点在用的账号。
	_, err = ask(testNode, selID, 1, relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_RATE_LIMIT)
	require.NoError(t, err)
	_, err = ask(testNode+1, "", 1, relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_RATE_LIMIT)
	require.ErrorIs(t, err, master.ErrSelectionNotFound)

	_, err = w.sel.UpstreamError(ctx, testNode, &relayv1.UpstreamErrorRequest{AccountId: 1})
	require.Error(t, err, "the kind is required")

	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: selID, RequestDone: true})
	w.waitReleased(t)
	_, err = ask(testNode, selID, 1, relayv1.UpstreamErrorKind_UPSTREAM_ERROR_KIND_RESPONSE)
	require.ErrorIs(t, err, master.ErrSelectionNotFound, "decisions come before the release")
}

// 凭证的选号上下文带着入账要恢复的值：兜底事实、贡献房间路由、渠道映射、配额平台、订阅。
func TestVoucherCarriesTheSelectionContext(t *testing.T) {
	override := 0.7
	account := &service.Account{ID: 9, ContributionRouteSource: service.ContributionRouteSourceRoom, ContributionRoomID: 44, ContributionRateMultiplierOverride: &override}
	ctx := service.WithFallbackPoolTrace(context.Background(), service.FallbackPoolTrace{SourceGroupID: 20, SourceGroupName: "free", TargetGroupID: 29, TargetGroupName: "fallback"})
	record := &requestRecord{pricingAt: time.UnixMilli(1_800_000_000_000)}
	sel := &selectionRecord{request: record, quota: service.QuotaRequest{Platform: "openai"}}
	c := selectionContext(handler.OpenAISelectOutcome{Ctx: ctx, Account: account}, sel,
		service.ChannelMappingResult{ChannelID: 3, MappedModel: "gpt-5-mini", BillingModelSource: "channel_mapped"}, &service.UserSubscription{ID: 77})
	require.Equal(t, int64(1_800_000_000_000), c.GetPricingAtUnixMs())
	require.Equal(t, "openai", c.GetQuotaPlatform())
	require.Equal(t, int64(77), c.GetSubscriptionId())
	require.Equal(t, int64(3), c.GetChannelId())
	require.Equal(t, "gpt-5-mini", c.GetChannelMappedModel())
	require.Equal(t, "channel_mapped", c.GetBillingModelSource())
	require.Equal(t, int64(20), c.GetFallbackSourceGroupId())
	require.Equal(t, "fallback", c.GetFallbackTargetGroupName())
	require.Equal(t, service.ContributionRouteSourceRoom, c.GetContributionRouteSource())
	require.Equal(t, int64(44), c.GetContributionRoomId())
	require.True(t, c.GetHasContributionRateMultiplierOverride())
	require.InDelta(t, 0.7, c.GetContributionRateMultiplierOverride(), 1e-12)

	plain := selectionContext(handler.OpenAISelectOutcome{Ctx: context.Background(), Account: &service.Account{ID: 9}}, sel, service.ChannelMappingResult{}, nil)
	require.Zero(t, plain.GetFallbackSourceGroupId(), "no fallback, no trace")
	require.False(t, plain.GetHasContributionRateMultiplierOverride())
}

type recordedReport struct {
	kind    string
	account int64
	err     error
	extra   any
}

type recordingReporter struct {
	mu      sync.Mutex
	reports []recordedReport
}

func (r *recordingReporter) add(rep recordedReport) {
	r.mu.Lock()
	r.reports = append(r.reports, rep)
	r.mu.Unlock()
}

func (r *recordingReporter) ReportScheduleResult(account *service.Account, model string, success bool, firstTokenMs *int, _ int64, _ string, observedErr error) {
	r.add(recordedReport{kind: "result", account: account.ID, err: observedErr, extra: []any{model, success, firstTokenMs}})
}
func (r *recordingReporter) ObserveHealthFailure(_ context.Context, account *service.Account, observedErr error) {
	r.add(recordedReport{kind: "health", account: account.ID, err: observedErr})
}
func (r *recordingReporter) RecordAccountSwitch() { r.add(recordedReport{kind: "switch"}) }
func (r *recordingReporter) UpdateCodexUsageSnapshot(_ context.Context, accountID int64, snapshot *service.OpenAICodexUsageSnapshot) {
	r.add(recordedReport{kind: "codex", account: accountID, extra: snapshot})
}
func (r *recordingReporter) TempUnscheduleTransportError(_ context.Context, account *service.Account, safeErr string) {
	r.add(recordedReport{kind: "transport", account: account.ID, extra: safeErr})
}
func (r *recordingReporter) OllamaCloudUsageActivity(account *service.Account) {
	r.add(recordedReport{kind: "ollama", account: account.ID})
}
func (r *recordingReporter) UpdateSessionWindow(_ context.Context, account *service.Account, headers http.Header) {
	r.add(recordedReport{kind: "session_window", account: account.ID, extra: headers})
}

// 账号事件：只认这台节点正在用或刚用过（10 分钟内）的账号，用主节点记录里的账号对象执行。
func TestAccountEventsOnTheMaster(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeSimple, apiKeyAccount(1, "one"))
	rec := &recordingReporter{}
	w.sel.localReporter = func() service.OpenAIAccountReporter { return rec }

	result := func(accountID int64) *relayv1.AccountEvent {
		return &relayv1.AccountEvent{AccountId: accountID, Kind: &relayv1.AccountEvent_ScheduleResult{ScheduleResult: &relayv1.ScheduleResultEvent{
			Model: "gpt-5", Success: false, HasFirstTokenMs: true, FirstTokenMs: 120,
			Failure: &relayv1.HealthFailureFacts{Eligible: true, StatusCode: 502, Body: []byte("bad gateway")},
		}}}
	}
	w.sel.applyAccountEvent(testNode, result(1))
	require.Empty(t, rec.reports, "the node is not using this account")

	resp, err := w.sel.Select(ctx, testNode, responsesRequest("r1", 1, "sk-a"))
	require.NoError(t, err)
	w.sel.applyAccountEvent(testNode, result(1))
	w.sel.applyAccountEvent(testNode+1, result(1))
	require.Len(t, rec.reports, 1, "only the node that selected the account")
	got := rec.reports[0]
	require.Equal(t, "result", got.kind)
	var failover *service.UpstreamFailoverError
	require.ErrorAs(t, got.err, &failover)
	require.Equal(t, 502, failover.StatusCode)
	extra, ok := got.extra.([]any)
	require.True(t, ok)
	firstToken, ok := extra[2].(*int)
	require.True(t, ok)
	require.Equal(t, 120, *firstToken)

	// 调度结果在放槽之后才到：刚用过的账号照样认。
	w.sel.release(testNode, &relayv1.SelectionRelease{SelectionId: resp.GetSelection().GetSelectionId(), RequestDone: true})
	w.sel.applyAccountEvent(testNode, &relayv1.AccountEvent{AccountId: 1, Kind: &relayv1.AccountEvent_TransportError{TransportError: &relayv1.TransportErrorEvent{Message: "dial tcp: refused"}}})
	w.sel.applyAccountEvent(testNode, &relayv1.AccountEvent{AccountId: 1, Kind: &relayv1.AccountEvent_CodexUsage{CodexUsage: &relayv1.CodexUsageEvent{SnapshotJson: []byte(`{"primary_used_percent":40,"updated_at":"2026-09-27T01:02:03Z"}`)}}})
	w.sel.applyAccountEvent(testNode, &relayv1.AccountEvent{AccountId: 1, Kind: &relayv1.AccountEvent_HealthFailure{HealthFailure: &relayv1.HealthFailureEvent{Failure: &relayv1.HealthFailureFacts{}}}})
	w.sel.applyAccountEvent(testNode, &relayv1.AccountEvent{Kind: &relayv1.AccountEvent_AccountSwitch{AccountSwitch: &relayv1.AccountSwitchEvent{}}})
	w.sel.applyAccountEvent(testNode, &relayv1.AccountEvent{AccountId: 1, Kind: &relayv1.AccountEvent_SessionWindow{SessionWindow: &relayv1.SessionWindowEvent{
		Headers: []*relayv1.HeaderValues{
			{Name: "Anthropic-Ratelimit-Unified-5h-Status", Values: []string{"allowed"}},
			{Name: "Set-Cookie", Values: []string{"not-a-window-header"}},
		},
	}}})
	kinds := []string{}
	for _, r := range rec.reports[1:] {
		kinds = append(kinds, r.kind)
	}
	require.Equal(t, []string{"transport", "codex", "switch", "session_window"}, kinds, "a non-eligible health failure changes nothing")
	snapshot, ok := rec.reports[2].extra.(*service.OpenAICodexUsageSnapshot)
	require.True(t, ok)
	require.Equal(t, "2026-09-27T01:02:03Z", snapshot.UpdatedAt)
	window, ok := rec.reports[4].extra.(http.Header)
	require.True(t, ok)
	require.Equal(t, "allowed", window.Get("anthropic-ratelimit-unified-5h-status"))
	require.Empty(t, window.Get("Set-Cookie"), "only the session window headers are used")

	later := time.Now().Add(recentUseTTL + time.Minute)
	w.sel.now = func() time.Time { return later }
	w.sel.applyAccountEvent(testNode, result(1))
	require.Len(t, rec.reports, 5, "long after the release the account is no longer this node's")
	w.sel.now = time.Now
}

func chatRequest(requestID string, attempt uint32, key string) *relayv1.SelectRequest {
	req := responsesRequest(requestID, attempt, key)
	req.Endpoint = relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_CHAT
	req.Path = "/v1/chat/completions"
	return req
}

// Chat Completions：与本地 ChatCompletions 一样要求账号支持 Chat（不看 Responses 能力）、
// 没有续链和生图（从节点报了也不认）、cyber 屏蔽在占用户槽之前查（Responses 在计费检查之后）。
func TestSelectChatCompletions(t *testing.T) {
	ctx := context.Background()
	chatOnly := apiKeyAccount(1, "chat-only")
	chatOnly.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}
	w := newWorld(t, config.RunModeSimple, chatOnly)

	img := responsesRequest("r0", 1, "sk-a")
	img.ImageIntent = true
	resp, err := w.sel.Select(ctx, testNode, img)
	require.NoError(t, err)
	require.NotNil(t, resp.GetRejection(), "an image request on Responses needs an account that supports Responses")
	require.Equal(t, int64(0), w.slots.held.Load())

	req := chatRequest("r1", 1, "sk-a")
	req.ImageIntent, req.PreviousResponseId, req.LegacyCompact = true, "resp_not_mine", true
	resp, err = w.sel.Select(ctx, testNode, req)
	require.NoError(t, err)
	sel := resp.GetSelection()
	require.NotNil(t, sel, "chat ignores Responses-only fields: %+v", resp.GetRejection())
	require.Equal(t, int64(1), sel.GetAccount().GetId())
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), RequestDone: true})
	w.waitReleased(t)

	// cyber 屏蔽命中：Chat 不占用户槽就拒绝；Responses 先占槽、过计费检查再拒绝（本地顺序）。
	var slotsWhenChecked []int64
	w.sel.findCyberBlocked = func(context.Context, service.CyberSessionLookup) string {
		slotsWhenChecked = append(slotsWhenChecked, w.slots.held.Load())
		return "cyber-key"
	}
	blocked := chatRequest("r2", 1, "sk-a")
	blocked.Cyber = &relayv1.CyberSessionLookup{ExplicitKey: "k"}
	resp, err = w.sel.Select(ctx, testNode, blocked)
	require.NoError(t, err)
	require.Equal(t, "cyber-key", resp.GetRejection().GetCyberBlockKey())
	require.Equal(t, int32(403), resp.GetRejection().GetStatus())
	blockedResponses := responsesRequest("r3", 1, "sk-a")
	blockedResponses.Cyber = &relayv1.CyberSessionLookup{ExplicitKey: "k"}
	_, err = w.sel.Select(ctx, testNode, blockedResponses)
	require.NoError(t, err)
	require.Equal(t, []int64{0, 1}, slotsWhenChecked, "chat checks before taking the user slot, responses after")
	require.Equal(t, int64(0), w.slots.held.Load())
}

func messagesRequest(requestID string, attempt uint32, key, model string) *relayv1.SelectRequest {
	req := responsesRequest(requestID, attempt, key)
	req.Endpoint = relayv1.SelectEndpoint_SELECT_ENDPOINT_OPENAI_MESSAGES
	req.Path = "/v1/messages"
	req.Model = model
	return req
}

// OpenAI 分组的 /v1/messages：分组要允许派发；按派发映射模型选号；计费和选不出账号的错误按 Anthropic 格式。
func TestSelectMessages(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeSimple, apiKeyAccount(1, "one"))

	resp, err := w.sel.Select(ctx, testNode, messagesRequest("r1", 1, "sk-a", "claude-sonnet-4-5"))
	require.NoError(t, err)
	rej := resp.GetRejection()
	require.Equal(t, int32(403), rej.GetStatus())
	require.Equal(t, "permission_error", rej.GetErrorType())
	require.True(t, rej.GetAnthropicFormat())
	require.Equal(t, int64(0), w.slots.held.Load(), "denied before any slot is taken")

	w.keys.keys["sk-a"].Group.AllowMessagesDispatch = true
	req := messagesRequest("r2", 1, "sk-a", "claude-sonnet-4-5")
	req.PreviousResponseId, req.ImageIntent = "resp_not_mine", true
	resp, err = w.sel.Select(ctx, testNode, req)
	require.NoError(t, err)
	sel := resp.GetSelection()
	require.NotNil(t, sel, "messages ignores Responses-only fields: %+v", resp.GetRejection())
	routing := handler.OpenAIMessagesRoutingModel(w.keys.keys["sk-a"], "claude-sonnet-4-5")
	require.NotEqual(t, "claude-sonnet-4-5", routing, "the group's dispatch mapping picks a GPT model")
	require.Equal(t, routing, sel.GetForwardModel())
	v, err := sign.VerifyVoucher(sel.GetVoucher(), w.pub, testNode, time.Now())
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-4-5", v.GetRequestedModel())
	require.Contains(t, v.GetAllowedBillingModels(), routing)
	w.sel.Release(testNode, &relayv1.SelectionRelease{SelectionId: sel.GetSelectionId(), RequestDone: true})
	w.waitReleased(t)

	// 选不出账号（排除了唯一的账号、还没排除过别的）：Anthropic 格式。
	none := newWorld(t, config.RunModeSimple)
	none.keys.keys["sk-a"].Group.AllowMessagesDispatch = true
	resp, err = none.sel.Select(ctx, testNode, messagesRequest("r3", 1, "sk-a", "claude-sonnet-4-5"))
	require.NoError(t, err)
	require.True(t, resp.GetRejection().GetAnthropicFormat(), "%+v", resp.GetRejection())

	// 计费拒绝：Anthropic 格式；用户并发槽等其他准入错误仍是 OpenAI 格式（与本地一致）。
	broke := newWorldWithBalance(t, config.RunModeStandard, 0, apiKeyAccount(1, "one"))
	broke.keys.keys["sk-a"].Group.AllowMessagesDispatch = true
	resp, err = broke.sel.Select(ctx, testNode, messagesRequest("r4", 1, "sk-a", "claude-sonnet-4-5"))
	require.NoError(t, err)
	require.Equal(t, int32(403), resp.GetRejection().GetStatus())
	require.True(t, resp.GetRejection().GetAnthropicFormat())
	resp, err = broke.sel.Select(ctx, testNode, responsesRequest("r5", 1, "sk-a"))
	require.NoError(t, err)
	require.False(t, resp.GetRejection().GetAnthropicFormat(), "responses billing errors stay OpenAI-shaped")
}

// 凭证允许的计费模型：请求模型、渠道映射后的、账号映射后发给上游的（去重）。
func TestAllowedBillingModels(t *testing.T) {
	mapped := &service.Account{Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5": "gpt-5-2025"}}}
	require.Equal(t, []string{"gpt-5-alias", "gpt-5", "gpt-5-2025"}, allowedBillingModels("gpt-5-alias", "gpt-5", mapped))
	require.Equal(t, []string{"gpt-5"}, allowedBillingModels("gpt-5", "gpt-5", &service.Account{}))
}

// memIdentity 是主节点的身份缓存（service.IdentityCache）。
type memIdentity struct {
	mu           sync.Mutex
	fingerprints map[int64]*service.Fingerprint
	masked       map[int64]string
	maskedSets   int
}

func (m *memIdentity) GetFingerprint(_ context.Context, id int64) (*service.Fingerprint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if fp, ok := m.fingerprints[id]; ok {
		copied := *fp
		return &copied, nil
	}
	return nil, errors.New("no fingerprint")
}

func (m *memIdentity) SetFingerprint(_ context.Context, id int64, fp *service.Fingerprint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := *fp
	m.fingerprints[id] = &copied
	return nil
}

func (m *memIdentity) GetMaskedSessionID(_ context.Context, id int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.masked[id], nil
}

func (m *memIdentity) SetMaskedSessionID(_ context.Context, id int64, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.masked[id] = sessionID
	m.maskedSets++
	return nil
}

// seededTokens 是主节点的 token 缓存：Vertex 服务账号的 token 已经换好（测试不去访问 Google）。
type seededTokens struct{}

func (seededTokens) GetAccessToken(_ context.Context, key string) (string, error) {
	if strings.HasPrefix(key, "vertex:service_account:") {
		return "SECRET-vertex-token", nil
	}
	return "", nil
}
func (seededTokens) SetAccessToken(context.Context, string, string, time.Duration) error {
	return nil
}
func (seededTokens) DeleteAccessToken(context.Context, string) error { return nil }
func (seededTokens) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}
func (seededTokens) ReleaseRefreshLock(context.Context, string) error { return nil }

// memGroups 是主节点的分组仓储（解析兜底分组用）。
type memGroups struct {
	service.GroupRepository
	byID map[int64]*service.Group
}

func (m *memGroups) GetByID(ctx context.Context, id int64) (*service.Group, error) {
	return m.GetByIDLite(ctx, id)
}

func (m *memGroups) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	if g, ok := m.byID[id]; ok {
		copied := *g
		return &copied, nil
	}
	return nil, errors.New("no group")
}
