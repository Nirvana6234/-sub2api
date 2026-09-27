package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

// CyberSessionBlockStore 是 cyber 会话屏蔽表的存取接口。
// repository 层 gatewayCache 附带实现（类型断言探测接入，不改 GatewayCache
// 共享接口）；测试 stub 不实现时屏蔽能力自动降级关闭。
type CyberSessionBlockStore interface {
	SetCyberSessionBlocked(ctx context.Context, scopeKey string, keys []string, ttl time.Duration) error
	IsCyberSessionScopeActive(ctx context.Context, scopeKey string) (bool, error)
	FindCyberSessionBlocked(ctx context.Context, keys []string) (string, error)
}

const cyberSessionTranscriptLookupOverflowBlockKey = "transcript_lookup_limit_exceeded"

// CyberSessionExplicitBlockKey returns an inexpensive exact key when the
// client supplies a stable session signal.
func CyberSessionExplicitBlockKey(apiKeyID int64, c *gin.Context, body []byte) string {
	return hashCyberSessionBlockKey(apiKeyID, explicitOpenAISessionID(c, body))
}

// CyberSessionTranscriptBlockKeys returns the exact full-request key followed
// by an optional rewrite-tolerant context key. The latter is emitted only after
// model-generated history has been observed.
func CyberSessionTranscriptBlockKeys(apiKeyID int64, body []byte) []string {
	derived := deriveOpenAICyberTranscriptBlockKeys(apiKeyID, body)
	if len(derived.lookupKeys) == 0 {
		return nil
	}
	keys := []string{derived.lookupKeys[len(derived.lookupKeys)-1]}
	if derived.preLatestUserKey != "" && derived.preLatestUserKey != keys[0] {
		keys = append(keys, derived.preLatestUserKey)
	}
	return keys
}

func CyberSessionTranscriptLookupKeys(apiKeyID int64, body []byte) []string {
	return deriveOpenAICyberTranscriptBlockKeys(apiKeyID, body).lookupKeys
}

// CyberSessionScopeKey is a coarse, non-blocking fingerprint used only to
// avoid transcript parsing and MGET for sources that never produced a hit.
func CyberSessionScopeKey(apiKeyID int64, clientIP, userAgent string) string {
	if apiKeyID <= 0 {
		return ""
	}
	raw := "cyber-scope:v1|api_key=" + strconv.FormatInt(apiKeyID, 10) +
		"|ip=" + strings.TrimSpace(clientIP) +
		"|ua=" + NormalizeSessionUserAgent(userAgent)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func hashCyberSessionBlockKey(apiKeyID int64, raw string) string {
	if raw == "" {
		return ""
	}
	isolated := isolateOpenAISessionID(apiKeyID, raw)
	sum := sha256.Sum256([]byte(isolated))
	return hex.EncodeToString(sum[:])
}

// cyberSessionBlockStore 探测 cache 是否具备屏蔽存储能力。
// 注意：若未来以装饰器包装 GatewayCache（如日志/指标装饰器），该装饰器必须同时实现
// CyberSessionBlockStore，否则会话屏蔽能力将静默降级关闭
// （编译断言 var _ service.CyberSessionBlockStore = (*gatewayCache)(nil) 只覆盖
// *gatewayCache 本体，无法覆盖其外层包装）。
func (s *OpenAIGatewayService) cyberSessionBlockStore() CyberSessionBlockStore {
	if s == nil || s.cache == nil {
		return nil
	}
	store, ok := s.cache.(CyberSessionBlockStore)
	if !ok {
		return nil
	}
	return store
}

// CyberSessionBlockRuntime 返回 (开关, TTL)。开关默认关。
// 委托给 SettingService.GetCyberSessionBlockRuntime，进程内缓存避免热路径 DB 往返。
func (s *OpenAIGatewayService) CyberSessionBlockRuntime(ctx context.Context) (bool, time.Duration) {
	if s == nil || s.settingService == nil {
		return false, time.Hour
	}
	return s.settingService.GetCyberSessionBlockRuntime(ctx)
}

// MarkCyberSessionBlocked 把会话写入屏蔽表（写入点：cyber 命中后）。
// 开关关闭、key 为空或存储不可用时静默跳过。
func (s *OpenAIGatewayService) MarkCyberSessionBlocked(ctx context.Context, scopeKey string, keys []string) {
	if s == nil || len(keys) == 0 {
		return
	}
	enabled, ttl := s.CyberSessionBlockRuntime(ctx)
	if !enabled {
		return
	}
	store := s.cyberSessionBlockStore()
	if store == nil {
		return
	}
	if err := store.SetCyberSessionBlocked(ctx, scopeKey, keys, ttl); err != nil {
		logger.LegacyPrintf("service.openai_gateway", "cyber session block write failed: err=%v", err)
	}
}

// FindCyberSessionBlockedForRequest applies explicit-first lookup followed by
// scope-gated transcript matching. All failures remain fail-open.
func (s *OpenAIGatewayService) FindCyberSessionBlockedForRequest(ctx context.Context, apiKeyID int64, c *gin.Context, body []byte, clientIP, userAgent string) string {
	enabled, _ := s.CyberSessionBlockRuntime(ctx)
	if !enabled {
		return ""
	}
	return s.findCyberSessionBlocked(ctx, CyberSessionExplicitBlockKey(apiKeyID, c, body), CyberSessionScopeKey(apiKeyID, clientIP, userAgent), func() ([]string, bool) {
		transcript := deriveOpenAICyberTranscriptBlockKeys(apiKeyID, body)
		return transcript.lookupKeys, transcript.lookupKeysTruncated
	})
}

// CyberSessionLookup 是 cyber 会话屏蔽要查的键（都已哈希）。主从分流时从节点按请求算出、
// 随选号发给主节点，主节点查屏蔽表（设计 3.4）。
type CyberSessionLookup struct {
	ExplicitKey         string
	ScopeKey            string
	TranscriptKeys      []string
	TranscriptTruncated bool
}

// NewCyberSessionLookup 按请求算出要查的键，与 FindCyberSessionBlockedForRequest 用的一致。
func NewCyberSessionLookup(apiKeyID int64, c *gin.Context, body []byte, clientIP, userAgent string) CyberSessionLookup {
	transcript := deriveOpenAICyberTranscriptBlockKeys(apiKeyID, body)
	return CyberSessionLookup{
		ExplicitKey:         CyberSessionExplicitBlockKey(apiKeyID, c, body),
		ScopeKey:            CyberSessionScopeKey(apiKeyID, clientIP, userAgent),
		TranscriptKeys:      transcript.lookupKeys,
		TranscriptTruncated: transcript.lookupKeysTruncated,
	}
}

// FindCyberSessionBlockedByLookup 用从节点算好的键查屏蔽表，规则与 FindCyberSessionBlockedForRequest 相同。
func (s *OpenAIGatewayService) FindCyberSessionBlockedByLookup(ctx context.Context, l CyberSessionLookup) string {
	enabled, _ := s.CyberSessionBlockRuntime(ctx)
	if !enabled {
		return ""
	}
	return s.findCyberSessionBlocked(ctx, l.ExplicitKey, l.ScopeKey, func() ([]string, bool) {
		return l.TranscriptKeys, l.TranscriptTruncated
	})
}

// findCyberSessionBlocked：先查显式会话键，再在会话范围激活时查对话记录键。transcript 只在范围激活时才算。
func (s *OpenAIGatewayService) findCyberSessionBlocked(ctx context.Context, explicitKey, scopeKey string, transcript func() ([]string, bool)) string {
	store := s.cyberSessionBlockStore()
	if store == nil {
		return ""
	}
	if explicitKey != "" {
		key, err := store.FindCyberSessionBlocked(ctx, []string{explicitKey})
		if err != nil {
			logger.LegacyPrintf("service.openai_gateway", "cyber explicit session read failed: err=%v", err)
			return ""
		}
		if key != "" {
			return key
		}
	}
	active, err := store.IsCyberSessionScopeActive(ctx, scopeKey)
	if err != nil {
		logger.LegacyPrintf("service.openai_gateway", "cyber session scope read failed: err=%v", err)
		return ""
	}
	if !active {
		return ""
	}
	keys, truncated := transcript()
	if truncated {
		// Once the coarse scope is active, silently dropping old candidates would
		// let a blocked client evade prefix matching by appending dummy items.
		return cyberSessionTranscriptLookupOverflowBlockKey
	}
	if len(keys) == 0 {
		return ""
	}
	key, err := store.FindCyberSessionBlocked(ctx, keys)
	if err != nil {
		logger.LegacyPrintf("service.openai_gateway", "cyber session block batch read failed: err=%v", err)
		return ""
	}
	return key
}
