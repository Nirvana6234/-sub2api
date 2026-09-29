package accountcodec_test

import (
	"bytes"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/relay/accountcodec"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sealbox"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func sampleAccount() *service.Account {
	rate := 0.8
	parent := int64(3)
	owner := int64(77)
	return &service.Account{
		ID: 42, Name: "acct", Platform: "openai", Type: "oauth", Concurrency: 5, Priority: 2,
		RateMultiplier: &rate, Status: "active", ParentAccountID: &parent, QuotaDimension: "spark",
		GroupIDs: []int64{1, 2},
		Credentials: map[string]any{
			"base_url":           "https://api.example.com",
			"model_mapping":      map[string]any{"gpt-5": "gpt-5-2026"},
			"chatgpt_account_id": "acc-123",
			"access_token":       "SECRET-ACCESS-OLD",
			"api_key":            "SECRET-API-KEY",
			"refresh_token":      "SECRET-REFRESH",
			"client_secret":      "SECRET-CLIENT",
			"header_overrides":   map[string]any{"Authorization": "Bearer SECRET-HEADER"},
			"brand_new_field":    "SECRET-UNKNOWN",
		},
		Extra: map[string]any{"codex_usage": 12, "session_token_cache": "SECRET-EXTRA"},
		Proxy: &service.Proxy{ID: 9, Name: "p", Protocol: "http", Host: "10.0.0.1", Port: 8080, Username: "SECRET-PUSER", Password: "SECRET-PPASS", Status: "active", OwnerUserID: &owner},
	}
}

func TestEncodeDecodeRoundTripAndSecretsStaySealed(t *testing.T) {
	key, err := sealbox.GenerateKey()
	require.NoError(t, err)
	a := sampleAccount()
	snap, err := accountcodec.Encode(a, map[string]any{"access_token": "SECRET-ACCESS-FRESH"}, 7, key.PublicKey(), nil)
	require.NoError(t, err)

	// 快照的明文部分里找不到任何密钥；refresh token 等连加密的也没有。
	raw, err := proto.Marshal(snap)
	require.NoError(t, err)
	for _, secret := range []string{"SECRET-ACCESS-OLD", "SECRET-ACCESS-FRESH", "SECRET-API-KEY", "SECRET-REFRESH", "SECRET-CLIENT",
		"SECRET-HEADER", "SECRET-UNKNOWN", "SECRET-EXTRA", "SECRET-PUSER", "SECRET-PPASS"} {
		require.False(t, bytes.Contains(raw, []byte(secret)), "%s must not appear unsealed", secret)
	}

	open := func(sealed, aad []byte) ([]byte, error) { return sealbox.Open(key, sealed, aad) }
	cache := accountcodec.NewSecretCache()
	got, err := accountcodec.Decode(snap, 7, open, cache)
	require.NoError(t, err)
	require.Equal(t, "SECRET-ACCESS-FRESH", got.Credentials["access_token"], "the master's fresh token wins")
	require.Equal(t, "SECRET-API-KEY", got.Credentials["api_key"])
	require.Equal(t, "https://api.example.com", got.Credentials["base_url"])
	require.Equal(t, "SECRET-UNKNOWN", got.Credentials["brand_new_field"], "unknown fields travel sealed")
	require.NotContains(t, got.Credentials, "refresh_token")
	require.NotContains(t, got.Credentials, "client_secret")
	require.NotContains(t, got.Extra, "session_token_cache")
	require.EqualValues(t, 12, got.Extra["codex_usage"])
	require.Equal(t, "SECRET-PPASS", got.Proxy.Password)
	require.Equal(t, int64(77), *got.Proxy.OwnerUserID)
	require.Equal(t, 0.8, *got.RateMultiplier)
	require.Equal(t, int64(3), *got.ParentAccountID)
	require.Equal(t, []int64{1, 2}, got.GroupIDs)
	require.True(t, cache.Has(42, snap.CredentialVersion))

	// 节点已有这个版本：不再下发密钥，从本地缓存取。
	again, err := accountcodec.Encode(a, map[string]any{"access_token": "SECRET-ACCESS-FRESH"}, 7, key.PublicKey(), cache.Has)
	require.NoError(t, err)
	require.Empty(t, again.SealedCredentials)
	require.Equal(t, snap.CredentialVersion, again.CredentialVersion)
	got2, err := accountcodec.Decode(again, 7, open, cache)
	require.NoError(t, err)
	require.Equal(t, "SECRET-API-KEY", got2.Credentials["api_key"])

	// 凭据变了（刷新了 token）就是新版本，要重新下发。
	changed, err := accountcodec.Encode(a, map[string]any{"access_token": "SECRET-ACCESS-NEWER"}, 7, key.PublicKey(), cache.Has)
	require.NoError(t, err)
	require.NotEqual(t, snap.CredentialVersion, changed.CredentialVersion)
	require.NotEmpty(t, changed.SealedCredentials)

	cache.Forget(42)
	_, err = accountcodec.Decode(again, 7, open, cache)
	require.ErrorIs(t, err, accountcodec.ErrSecretsMissing)
}

// 密文绑定节点、账号和版本：别的节点解不开；改了账号 ID 或版本也解不开。
func TestSealedCredentialsAreBoundToNodeAccountAndVersion(t *testing.T) {
	key, err := sealbox.GenerateKey()
	require.NoError(t, err)
	snap, err := accountcodec.Encode(sampleAccount(), nil, 7, key.PublicKey(), nil)
	require.NoError(t, err)
	open := func(sealed, aad []byte) ([]byte, error) { return sealbox.Open(key, sealed, aad) }

	_, err = accountcodec.Decode(snap, 8, open, nil)
	require.Error(t, err, "sealed for node 7")

	tampered := clone(snap)
	tampered.Id = 43
	_, err = accountcodec.Decode(tampered, 7, open, nil)
	require.Error(t, err)

	tampered = clone(snap)
	tampered.CredentialVersion = "0000"
	_, err = accountcodec.Decode(tampered, 7, open, nil)
	require.Error(t, err)

	other, err := sealbox.GenerateKey()
	require.NoError(t, err)
	_, err = accountcodec.Decode(snap, 7, func(sealed, aad []byte) ([]byte, error) { return sealbox.Open(other, sealed, aad) }, nil)
	require.Error(t, err, "another node's key cannot open it")

	_, err = accountcodec.Encode(sampleAccount(), nil, 7, nil, nil)
	require.Error(t, err, "a node without an encryption key gets no credentials")
}

func TestLooksSecret(t *testing.T) {
	for _, n := range []string{"access_token", "API_KEY", "client_secret", "cookie", "private_key", "aws_session_token"} {
		require.True(t, accountcodec.LooksSecret(n), n)
	}
	for _, n := range []string{"base_url", "codex_usage", "model_mapping",
		"openai_apikey_responses_websockets_v2_enabled", "openai_apikey_responses_websockets_v2_mode"} {
		require.False(t, accountcodec.LooksSecret(n), n)
	}
}

func clone(s *relayv1.AccountSnapshot) *relayv1.AccountSnapshot {
	c, _ := proto.Clone(s).(*relayv1.AccountSnapshot)
	return c
}
