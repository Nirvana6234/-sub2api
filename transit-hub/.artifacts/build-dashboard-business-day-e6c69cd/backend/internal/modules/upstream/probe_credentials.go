package upstream

import (
	"net/http"
	"net/url"
	"strings"
)

// ProbeCredential contains the short-lived upstream credentials needed for a
// server-side health probe. Key is plaintext and must never be logged, stored,
// or returned to the browser.
type ProbeCredential struct {
	BaseURL         string
	Key             string
	ProviderFamily  string
	Models          []string
	AccountPlatform string
	AccountType     string
	Extra           map[string]any
	HeaderOverrides map[string]string
}

const chatGPTCodexResponsesURL = "https://chatgpt.com/backend-api/codex/responses"

const (
	ReasonCredentialUnavailable      = "credential_unavailable"
	ReasonSecureVerificationRequired = "secure_verification_required"
	ReasonBaseURLUnavailable         = "base_url_unavailable"
	ReasonModelUnavailable           = "model_unavailable"
	ReasonExportUnavailable          = "export_unavailable"
	ReasonCredentialsRedacted        = "credentials_redacted"
)

type ProbeCredentialError struct {
	Reason string
}

func (e *ProbeCredentialError) Error() string { return e.Reason }

func newProbeCredentialError(reason string) *ProbeCredentialError {
	return &ProbeCredentialError{Reason: reason}
}

func ProbeCredentialReason(err error) string {
	if err == nil {
		return ""
	}
	if credErr, ok := err.(*ProbeCredentialError); ok {
		return credErr.Reason
	}
	return ReasonCredentialUnavailable
}

var sub2apiCredentialKeyFields = []string{
	"api_key", "apiKey", "access_token", "accessToken", "session_key", "sessionKey", "key", "token",
}

var sub2apiCredentialBaseURLFields = []string{
	"base_url", "baseUrl", "endpoint", "api_base", "apiBase", "url",
}

func (s *PlatformService) ResolveProbeCredential(session Session, account AdminGroupAccountInfo) (ProbeCredential, error) {
	switch session.Platform {
	case PlatformNewAPI:
		return s.resolveNewAPIChannelCredential(session, account)
	default:
		return s.resolveSub2APIAccountCredential(session, account)
	}
}

func (s *PlatformService) resolveNewAPIChannelCredential(session Session, account AdminGroupAccountInfo) (ProbeCredential, error) {
	baseURL := strings.TrimSpace(account.BaseURL)
	if baseURL == "" {
		return ProbeCredential{}, newProbeCredentialError(ReasonBaseURLUnavailable)
	}
	if strings.TrimSpace(account.ID) == "" {
		return ProbeCredential{}, newProbeCredentialError(ReasonCredentialUnavailable)
	}
	key, err := s.FetchNewAPIChannelKey(session, account.ID)
	if err != nil {
		return ProbeCredential{}, err
	}
	return ProbeCredential{
		BaseURL:         baseURL,
		Key:             key,
		ProviderFamily:  account.Platform,
		Models:          splitModels(account.Models),
		AccountPlatform: account.Platform,
		AccountType:     account.Type,
	}, nil
}

func (s *PlatformService) FetchNewAPIChannelKey(session Session, channelID string) (string, error) {
	if session.Platform != PlatformNewAPI || !session.IsAuthenticated() {
		return "", newProbeCredentialError(ReasonSecureVerificationRequired)
	}
	response, err := s.httpClient.requestJSON(session.BaseURL+"/api/channel/"+url.PathEscape(channelID)+"/key", requestOptions{
		Cookie:      session.Cookie,
		UserID:      session.UserID,
		AccessToken: session.AccessToken,
		TokenType:   session.TokenType,
		Method:      http.MethodPost,
	})
	if err != nil {
		if reqErr, ok := err.(*RequestError); ok {
			if reqErr.MessageKey == ErrorAuth || reqErr.StatusCode == http.StatusForbidden {
				return "", newProbeCredentialError(ReasonSecureVerificationRequired)
			}
		}
		return "", newProbeCredentialError(ReasonCredentialUnavailable)
	}
	data := dataRecord(response.Payload)
	if key := firstString(data, []string{"key"}); key != nil && strings.TrimSpace(*key) != "" {
		return strings.TrimSpace(*key), nil
	}
	return "", newProbeCredentialError(ReasonCredentialUnavailable)
}

func (s *PlatformService) resolveSub2APIAccountCredential(session Session, account AdminGroupAccountInfo) (ProbeCredential, error) {
	if session.Platform != PlatformSub2API || !session.IsAuthenticated() {
		return ProbeCredential{}, newProbeCredentialError(ReasonCredentialUnavailable)
	}
	accountID := strings.TrimSpace(account.ID)
	if accountID == "" {
		return ProbeCredential{}, newProbeCredentialError(ReasonCredentialUnavailable)
	}

	exportURL := session.BaseURL + "/api/v1/admin/accounts/data?ids=" + url.QueryEscape(accountID) + "&include_proxies=false"
	response, err := s.httpClient.requestJSON(exportURL, adminAuthOptions(session))
	if err != nil {
		return ProbeCredential{}, newProbeCredentialError(ReasonExportUnavailable)
	}

	items := sub2APIExportAccounts(response.Payload)
	if len(items) != 1 {
		return ProbeCredential{}, newProbeCredentialError(ReasonCredentialUnavailable)
	}
	record, ok := items[0].(map[string]any)
	if !ok {
		return ProbeCredential{}, newProbeCredentialError(ReasonCredentialUnavailable)
	}

	credentials, _ := record["credentials"].(map[string]any)
	key := firstPlaintextKey(credentials)
	if key == "" {
		return ProbeCredential{}, newProbeCredentialError(ReasonCredentialsRedacted)
	}

	accountPlatform := firstNonEmptyString(stringFromAny(record["platform"]), account.Platform)
	accountType := firstNonEmptyString(stringFromAny(record["type"]), account.Type)
	extra := mapFromAny(record["extra"])

	baseURL := firstBaseURL(credentials)
	if baseURL == "" {
		if b := firstString(record, sub2apiCredentialBaseURLFields); b != nil {
			baseURL = strings.TrimSpace(*b)
		}
	}
	if baseURL == "" {
		switch {
		case isOpenAIOAuthProbeAccount(accountPlatform, accountType):
			baseURL = chatGPTCodexResponsesURL
		case isOpenAIAPIKeyProbeAccount(accountPlatform, accountType):
			baseURL = "https://api.openai.com"
		default:
			return ProbeCredential{}, newProbeCredentialError(ReasonBaseURLUnavailable)
		}
	}

	return ProbeCredential{
		BaseURL:         baseURL,
		Key:             key,
		ProviderFamily:  accountPlatform,
		Models:          splitModels(account.Models),
		AccountPlatform: accountPlatform,
		AccountType:     accountType,
		Extra:           extra,
		HeaderOverrides: headerOverridesFromCredentials(accountPlatform, accountType, credentials),
	}, nil
}

func sub2APIExportAccounts(payload any) []any {
	root, ok := payload.(map[string]any)
	if !ok {
		return []any{}
	}
	switch data := root["data"].(type) {
	case []any:
		return data
	case map[string]any:
		if accounts, ok := data["accounts"].([]any); ok {
			return accounts
		}
	}
	return []any{}
}

func firstPlaintextKey(credentials map[string]any) string {
	if credentials == nil {
		return ""
	}
	if v := firstString(credentials, sub2apiCredentialKeyFields); v != nil {
		return strings.TrimSpace(*v)
	}
	return ""
}

func firstBaseURL(credentials map[string]any) string {
	if credentials == nil {
		return ""
	}
	if v := firstString(credentials, sub2apiCredentialBaseURLFields); v != nil {
		return strings.TrimSpace(*v)
	}
	return ""
}

func isOpenAIOAuthProbeAccount(platform string, accountType string) bool {
	return strings.EqualFold(strings.TrimSpace(platform), "openai") &&
		(strings.EqualFold(strings.TrimSpace(accountType), "oauth") || strings.EqualFold(strings.TrimSpace(accountType), "setup-token"))
}

func isOpenAIAPIKeyProbeAccount(platform string, accountType string) bool {
	return strings.EqualFold(strings.TrimSpace(platform), "openai") &&
		(strings.EqualFold(strings.TrimSpace(accountType), "apikey") || strings.EqualFold(strings.TrimSpace(accountType), "api_key"))
}

func isHeaderOverrideEligibleProbeAccount(platform string, accountType string) bool {
	p := strings.ToLower(strings.TrimSpace(platform))
	t := strings.ToLower(strings.TrimSpace(accountType))
	return (p == "openai" || p == "anthropic") && (t == "apikey" || t == "api_key")
}

func headerOverridesFromCredentials(platform string, accountType string, credentials map[string]any) map[string]string {
	if !isHeaderOverrideEligibleProbeAccount(platform, accountType) || credentials == nil {
		return nil
	}
	enabled, _ := credentials["header_override_enabled"].(bool)
	if !enabled {
		return nil
	}
	raw, ok := credentials["header_overrides"].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for name, value := range raw {
		headerName := strings.ToLower(strings.TrimSpace(name))
		headerValue, ok := value.(string)
		headerValue = strings.TrimSpace(headerValue)
		if !ok || headerName == "" || headerValue == "" || isBlockedHeaderOverrideName(headerName) {
			continue
		}
		out[headerName] = headerValue
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isBlockedHeaderOverrideName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "host", "content-length", "content-type", "transfer-encoding", "connection", "keep-alive",
		"proxy-authenticate", "proxy-authorization", "proxy-connection", "te", "trailer", "upgrade",
		"authorization", "x-api-key", "x-goog-api-key", "cookie", "accept-encoding",
		"sec-websocket-key", "sec-websocket-version", "sec-websocket-extensions", "sec-websocket-protocol",
		"sec-websocket-accept", "session_id", "conversation_id", "x-codex-turn-state",
		"x-codex-turn-metadata", "chatgpt-account-id", "x-claude-code-session-id",
		"x-client-request-id", "x-grok-conv-id":
		return true
	default:
		return false
	}
}

func mapFromAny(value any) map[string]any {
	source, ok := value.(map[string]any)
	if !ok || source == nil {
		return nil
	}
	out := make(map[string]any, len(source))
	for k, v := range source {
		out[k] = v
	}
	return out
}

func stringFromAny(value any) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func splitModels(models string) []string {
	if strings.TrimSpace(models) == "" {
		return nil
	}
	parts := strings.Split(models, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
