package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// pawMessagesKeySource resolves groups from a fixed set, the way the real source
// does from the user's available groups: a group that is not in the set is
// forbidden, not merely missing.
type pawMessagesKeySource struct {
	groups map[int64]service.Group
	key    *service.APIKey
}

func (s *pawMessagesKeySource) ResolvePawAPIKey(ctx context.Context, userID, groupID int64) (*service.APIKey, *service.UserSubscription, error) {
	key, subscription, _, err := s.ResolvePawGroupKey(ctx, userID, groupID)
	return key, subscription, err
}

func (s *pawMessagesKeySource) ResolvePawGroupKey(_ context.Context, _, groupID int64) (*service.APIKey, *service.UserSubscription, *service.Group, error) {
	group, ok := s.groups[groupID]
	if !ok {
		return nil, nil, nil, infraerrors.Forbidden("GROUP_FORBIDDEN", "selected group is not available to this user")
	}
	return s.key, nil, &group, nil
}

type pawMessagesObservation struct {
	key       *service.APIKey
	userAgent string
	forwarded string
}

func newPawMessagesEngine(t *testing.T, countTokens bool) (*gin.Engine, *service.APIKey, *pawMessagesObservation) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	claude := service.Group{ID: 5, Name: "claude", Platform: service.PlatformAnthropic, Status: service.StatusActive}
	source := &pawMessagesKeySource{
		groups: map[int64]service.Group{5: claude},
		key: &service.APIKey{
			ID: 99, UserID: 42, Status: service.StatusActive,
			User: &service.User{ID: 42, Status: service.StatusActive},
			// The internal key is automatic and carries candidates. A Messages request
			// must not inherit that: it names its own group.
			AutoGroup: true, AutoGroupIDs: []int64{7, 8},
		},
	}
	observed := &pawMessagesObservation{}
	chat := service.NewPawChatService(nil, source)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Next()
		observed.key, _ = middleware.GetAPIKeyFromContext(c)
		observed.userAgent = c.Request.UserAgent()
		observed.forwarded = c.Request.Header.Get(PawClientUserAgentHeader)
	})
	handler := pawMessagesHandler(chat, PawRouteDependencies{}, countTokens)
	r.POST("/api/v1/paw/messages", handler)
	r.POST("/api/v1/paw/messages/count_tokens", handler)
	return r, source.key, observed
}

func postPawMessages(r *gin.Engine, path string, headers map[string]string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

type anthropicError struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeAnthropicError(t *testing.T, w *httptest.ResponseRecorder) anthropicError {
	t.Helper()
	var decoded anthropicError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &decoded), w.Body.String())
	return decoded
}

// Claude Code parses the error body. The paw envelope is not a shape it knows and
// it would print the raw JSON at the user instead of a message.
func TestPawMessagesAnswersInTheShapeAnthropicClientsParse(t *testing.T) {
	r, _, _ := newPawMessagesEngine(t, false)

	w := postPawMessages(r, "/api/v1/paw/messages", nil, `{"model":"claude-opus-5"}`)

	require.Equal(t, http.StatusBadRequest, w.Code)
	decoded := decodeAnthropicError(t, w)
	require.Equal(t, "error", decoded.Type)
	require.Equal(t, "invalid_request_error", decoded.Error.Type)
}

func TestPawMessagesRequiresAPositiveGroupAndHasNoAutomaticMode(t *testing.T) {
	r, _, observed := newPawMessagesEngine(t, false)

	for _, header := range []string{"", "0", "-3", "auto", "abc"} {
		t.Run("header="+header, func(t *testing.T) {
			observed.key = nil
			headers := map[string]string{}
			if header != "" {
				headers[PawGroupHeader] = header
			}

			w := postPawMessages(r, "/api/v1/paw/messages", headers, `{"model":"claude-opus-5"}`)

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.Nil(t, observed.key, "no group means no key is bound")
		})
	}
}

func TestPawMessagesRefusesAGroupThePlayerCannotUse(t *testing.T) {
	r, _, observed := newPawMessagesEngine(t, false)

	w := postPawMessages(r, "/api/v1/paw/messages", map[string]string{PawGroupHeader: "999"}, `{"model":"claude-opus-5"}`)

	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	require.Equal(t, "permission_error", decodeAnthropicError(t, w).Error.Type)
	require.Nil(t, observed.key)
}

// The same guard every paw route has: an account-session route that also accepted a
// provider key would let a caller pick which credential is billed.
func TestPawMessagesRejectsProviderCredentials(t *testing.T) {
	r, _, _ := newPawMessagesEngine(t, false)

	for name, headers := range map[string]map[string]string{
		"x-api-key header": {PawGroupHeader: "5", "x-api-key": "sk-provider"},
	} {
		t.Run(name, func(t *testing.T) {
			w := postPawMessages(r, "/api/v1/paw/messages", headers, `{"model":"claude-opus-5"}`)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		})
	}

	w := postPawMessages(r, "/api/v1/paw/messages", map[string]string{PawGroupHeader: "5"},
		`{"model":"claude-opus-5","api_key":"sk-provider"}`)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

func TestPawMessagesBindsTheNamedGroupAndDropsTheInternalKeysAutomaticRouting(t *testing.T) {
	for _, path := range []string{"/api/v1/paw/messages", "/api/v1/paw/messages/count_tokens"} {
		t.Run(path, func(t *testing.T) {
			r, internalKey, observed := newPawMessagesEngine(t, strings.HasSuffix(path, "count_tokens"))

			w := postPawMessages(r, path, map[string]string{PawGroupHeader: "5"}, `{"model":"claude-opus-5"}`)

			// No gateway is attached, so a valid request stops at the dispatch boundary.
			require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
			require.Equal(t, "api_error", decodeAnthropicError(t, w).Error.Type)
			require.NotNil(t, observed.key)
			require.NotSame(t, internalKey, observed.key, "the shared key must not be mutated")
			require.Equal(t, int64(5), *observed.key.GroupID)
			require.Equal(t, service.PlatformAnthropic, observed.key.Group.Platform)
			require.False(t, observed.key.AutoGroup)
			require.Empty(t, observed.key.AutoGroupIDs)
			require.True(t, internalKey.AutoGroup, "the internal key keeps its own configuration")
		})
	}
}

// A Claude Code session names models a catalog has no reason to list, such as the
// small one it uses for background work. Refusing them here would break the session
// while the same request made with a key for the group goes through.
func TestPawMessagesDoesNotHoldTheModelToTheGroupCatalog(t *testing.T) {
	r, _, observed := newPawMessagesEngine(t, false)

	w := postPawMessages(r, "/api/v1/paw/messages", map[string]string{PawGroupHeader: "5"},
		`{"model":"claude-haiku-4-5-20251001","max_tokens":1}`)

	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	require.NotNil(t, observed.key, "the request reached the dispatch boundary")
}

func TestPawMessagesToleratesABodyWithNoModel(t *testing.T) {
	r, _, observed := newPawMessagesEngine(t, true)

	w := postPawMessages(r, "/api/v1/paw/messages/count_tokens", map[string]string{PawGroupHeader: "5"}, `{}`)

	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	require.NotNil(t, observed.key)
}

// ---- The editor's User-Agent -------------------------------------------------------

func TestPawMessagesRestoresTheEditorUserAgentForTheGateway(t *testing.T) {
	r, _, observed := newPawMessagesEngine(t, false)
	editor := "claude-cli/2.1.258 (external, claude-vscode)"

	postPawMessages(r, "/api/v1/paw/messages", map[string]string{
		PawGroupHeader:           "5",
		"User-Agent":             "gongfei-desktop-client",
		PawClientUserAgentHeader: editor,
	}, `{"model":"claude-opus-5"}`)

	require.Equal(t, editor, observed.userAgent, "groups restricted to Claude Code read this")
	require.Empty(t, observed.forwarded, "the carrier header must not travel any further")
}

func TestPawMessagesKeepsTheRequestUserAgentWhenNoneIsForwarded(t *testing.T) {
	r, _, observed := newPawMessagesEngine(t, false)

	postPawMessages(r, "/api/v1/paw/messages", map[string]string{
		PawGroupHeader: "5",
		"User-Agent":   "gongfei-desktop-client",
	}, `{"model":"claude-opus-5"}`)

	require.Equal(t, "gongfei-desktop-client", observed.userAgent)
}

func TestPawMessagesIgnoresAForwardedUserAgentThatIsNotPlainText(t *testing.T) {
	r, _, observed := newPawMessagesEngine(t, false)

	for name, hostile := range map[string]string{
		"control character": "claude-cli/2.1.258\x01",
		"too long":          "claude-cli/2.1.258 " + strings.Repeat("x", 600),
	} {
		t.Run(name, func(t *testing.T) {
			postPawMessages(r, "/api/v1/paw/messages", map[string]string{
				PawGroupHeader:           "5",
				"User-Agent":             "gongfei-desktop-client",
				PawClientUserAgentHeader: hostile,
			}, `{"model":"claude-opus-5"}`)

			require.Equal(t, "gongfei-desktop-client", observed.userAgent)
			require.Empty(t, observed.forwarded)
		})
	}
}

// ---- Which gateway serves it --------------------------------------------------------

// Held to gateway.go's own choice, so a request through a key and the same request
// through an account session are handled by the same code.
func TestPawMessagesTargetMirrorsTheAPIKeyGateway(t *testing.T) {
	for _, tc := range []struct {
		platform    string
		countTokens bool
		want        pawMessagesTarget
	}{
		{service.PlatformAnthropic, false, pawMessagesAnthropic},
		{service.PlatformOpenAI, false, pawMessagesOpenAI},
		{service.PlatformGrok, false, pawMessagesOpenAI},
		{service.PlatformGemini, false, pawMessagesAnthropic},
		{service.PlatformAnthropic, true, pawCountTokensAnthropic},
		{service.PlatformOpenAI, true, pawCountTokensOpenAI},
		{service.PlatformGrok, true, pawCountTokensGrok},
		{service.PlatformGemini, true, pawCountTokensAnthropic},
		{"", false, pawMessagesAnthropic},
	} {
		got := pawMessagesTargetFor(tc.platform, tc.countTokens)
		require.Equal(t, tc.want, got, "platform=%q countTokens=%v", tc.platform, tc.countTokens)
	}
}
