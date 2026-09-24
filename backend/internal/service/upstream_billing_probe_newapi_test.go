package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type contextAwareProbeBody struct {
	ctx    context.Context
	reader *strings.Reader
}

func (b *contextAwareProbeBody) Read(data []byte) (int, error) {
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	default:
		return b.reader.Read(data)
	}
}

func (b *contextAwareProbeBody) Close() error { return nil }

type newAPIProbeHTTPStub struct {
	pricingBody string
}

func (s newAPIProbeHTTPStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	status := http.StatusOK
	body := ""
	switch req.URL.Path {
	case "/v1/sub2api/billing":
		status = http.StatusNotFound
		body = `{"message":"not found"}`
	case "/v1/models":
		body = `{"data":[{"id":"gpt-5.4"},{"id":"gpt-5.6-sol"}]}`
	case "/api/pricing":
		body = s.pricingBody
		if body == "" {
			body = `{"success":true,"pricing_version":"v42","group_ratio":{"gpt-plus":0.12,"gpt-pro":0.2},"data":[{"model_name":"gpt-5.4","enable_groups":["gpt-plus","gpt-pro"]},{"model_name":"gpt-5.6-sol","enable_groups":["gpt-pro"]},{"model_name":"gpt-image-1","enable_groups":["gpt-plus"]}]}`
		}
	default:
		return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       &contextAwareProbeBody{ctx: req.Context(), reader: strings.NewReader(body)},
	}, nil
}

func (stub newAPIProbeHTTPStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return stub.Do(req, proxyURL, accountID, accountConcurrency)
}

var _ io.ReadCloser = (*contextAwareProbeBody)(nil)

func TestParseNewAPIGroupRatio(t *testing.T) {
	group, ratio, version, err := parseNewAPIGroupRatio(
		[]byte(`{"object":"list","data":[{"id":"gpt-5.4"},{"id":"gpt-5.6-sol"}]}`),
		[]byte(`{"success":true,"pricing_version":"v42","group_ratio":{"gpt-plus":0.12,"gpt-pro":0.2},"data":[{"model_name":"gpt-5.4","enable_groups":["gpt-plus","gpt-pro"]},{"model_name":"gpt-5.6-sol","enable_groups":["gpt-pro"]},{"model_name":"gpt-image-1","enable_groups":["gpt-plus"]}]}`),
	)
	require.NoError(t, err)
	require.Equal(t, "gpt-pro", group)
	require.Equal(t, 0.2, ratio)
	require.Equal(t, "v42", version)
}

func TestParseNewAPIGroupRatioRejectsUnknownGroup(t *testing.T) {
	_, _, _, err := parseNewAPIGroupRatio(
		[]byte(`{"data":[{"id":"unknown-model"}]}`),
		[]byte(`{"success":true,"group_ratio":{"gpt-pro":0.2},"data":[{"model_name":"gpt-5.4","enable_groups":["gpt-pro"]}]}`),
	)
	require.Error(t, err)
}

func TestParseNewAPIGroupRatioUsesExplicitGroupOverride(t *testing.T) {
	group, ratio, version, err := parseNewAPIGroupRatioWithOverride(
		[]byte(`{"data":[{"id":"gpt-5.4"},{"id":"gpt-5.6-sol"}]}`),
		[]byte(`{"success":true,"pricing_version":"v42","group_ratio":{"gpt-plus":0.12,"gpt-pro":0.2},"data":[{"model_name":"gpt-5.4","enable_groups":["gpt-plus","gpt-pro"]},{"model_name":"gpt-5.6-sol","enable_groups":["gpt-pro"]}]}`),
		"gpt-pro",
	)
	require.NoError(t, err)
	require.Equal(t, "gpt-pro", group)
	require.Equal(t, 0.2, ratio)
	require.Equal(t, "v42", version)
}

func TestParseNewAPIGroupRatioRejectsInvalidGroupOverride(t *testing.T) {
	_, _, _, err := parseNewAPIGroupRatioWithOverride(
		[]byte(`{"data":[{"id":"gpt-5.4"}]}`),
		[]byte(`{"success":true,"group_ratio":{"gpt-plus":0.12},"data":[{"model_name":"gpt-5.4","enable_groups":["gpt-plus"]}]}`),
		"gpt-pro",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "gpt-pro")
}

func TestUpstreamBillingProbeNewAPIFallbackReadsResponseBeforeCancel(t *testing.T) {
	account := &Account{
		ID:       108,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Status:   StatusActive,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://newapi.example/v1",
		},
		Extra: map[string]any{UpstreamBillingProbeEnabledExtraKey: true},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	svc := newUpstreamBillingProbeTestService(repo, newAPIProbeHTTPStub{}, &upstreamBillingProbeSettingRepo{})

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, "newapi.group_billing", snapshot.Data["object"])
	require.Equal(t, "gpt-pro", snapshot.Data["newapi_group"])
	require.Equal(t, 0.2, snapshot.Data["resolved_rate_multiplier"])
}

func TestUpstreamBillingProbeNewAPIFallbackUsesAccountGroupOverride(t *testing.T) {
	account := &Account{
		ID:       76,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Status:   StatusActive,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://newapi.example/v1",
		},
		Extra: map[string]any{
			UpstreamBillingProbeEnabledExtraKey: true,
			UpstreamBillingNewAPIGroupExtraKey:  "gpt-plus",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := newAPIProbeHTTPStub{
		pricingBody: `{"success":true,"pricing_version":"v43","group_ratio":{"gpt-plus":0.12,"gpt-pro":0.2},"data":[{"model_name":"gpt-5.4","enable_groups":["gpt-plus","gpt-pro"]},{"model_name":"gpt-5.6-sol","enable_groups":["gpt-plus","gpt-pro"]}]}`,
	}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, "gpt-plus", snapshot.Data["newapi_group"])
	require.Equal(t, 0.12, snapshot.Data["resolved_rate_multiplier"])
}
