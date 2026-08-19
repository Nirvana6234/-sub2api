package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsOpenAIPermanentCapability403(t *testing.T) {
	tests := []struct {
		name        string
		upstreamMsg string
		upstreamBody []byte
		expected    bool
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
}
