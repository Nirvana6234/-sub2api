package service

import (
	"context"
	"net/http"
)

// FingerprintHeaderNames 是指纹创建与升级读的请求头（createFingerprintFromHeaders、mergeHeadersIntoFingerprint）。
// 主从分流的从节点只把这几个随选号带给主节点，主节点按它们做与本地同一段 GetOrCreateFingerprint。
var FingerprintHeaderNames = []string{
	"User-Agent",
	"X-Stainless-Lang",
	"X-Stainless-Package-Version",
	"X-Stainless-OS",
	"X-Stainless-Arch",
	"X-Stainless-Runtime",
	"X-Stainless-Runtime-Version",
}

// RelayIdentity 是主节点选号时给从节点的身份信息（指纹、伪装会话 ID 在主节点的缓存里）：
//   - Anthropic OAuth / setup-token 账号的指纹：与本地转发时同一段 GetOrCreateFingerprint（按客户端请求头创建、升级、续期）；
//   - 开了会话 ID 伪装的账号现在的伪装会话 ID：只读、不续期，从节点真用到时报回来再写（本地也是用到时才写）。
//
// 不是 OAuth 账号时都为空。
func (s *GatewayService) RelayIdentity(ctx context.Context, account *Account, headers http.Header) (*Fingerprint, string, error) {
	if s == nil || s.identityService == nil || account == nil || !account.IsAnthropicOAuthOrSetupToken() {
		return nil, "", nil
	}
	fp, err := s.identityService.GetOrCreateFingerprint(ctx, account.ID, headers)
	if err != nil {
		return nil, "", err
	}
	masked := ""
	if account.IsSessionIDMaskingEnabled() {
		if masked, err = s.identityService.cache.GetMaskedSessionID(ctx, account.ID); err != nil {
			return nil, "", err
		}
	}
	return fp, masked, nil
}

// SetRelayMaskedSessionID 写伪装会话 ID 并续期（主节点照写从节点转发时用到的，本地 RewriteUserIDWithMasking 里的那一步）。
func (s *GatewayService) SetRelayMaskedSessionID(ctx context.Context, accountID int64, sessionID string) error {
	if s == nil || s.identityService == nil {
		return nil
	}
	return s.identityService.cache.SetMaskedSessionID(ctx, accountID, sessionID)
}
