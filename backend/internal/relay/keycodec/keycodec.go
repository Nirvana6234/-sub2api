// Package keycodec 编解码准入回复里的 API Key 快照（设计 3.2、开发计划 WP9）。
//
// 从节点要在选号之前运行处理函数里的本地步骤（推理强度策略、生图权限、模型白名单中间件等），转发时也按
// Key 的分组读配置，所以准入通过后主节点把 Key、用户、分组、订阅按白名单发过去。用户只带转发要用的字段：
// 邮箱、密码哈希、TOTP、余额等一律不出主节点。每个字段都要在 keycodec_test.go 里归类，新增字段时测试失败。
//
// 从节点不信这份快照做任何计费或放行的决定：选号时主节点独立复查。
package keycodec

import (
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// EncodeAPIKey 按白名单编码 Key（含用户、分组）。Key 原文不带：从节点本来就有。
func EncodeAPIKey(k *service.APIKey) ([]byte, error) {
	if k == nil {
		return nil, errors.New("keycodec: nil api key")
	}
	out := &service.APIKey{
		ID:        k.ID,
		UserID:    k.UserID,
		Name:      k.Name,
		GroupID:   k.GroupID,
		AutoGroup: k.AutoGroup,
		Status:    k.Status,
		User:      user(k.User),
		Group:     group(k.Group),
	}
	return json.Marshal(out)
}

// DecodeAPIKey 解码 Key 快照，Key 原文由调用方填回。
func DecodeAPIKey(data []byte, rawKey string) (*service.APIKey, error) {
	var k service.APIKey
	if err := json.Unmarshal(data, &k); err != nil {
		return nil, err
	}
	if k.ID <= 0 || k.User == nil || k.User.ID <= 0 {
		return nil, errors.New("keycodec: incomplete api key snapshot")
	}
	k.Key = rawKey
	return &k, nil
}

// EncodeSubscription 按白名单编码订阅；nil 编码为空。
func EncodeSubscription(s *service.UserSubscription) ([]byte, error) {
	if s == nil {
		return nil, nil
	}
	return json.Marshal(&service.UserSubscription{
		ID:        s.ID,
		UserID:    s.UserID,
		GroupID:   s.GroupID,
		StartsAt:  s.StartsAt,
		ExpiresAt: s.ExpiresAt,
		Status:    s.Status,
	})
}

// DecodeSubscription 解码订阅快照；空为 nil。
func DecodeSubscription(data []byte) (*service.UserSubscription, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var s service.UserSubscription
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func user(u *service.User) *service.User {
	if u == nil {
		return nil
	}
	return &service.User{
		ID:          u.ID,
		Role:        u.Role,
		Concurrency: u.Concurrency,
		Status:      u.Status,
	}
}

// group 复制分组，去掉账号列表和统计数（大，且从节点用不上）。分组里没有密钥（测试核对字段名）。
func group(g *service.Group) *service.Group {
	if g == nil {
		return nil
	}
	cp := *g
	cp.AccountGroups = nil
	cp.AccountCount, cp.ActiveAccountCount, cp.RateLimitedAccountCount = 0, 0, 0
	return &cp
}
