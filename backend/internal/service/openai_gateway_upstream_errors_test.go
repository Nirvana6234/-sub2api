package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsOpenAIPermanentCapability403(t *testing.T) {
	tests := []struct {
		name         string
		upstreamMsg  string
		upstreamBody []byte
		expected     bool
	}{
		{
			name:        "production_capability_disabled_message_in_upstream_msg",
			upstreamMsg: "Access forbidden (403): Image generation is not enabled for this group",
			expected:    true,
		},
		{
			name:         "capability_disabled_message_in_body_only",
			upstreamBody: []byte(`{"error":{"message":"Image generation is not enabled for this group"}}`),
			expected:     true,
		},
		{
			name:         "insufficient_quota_error_code",
			upstreamBody: []byte(`{"error":{"code":"insufficient_quota","message":"You exceeded your current quota"}}`),
			expected:     true,
		},
		{
			name:        "account_suspended",
			upstreamMsg: "Access forbidden: account is suspended",
			expected:    true,
		},
		{
			name:        "account_deactivated",
			upstreamMsg: "Access forbidden: account has been deactivated",
			expected:    true,
		},
		{
			name:        "case_insensitive_match",
			upstreamMsg: "IS NOT ENABLED FOR THIS GROUP",
			expected:    true,
		},
		{
			name:        "generic_403_does_not_match",
			upstreamMsg: "Access forbidden",
			expected:    false,
		},
		{
			name:         "generic_403_body_does_not_match",
			upstreamBody: []byte(`{"error":{"message":"You do not have access to this resource"}}`),
			expected:     false,
		},
		{
			name:        "empty_message_and_body",
			upstreamMsg: "",
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, isOpenAIPermanentCapability403(tt.upstreamMsg, tt.upstreamBody))
		})
	}
}

func TestIsOpenAIDailyUsageLimitError(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		upstreamMsg  string
		upstreamBody []byte
		expected     bool
	}{
		{
			name:         "explicit_daily_usage_message",
			statusCode:   http.StatusForbidden,
			upstreamBody: []byte(`{"error":{"message":"daily usage limit exceeded"}}`),
			expected:     true,
		},
		{
			name:         "insufficient_quota_with_daily_message",
			statusCode:   http.StatusForbidden,
			upstreamBody: []byte(`{"error":{"code":"insufficient_quota","message":"daily usage limit exceeded"}}`),
			expected:     true,
		},
		{
			name:        "same_message_wrong_status",
			statusCode:  http.StatusBadRequest,
			upstreamMsg: "daily usage limit exceeded",
			expected:    false,
		},
		{
			name:         "echoed_prompt_does_not_match_structured_json",
			statusCode:   http.StatusForbidden,
			upstreamBody: []byte(`{"error":{"code":"forbidden","message":"Access forbidden"},"prompt":"daily usage limit exceeded"}`),
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, isOpenAIDailyUsageLimitError(tt.statusCode, tt.upstreamMsg, tt.upstreamBody))
		})
	}
}

func TestNewOpenAIUpstreamFailoverError_PermanentCapability403ForcesNoSameAccountRetry(t *testing.T) {
	err := newOpenAIUpstreamFailoverError(
		http.StatusForbidden,
		http.Header{},
		[]byte(`{"error":{"message":"Image generation is not enabled for this group"}}`),
		"Image generation is not enabled for this group",
		true, // caller computed retryableOnSameAccount=true (e.g. pool mode + 403 in default retryable list)
	)

	require.NotNil(t, err)
	require.False(t, err.RetryableOnSameAccount, "a deterministic capability 403 must not be retried on the same account")
}

func TestNewOpenAIUpstreamFailoverError_TransientForbiddenKeepsCallerDecision(t *testing.T) {
	err := newOpenAIUpstreamFailoverError(
		http.StatusForbidden,
		http.Header{},
		[]byte(`{"error":{"message":"Access forbidden"}}`),
		"Access forbidden",
		true,
	)

	require.NotNil(t, err)
	require.True(t, err.RetryableOnSameAccount, "an unclassified 403 must keep the caller's retry decision unchanged")
}

func TestNewOpenAIUpstreamFailoverError_DailyUsageLimitIsRequestScoped(t *testing.T) {
	err := newOpenAIUpstreamFailoverError(
		http.StatusForbidden,
		http.Header{},
		[]byte(`{"error":{"message":"daily usage limit exceeded"}}`),
		"daily usage limit exceeded",
		false,
	)

	require.NotNil(t, err)
	require.True(t, err.RequestScopedTransient)
	require.Equal(t, GatewayFailureScopeRequest, err.Scope)
	require.Equal(t, OpenAIDailyUsageLimitReason, err.Reason)
	require.Equal(t, http.StatusServiceUnavailable, err.ClientStatusCode)
	require.NotEmpty(t, err.ClientMessage)
}

func TestOpenAIPassthroughDailyUsageLimitAlwaysFailsOver(t *testing.T) {
	body := []byte(`{"error":{"message":"daily usage limit exceeded"}}`)
	for _, accountType := range []string{
		AccountTypeAPIKey,
		AccountTypeUpstream,
		AccountTypeOAuth,
		AccountTypeSetupToken,
	} {
		t.Run(accountType, func(t *testing.T) {
			account := &Account{Platform: PlatformOpenAI, Type: accountType}
			require.True(t, shouldFailoverOpenAIPassthroughResponse(account, http.StatusForbidden, body))
		})
	}
}

func TestNewOpenAIAccountFailoverError_DailyUsageLimitDiffersByAccountType(t *testing.T) {
	svc := &OpenAIGatewayService{}
	body := []byte(`{"error":{"message":"daily usage limit exceeded"}}`)

	for _, tc := range []struct {
		name            string
		accountType     string
		credentials     map[string]any
		wantSameAccount bool
	}{
		{name: "upstream_retries_same_account", accountType: AccountTypeUpstream, wantSameAccount: true},
		{name: "api_key_with_custom_base_url_retries_same_account", accountType: AccountTypeAPIKey, credentials: map[string]any{"base_url": "https://relay.example/v1"}, wantSameAccount: true},
		{name: "plain_api_key_retries_same_account", accountType: AccountTypeAPIKey, wantSameAccount: true},
		{name: "oauth_skips_to_next_account", accountType: AccountTypeOAuth, wantSameAccount: false},
		{name: "setup_token_skips_to_next_account", accountType: AccountTypeSetupToken, wantSameAccount: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 100, Platform: PlatformOpenAI, Type: tc.accountType, Credentials: tc.credentials}
			err := svc.newOpenAIAccountFailoverError(
				account,
				http.StatusForbidden,
				http.Header{},
				body,
				"daily usage limit exceeded",
				false,
				false,
			)

			require.Equal(t, tc.wantSameAccount, err.RetryableOnSameAccount)
			require.True(t, err.RequestScopedTransient)
			require.Equal(t, GatewayFailureScopeRequest, err.Scope)
			require.Equal(t, OpenAIDailyUsageLimitReason, err.Reason)
			require.Equal(t, http.StatusServiceUnavailable, err.ClientStatusCode)
		})
	}
}

func TestOpenAIRetryableOnSameAccount(t *testing.T) {
	poolAccount := &Account{
		ID:          1,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"pool_mode": true},
	}
	nonPoolAccount := &Account{
		ID:       2,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
	}
	upstreamRelayAccount := &Account{
		ID:          5,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://relay.example/v1"},
	}

	// Pool-mode account, ordinary 403 (not classified as permanent) -> retryable, matching
	// today's behavior (defaultPoolModeRetryableStatusCodes includes 403).
	require.True(t, openAIRetryableOnSameAccount(poolAccount, http.StatusForbidden, false, "Access forbidden", nil))

	// Pool-mode account, permanent-capability 403 -> must not retry on the same account.
	require.False(t, openAIRetryableOnSameAccount(poolAccount, http.StatusForbidden, false,
		"Image generation is not enabled for this group", nil))

	// shouldDisable=true always wins regardless of classification.
	require.False(t, openAIRetryableOnSameAccount(poolAccount, http.StatusForbidden, true, "Access forbidden", nil))

	// Non-pool-mode account is never same-account-retryable, permanent or not.
	require.False(t, openAIRetryableOnSameAccount(nonPoolAccount, http.StatusForbidden, false, "Access forbidden", nil))

	require.True(t, openAIRetryableOnSameAccount(nonPoolAccount, http.StatusForbidden, false,
		"daily usage limit exceeded", nil), "OpenAI API-key accounts get bounded same-account retry")

	require.True(t, openAIRetryableOnSameAccount(upstreamRelayAccount, http.StatusForbidden, false,
		"daily usage limit exceeded", nil), "upstream relay accounts get bounded same-account retry")

	upstreamAccount := &Account{
		ID:       3,
		Platform: PlatformOpenAI,
		Type:     AccountTypeUpstream,
	}
	require.True(t, openAIRetryableOnSameAccount(upstreamAccount, http.StatusForbidden, false,
		"daily usage limit exceeded", nil))

	oauthAccount := &Account{
		ID:       4,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
	}
	require.False(t, openAIRetryableOnSameAccount(oauthAccount, http.StatusForbidden, false,
		"daily usage limit exceeded", nil), "real provider accounts skip to the next account")
}
