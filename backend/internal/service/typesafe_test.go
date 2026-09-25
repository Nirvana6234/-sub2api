package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// typeSafeChannelRepoStub 只实现渠道缓存加载要用的两个方法，其余方法调用即 panic。
type typeSafeChannelRepoStub struct {
	ChannelRepository
	channels  []Channel
	platforms map[int64]string
}

func (s *typeSafeChannelRepoStub) ListAll(context.Context) ([]Channel, error) {
	return s.channels, nil
}

func (s *typeSafeChannelRepoStub) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return s.platforms, nil
}

type typeSafeUpstreamStub struct {
	status  int
	header  http.Header
	body    string
	err     error
	request *http.Request
	sent    string
}

func (s *typeSafeUpstreamStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	s.request = req
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		s.sent = string(b)
	}
	if s.err != nil {
		return nil, s.err
	}
	header := s.header
	if header == nil {
		header = http.Header{"Content-Type": []string{"application/json"}}
	}
	return &http.Response{StatusCode: s.status, Header: header, Body: io.NopCloser(strings.NewReader(s.body))}, nil
}

func (s *typeSafeUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, accountConcurrency)
}

func newTypeSafeTestAccount() *Account {
	return &Account{
		ID: 7, Name: "ts", Platform: PlatformTypeSafe, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "ts-account-key", "base_url": "https://ts.example/v1/"},
	}
}

func newTypeSafeTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/systemone", nil)
	return c, w
}

const typeSafeRequestBody = `{"model":"jev-latest","state":{"conversation":[]},"questions":{"q":{"type":"noul","instructions":"x"}}}`

func TestForwardTypeSafeSystemOneSuccess(t *testing.T) {
	upstream := &typeSafeUpstreamStub{
		status: http.StatusOK,
		body:   `{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0.9}},"usage":{"input_tokens":1130,"output_tokens":3}}`,
		header: http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"req-1"}},
	}
	svc := &GatewayService{httpUpstream: upstream}
	c, w := newTypeSafeTestContext()

	result, err := svc.ForwardTypeSafeSystemOne(context.Background(), c, newTypeSafeTestAccount(), []byte(typeSafeRequestBody))

	require.NoError(t, err)
	require.Equal(t, "https://ts.example/v1/systemone", upstream.request.URL.String())
	require.Equal(t, "Bearer ts-account-key", upstream.request.Header.Get("Authorization"))
	require.Equal(t, typeSafeRequestBody, upstream.sent, "the body is forwarded byte for byte")
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, upstream.body, w.Body.String())
	require.Equal(t, 1130, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, "jev-latest", result.Model, "billing uses the requested model")
	require.Equal(t, "jev-1.13.0", result.UpstreamResponseModel)
	require.Equal(t, "req-1", result.RequestID)
}

// 客户端「固定版本下线就退回 jev-latest」靠的是 TypeSafe 的 400 原文。
func TestForwardTypeSafeSystemOnePassesRequestErrorsThrough(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity} {
		upstream := &typeSafeUpstreamStub{status: status, body: `{"detail":{"error_type":"invalid_request","message":"Unknown model: jev-9"}}`}
		svc := &GatewayService{httpUpstream: upstream}
		c, w := newTypeSafeTestContext()

		result, err := svc.ForwardTypeSafeSystemOne(context.Background(), c, newTypeSafeTestAccount(), []byte(typeSafeRequestBody))

		require.Nil(t, result)
		var clientErr *TypeSafeClientError
		require.True(t, errors.As(err, &clientErr), "status %d", status)
		require.Equal(t, status, w.Code)
		require.JSONEq(t, upstream.body, w.Body.String())
	}
}

func TestForwardTypeSafeSystemOneFailsOverOnAccountErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden, http.StatusTooManyRequests, 529, http.StatusInternalServerError} {
		upstream := &typeSafeUpstreamStub{status: status, body: `{"detail":"busy"}`, header: http.Header{"Retry-After": []string{"2"}}}
		svc := &GatewayService{httpUpstream: upstream}
		c, w := newTypeSafeTestContext()

		_, err := svc.ForwardTypeSafeSystemOne(context.Background(), c, newTypeSafeTestAccount(), []byte(typeSafeRequestBody))

		var failover *UpstreamFailoverError
		require.True(t, errors.As(err, &failover), "status %d", status)
		require.Equal(t, status, failover.StatusCode)
		require.Equal(t, "2", failover.ResponseHeaders.Get("Retry-After"))
		require.False(t, c.Writer.Written(), "nothing is written before the handler decides")
		require.Equal(t, http.StatusOK, w.Code)
	}

	upstream := &typeSafeUpstreamStub{err: errors.New("dial tcp: timeout")}
	svc := &GatewayService{httpUpstream: upstream}
	c, _ := newTypeSafeTestContext()
	_, err := svc.ForwardTypeSafeSystemOne(context.Background(), c, newTypeSafeTestAccount(), []byte(typeSafeRequestBody))
	var failover *UpstreamFailoverError
	require.True(t, errors.As(err, &failover))
}

func TestForwardTypeSafeSystemOneRejectsOtherAccounts(t *testing.T) {
	svc := &GatewayService{httpUpstream: &typeSafeUpstreamStub{status: http.StatusOK}}
	c, _ := newTypeSafeTestContext()
	account := newTypeSafeTestAccount()
	account.Platform = PlatformOpenAI

	_, err := svc.ForwardTypeSafeSystemOne(context.Background(), c, account, []byte(typeSafeRequestBody))

	require.Error(t, err)
}

func TestTypeSafeSystemOneURL(t *testing.T) {
	for base, want := range map[string]string{
		"https://api.typesafe.ai":     "https://api.typesafe.ai/v1/systemone",
		"https://api.typesafe.ai/":    "https://api.typesafe.ai/v1/systemone",
		"https://api.typesafe.ai/v1":  "https://api.typesafe.ai/v1/systemone",
		"https://api.typesafe.ai/v1/": "https://api.typesafe.ai/v1/systemone",
	} {
		require.Equal(t, want, typeSafeSystemOneURL(base), base)
	}
}

// 没配价格就不转发：只认分组或渠道的显式定价（按次和按 token 都算），不认全局价格表。
func TestHasTypeSafePricingRequiresExplicitPricing(t *testing.T) {
	price := 0.0005
	repo := &typeSafeChannelRepoStub{channels: []Channel{{
		ID: 1, Name: "jev", Status: StatusActive, GroupIDs: []int64{100},
		ModelPricing: []ChannelModelPricing{{
			Platform: PlatformTypeSafe, Models: []string{"jev-1.13.0"}, BillingMode: BillingModePerRequest,
			PerRequestPrice: &price,
		}},
	}}, platforms: map[int64]string{100: PlatformTypeSafe}}
	bs := NewBillingService(&config.Config{}, nil)
	resolver := NewModelPricingResolver(NewChannelService(repo, nil, nil, nil, nil), bs)
	svc := &GatewayService{resolver: resolver, billingService: bs}
	group := &Group{ID: 100, Platform: PlatformTypeSafe}
	apiKey := &APIKey{Group: group}
	ctx := context.Background()

	require.True(t, svc.HasTypeSafePricing(ctx, "jev-1.13.0", apiKey))
	require.False(t, svc.HasTypeSafePricing(ctx, "jev-latest", apiKey), "each name needs its own price")
	require.False(t, svc.HasTypeSafePricing(ctx, "jev-1.13.0", &APIKey{}), "no group, no channel pricing")
	require.False(t, svc.HasTypeSafePricing(ctx, "", apiKey))

	// 按次计费经 token 计价入口按一次收费。
	gid := group.ID
	resolved := resolver.Resolve(ctx, PricingInput{Model: "jev-1.13.0", GroupID: &gid, Group: group})
	cost, err := bs.CalculateTokenCostForRequest(TokenCostRequest{
		Ctx: ctx, Model: "jev-1.13.0", Group: group, Tokens: UsageTokens{InputTokens: 1130}, RateMultiplier: 2,
		Resolver: resolver, Resolved: resolved,
	})
	require.NoError(t, err)
	require.InDelta(t, 0.0005, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.001, cost.ActualCost, 1e-12)
}

func TestModelVendorPlatformRecognisesJev(t *testing.T) {
	require.Equal(t, PlatformTypeSafe, modelVendorPlatform("jev-1.13.0"))
	require.Equal(t, PlatformTypeSafe, modelVendorPlatform("jev-latest"))
	require.True(t, groupListsModel(PlatformTypeSafe, "jev-latest"))
	require.False(t, groupListsModel(PlatformTypeSafe, "gpt-5.5"))
}
