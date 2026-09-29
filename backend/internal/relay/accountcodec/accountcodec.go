// Package accountcodec 把选号选中的账号编码成给从节点的快照，以及在从节点上解回来
// （设计 2.2、7.1、9.1，开发计划 WP7）。
//
// 按白名单编码：转发要用的普通配置原样带上；其余凭据一律当作密钥，用那台节点的 X25519
// 加密公钥加密（sealbox），附加数据绑定节点、账号和凭据版本，别的节点、别的账号解不开。
// refresh token、client secret、服务账号文件这类只该在主节点用的永远不下发。
// 新增的凭据字段默认进密钥那一份，要明文下发必须显式加进白名单并确认不是密钥。
package accountcodec

import (
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sealbox"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// publicCredentialKeys 是可以明文下发的凭据字段：转发要用、不是密钥。
var publicCredentialKeys = map[string]bool{
	"base_url": true, "api_base_urls": true, "api_protocol": true,
	"model_mapping": true, "compact_model_mapping": true,
	"project_id": true, "subscription_tier": true, "plan_type": true, "tier_id": true, "entitlement_status": true,
	"pool_mode": true, "pool_mode_retry_status_codes": true, "pool_mode_retry_count": true,
	"temp_unschedulable_enabled": true, "temp_unschedulable_rules": true,
	"custom_error_codes_enabled": true, "custom_error_codes": true,
	"header_override_enabled": true,
	"chatgpt_account_id":      true, "chatgpt_user_id": true, "chatgpt_account_is_fedramp": true,
	"organization_id": true, "organization": true, "org_uuid": true, "team_id": true,
	"oauth_type": true, "account_mode": true, "auth_mode": true, "token_type": true,
	"aws_region": true, "vertex_location": true, "vertex_model_locations": true, "location": true,
	"model_provider": true, "user_agent": true, "intercept_warmup_requests": true,
	"zhipu_project": true, "zhipu_organization": true,
	"expires_at": true, "subscription_expires_at": true,
}

// neverSentCredentialKeys 只在主节点用（刷新 token、签服务账号 token），不下发，加密的也不给。
var neverSentCredentialKeys = map[string]bool{
	"refresh_token": true, "client_secret": true,
	"service_account_json": true, "service_account": true, "private_key": true,
}

const (
	proxyUsernameKey = "__proxy_username"
	proxyPasswordKey = "__proxy_password"
)

// nonSecretExtraKeys 是名字像密钥、其实只是开关的账号附加字段（按名字剔除会让从节点的转发与单机不同）。
var nonSecretExtraKeys = map[string]bool{
	// API Key 账号的 Responses WebSocket 开关与模式（名字里有 "apikey"）。
	"openai_apikey_responses_websockets_v2_enabled": true,
	"openai_apikey_responses_websockets_v2_mode":    true,
}

// LooksSecret 报告一个字段名是否像密钥（Extra 里剔除用）。
func LooksSecret(name string) bool {
	if nonSecretExtraKeys[name] {
		return false
	}
	n := strings.ToLower(name)
	for _, s := range []string{"token", "secret", "password", "passwd", "api_key", "apikey", "private", "cookie", "credential", "session_key"} {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

// credentialAAD 把加密的凭据绑定到节点、账号和版本。
func credentialAAD(nodeID, accountID int64, version string) []byte {
	return []byte(fmt.Sprintf("sub2api-relay-credentials|node=%d|account=%d|version=%s", nodeID, accountID, version))
}

// splitCredentials 把账号凭据分成明文那份和密钥那份；overrides（比如主节点刚取的短期 access token）
// 覆盖进密钥那份。
func splitCredentials(a *service.Account, overrides map[string]any) (public, secrets map[string]any) {
	public, secrets = map[string]any{}, map[string]any{}
	for k, v := range a.Credentials {
		switch {
		case neverSentCredentialKeys[k]:
		case publicCredentialKeys[k]:
			public[k] = v
		default:
			secrets[k] = v
		}
	}
	for k, v := range overrides {
		if !neverSentCredentialKeys[k] {
			secrets[k] = v
		}
	}
	if a.Proxy != nil {
		if a.Proxy.Username != "" {
			secrets[proxyUsernameKey] = a.Proxy.Username
		}
		if a.Proxy.Password != "" {
			secrets[proxyPasswordKey] = a.Proxy.Password
		}
	}
	return public, secrets
}

// Version 是密钥那份的内容哈希（凭据版本）。encoding/json 按键排序，同样的内容版本相同。
func Version(secrets map[string]any) (string, error) {
	raw, err := json.Marshal(secrets)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:16]), nil
}

// Encode 把账号编码成给 nodeID 的快照。nodeHas 报告这台节点手里是否已有这个账号的某个凭据版本，
// 有就不再下发密钥（设计 9.1：同一账号不重复下发；能不能用由这次选号决定）。
func Encode(a *service.Account, overrides map[string]any, nodeID int64, recipient *ecdh.PublicKey, nodeHas func(accountID int64, version string) bool) (*relayv1.AccountSnapshot, error) {
	if a == nil {
		return nil, errors.New("accountcodec: nil account")
	}
	public, secrets := splitCredentials(a, overrides)
	publicJSON, err := json.Marshal(public)
	if err != nil {
		return nil, err
	}
	extra := map[string]any{}
	for k, v := range a.Extra {
		if !LooksSecret(k) {
			extra[k] = v
		}
	}
	extraJSON, err := json.Marshal(extra)
	if err != nil {
		return nil, err
	}
	version, err := Version(secrets)
	if err != nil {
		return nil, err
	}
	s := &relayv1.AccountSnapshot{
		Id: a.ID, Name: a.Name, Platform: a.Platform, Type: a.Type,
		Concurrency: int32(a.Concurrency), Priority: int32(a.Priority),
		RateMultiplierUndeclared: a.RateMultiplierUndeclared, Status: a.Status,
		PublicCredentials: publicJSON, Extra: extraJSON, QuotaDimension: a.QuotaDimension,
		GroupIds: append([]int64(nil), a.GroupIDs...), CredentialVersion: version,
		ContributionRouteSource: a.ContributionRouteSource, ContributionRoomId: a.ContributionRoomID,
	}
	if a.RateMultiplier != nil {
		s.HasRateMultiplier, s.RateMultiplier = true, *a.RateMultiplier
	}
	if a.ParentAccountID != nil {
		s.ParentAccountId = *a.ParentAccountID
	}
	if a.ContributionRateMultiplierOverride != nil {
		s.HasContributionRateMultiplierOverride, s.ContributionRateMultiplierOverride = true, *a.ContributionRateMultiplierOverride
	}
	if p := a.Proxy; p != nil {
		s.Proxy = &relayv1.ProxySnapshot{Id: p.ID, Name: p.Name, Protocol: p.Protocol, Host: p.Host, Port: int32(p.Port), Status: p.Status}
		if p.OwnerUserID != nil {
			s.Proxy.HasOwnerUserId, s.Proxy.OwnerUserId = true, *p.OwnerUserID
		}
	}
	if nodeHas == nil || !nodeHas(a.ID, version) {
		if recipient == nil {
			return nil, errors.New("accountcodec: the node has no encryption key")
		}
		plain, err := json.Marshal(secrets)
		if err != nil {
			return nil, err
		}
		if s.SealedCredentials, err = sealbox.Seal(recipient, plain, credentialAAD(nodeID, a.ID, version)); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// SecretCache 是从节点按（账号, 凭据版本）缓存的解密后的密钥（设计 9.1：整台节点共用，
// 能不能用由每次选号结果决定）。只在内存。
type SecretCache struct {
	mu sync.RWMutex
	m  map[int64]cachedSecrets
}

type cachedSecrets struct {
	version string
	secrets map[string]any
}

// NewSecretCache 创建空缓存。
func NewSecretCache() *SecretCache { return &SecretCache{m: map[int64]cachedSecrets{}} }

// Has 报告是否已有这个账号的这个版本（选号请求里上报，主节点据此决定下不下发）。
func (c *SecretCache) Has(accountID int64, version string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.m[accountID]
	return ok && e.version == version
}

// Versions 返回手里每个账号的版本（选号请求里上报）。
func (c *SecretCache) Versions() map[int64]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[int64]string, len(c.m))
	for id, e := range c.m {
		out[id] = e.version
	}
	return out
}

// Forget 丢掉一个账号的密钥（上游 401 后，设计 9.1：主节点刷新，下次选号带新版本）。
func (c *SecretCache) Forget(accountID int64) {
	c.mu.Lock()
	delete(c.m, accountID)
	c.mu.Unlock()
}

// Clear 清空（纪元变化、吊销）。
func (c *SecretCache) Clear() {
	c.mu.Lock()
	c.m = map[int64]cachedSecrets{}
	c.mu.Unlock()
}

var (
	// ErrSecretsMissing：快照没带密钥，本地缓存里也没有这个版本（节点应重新选号）。
	ErrSecretsMissing = errors.New("accountcodec: credentials for this version are not cached")
)

// Opener 用节点的加密私钥解开密钥（identity.Identity.OpenSealed）。
type Opener func(sealed, aad []byte) ([]byte, error)

// Decode 在从节点上把快照解回账号：明文凭据 + 密钥（快照带的解密后放进缓存，没带的从缓存取）。
func Decode(s *relayv1.AccountSnapshot, nodeID int64, open Opener, cache *SecretCache) (*service.Account, error) {
	var secrets map[string]any
	if len(s.GetSealedCredentials()) > 0 {
		plain, err := open(s.GetSealedCredentials(), credentialAAD(nodeID, s.GetId(), s.GetCredentialVersion()))
		if err != nil {
			return nil, fmt.Errorf("accountcodec: open credentials: %w", err)
		}
		if err := json.Unmarshal(plain, &secrets); err != nil {
			return nil, err
		}
		if got, err := Version(secrets); err != nil || got != s.GetCredentialVersion() {
			return nil, errors.New("accountcodec: credential version does not match its content")
		}
		if cache != nil {
			cache.mu.Lock()
			cache.m[s.GetId()] = cachedSecrets{version: s.GetCredentialVersion(), secrets: secrets}
			cache.mu.Unlock()
		}
	} else {
		if cache == nil {
			return nil, ErrSecretsMissing
		}
		cache.mu.RLock()
		e, ok := cache.m[s.GetId()]
		cache.mu.RUnlock()
		if !ok || e.version != s.GetCredentialVersion() {
			return nil, ErrSecretsMissing
		}
		secrets = e.secrets
	}

	creds := map[string]any{}
	if len(s.GetPublicCredentials()) > 0 {
		if err := json.Unmarshal(s.GetPublicCredentials(), &creds); err != nil {
			return nil, err
		}
	}
	var proxyUser, proxyPass string
	for k, v := range secrets {
		switch k {
		case proxyUsernameKey:
			proxyUser, _ = v.(string)
		case proxyPasswordKey:
			proxyPass, _ = v.(string)
		default:
			creds[k] = v
		}
	}
	extra := map[string]any{}
	if len(s.GetExtra()) > 0 {
		if err := json.Unmarshal(s.GetExtra(), &extra); err != nil {
			return nil, err
		}
	}
	a := &service.Account{
		ID: s.GetId(), Name: s.GetName(), Platform: s.GetPlatform(), Type: s.GetType(),
		Credentials: creds, Extra: extra, Concurrency: int(s.GetConcurrency()), Priority: int(s.GetPriority()),
		RateMultiplierUndeclared: s.GetRateMultiplierUndeclared(), Status: s.GetStatus(), Schedulable: true,
		QuotaDimension: s.GetQuotaDimension(), GroupIDs: append([]int64(nil), s.GetGroupIds()...),
		ContributionRouteSource: s.GetContributionRouteSource(), ContributionRoomID: s.GetContributionRoomId(),
	}
	if s.GetHasRateMultiplier() {
		v := s.GetRateMultiplier()
		a.RateMultiplier = &v
	}
	if id := s.GetParentAccountId(); id > 0 {
		a.ParentAccountID = &id
	}
	if s.GetHasContributionRateMultiplierOverride() {
		v := s.GetContributionRateMultiplierOverride()
		a.ContributionRateMultiplierOverride = &v
	}
	if p := s.GetProxy(); p != nil {
		a.Proxy = &service.Proxy{ID: p.GetId(), Name: p.GetName(), Protocol: p.GetProtocol(), Host: p.GetHost(), Port: int(p.GetPort()),
			Status: p.GetStatus(), Username: proxyUser, Password: proxyPass}
		if p.GetHasOwnerUserId() {
			owner := p.GetOwnerUserId()
			a.Proxy.OwnerUserID = &owner
		}
		id := p.GetId()
		a.ProxyID = &id
	}
	return a, nil
}
