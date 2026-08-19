package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/dgraph-io/ristretto"
	"golang.org/x/sync/singleflight"
)

var (
	ErrAPIKeyNotFound       = infraerrors.NotFound("API_KEY_NOT_FOUND", "api key not found")
	ErrGroupNotAllowed      = infraerrors.Forbidden("GROUP_NOT_ALLOWED", "user is not allowed to bind this group")
	ErrAPIKeyExists         = infraerrors.Conflict("API_KEY_EXISTS", "api key already exists")
	ErrAPIKeyTooShort       = infraerrors.BadRequest("API_KEY_TOO_SHORT", "api key must be at least 16 characters")
	ErrAPIKeyInvalidChars   = infraerrors.BadRequest("API_KEY_INVALID_CHARS", "api key can only contain letters, numbers, underscores, and hyphens")
	ErrAPIKeyRateLimited    = infraerrors.TooManyRequests("API_KEY_RATE_LIMITED", "too many failed attempts, please try again later")
	ErrAPIKeyAuthOverloaded = infraerrors.ServiceUnavailable("API_KEY_AUTH_OVERLOADED", "api key authentication is temporarily overloaded")
	ErrAutoGroupUnavailable = infraerrors.Forbidden("AUTO_GROUP_UNAVAILABLE", "no available group for this API key")
	ErrInvalidIPPattern     = infraerrors.BadRequest("INVALID_IP_PATTERN", "invalid IP or CIDR pattern")
	// ErrAPIKeyExpired        = infraerrors.Forbidden("API_KEY_EXPIRED", "api key has expired")
	ErrAPIKeyExpired = infraerrors.Forbidden("API_KEY_EXPIRED", "api key 已过期")
	// ErrAPIKeyQuotaExhausted = infraerrors.TooManyRequests("API_KEY_QUOTA_EXHAUSTED", "api key quota exhausted")
	ErrAPIKeyQuotaExhausted = infraerrors.TooManyRequests("API_KEY_QUOTA_EXHAUSTED", "api key 额度已用完")

	// Rate limit errors
	ErrAPIKeyRateLimit5hExceeded = infraerrors.TooManyRequests("API_KEY_RATE_5H_EXCEEDED", "api key 5小时限额已用完")
	ErrAPIKeyRateLimit1dExceeded = infraerrors.TooManyRequests("API_KEY_RATE_1D_EXCEEDED", "api key 日限额已用完")
	ErrAPIKeyRateLimit7dExceeded = infraerrors.TooManyRequests("API_KEY_RATE_7D_EXCEEDED", "api key 7天限额已用完")
)

const (
	MaxAPIKeyCredentialBytes      = 128
	defaultAuthLookupConcurrency  = 64
	defaultNegativeAuthCacheSize  = 16384
	apiKeyMaxErrorsPerHour        = 20
	apiKeyLastUsedMinTouch        = 30 * time.Second
	apiKeySortCurrentConcurrency  = "current_concurrency"
	autoGroupStrategyPrice        = "price"
	autoGroupStrategyBalanced     = "balanced"
	autoGroupStrategySpeed        = "speed"
	autoGroupMetricWindow         = time.Hour
	autoGroupSampleLimit          = 20
	autoGroupMinReliableSamples   = 3
	autoGroupMaxAlternativeProbes = 3
	autoGroupMaxP95FirstToken     = 30 * time.Second
	autoGroupMetricCacheTTL       = time.Minute
	autoGroupSelectionTTL         = 24 * time.Hour
	autoGroupProbeWaitTimeout     = 2 * time.Minute
	autoGroupPriceReviewInterval  = 15 * time.Minute
	autoGroupPriceReviewRetry     = time.Minute
	autoGroupSwitchInterval       = 60 * time.Minute
	autoGroupSwitchWindow         = time.Hour
	autoGroupMaxSwitchesPerWindow = 1
	// Failure-triggered group switches have a separate, lower-frequency budget
	// so a noisy upstream cannot thrash the user across accounts. A confirmed
	// failure must be sustained before this budget is consumed.
	autoGroupTransientFailureWindow    = 10 * time.Minute
	autoGroupTransientFailureThreshold = 3
	autoGroupSwitchScoreMargin         = 0.10
	autoGroupSlowEventThreshold        = 5
	autoGroupMinPriceDiscount          = 0.08
	autoGroupFallbackRetryAfter        = 30 * time.Minute
	autoGroupCacheSweepEvery           = 256
	autoGroupCacheSweepLimit           = 128
	autoGroupResolveMaxAttempts        = 4
	// DB 写失败后的短退避，避免请求路径持续同步重试造成写风暴与高延迟。
	apiKeyLastUsedFailBackoff = 5 * time.Second
)

// APIKeyUpdateFields 声明 APIKeyRepository.Update 允许写回的列。
//
// 与 UserUpdateFields 同理：api_keys 的用量列由计费热路径原子递增
// （IncrementQuotaUsed / IncrementRateLimitUsage 的 quota_used、usage_5h/1d/7d），
// 若编辑 Key 时无条件整行回写，并发累计的配额与限流计数就会被旧快照覆盖。
// 因此调用方必须显式声明要改的列。
type APIKeyUpdateFields struct {
	Name              bool
	Status            bool
	Quota             bool
	GroupID           bool
	AutoGroup         bool
	AutoGroupStrategy bool
	AutoGroupIDs      bool
	ExpiresAt         bool
	// QuotaUsed 仅供"重置配额用量"路径声明；常规计费走 IncrementQuotaUsed。
	QuotaUsed bool
	// RateLimits 覆盖 rate_limit_5h / _1d / _7d 三个阈值。
	RateLimits bool
	// RateLimitUsage 覆盖 usage_5h/_1d/_7d 与三个窗口起点，
	// 仅供"重置限流用量"路径声明；常规计费走 IncrementRateLimitUsage。
	RateLimitUsage bool
	// IPRules 覆盖 ip_whitelist 与 ip_blacklist。
	IPRules bool
}

// IsEmpty 报告该次 Update 是否不写任何列。
func (f APIKeyUpdateFields) IsEmpty() bool {
	return f == APIKeyUpdateFields{}
}

type APIKeyRepository interface {
	Create(ctx context.Context, key *APIKey) error
	GetByID(ctx context.Context, id int64) (*APIKey, error)
	// GetKeyAndOwnerID 仅获取 API Key 的 key 与所有者 ID，用于删除等轻量场景
	GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error)
	GetByKey(ctx context.Context, key string) (*APIKey, error)
	// GetByKeyForAuth 认证专用查询，返回最小字段集
	GetByKeyForAuth(ctx context.Context, key string) (*APIKey, error)
	// Update 只写 fields 中显式声明的列，其余列保持库中当前值。
	Update(ctx context.Context, key *APIKey, fields APIKeyUpdateFields) error
	Delete(ctx context.Context, id int64) error
	// DeleteWithAudit keeps the legacy interface name for rolling-upgrade compatibility.
	// Implementations must tombstone the key and soft-delete it atomically without
	// retaining the deleted credential material.
	DeleteWithAudit(ctx context.Context, id int64) error

	ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error)
	VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error)
	CountByUserID(ctx context.Context, userID int64) (int64, error)
	ExistsByKey(ctx context.Context, key string) (bool, error)
	ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]APIKey, *pagination.PaginationResult, error)
	SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]APIKey, error)
	ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error)
	// UpdateGroupIDByUserAndGroup 将用户下绑定 oldGroupID 的所有 Key 迁移到 newGroupID
	UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error)
	CountByGroupID(ctx context.Context, groupID int64) (int64, error)
	ListKeysByUserID(ctx context.Context, userID int64) ([]string, error)
	ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error)

	// Quota methods
	IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error)
	UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error

	// Rate limit methods
	IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error
	ResetRateLimitWindows(ctx context.Context, id int64) error
	GetRateLimitData(ctx context.Context, id int64) (*APIKeyRateLimitData, error)
}

type apiKeyAllByUserIDLister interface {
	ListAllByUserID(ctx context.Context, userID int64, filters APIKeyListFilters) ([]APIKey, error)
}

type contributionBalanceReader interface {
	GetContributionBalance(ctx context.Context, userID int64) (float64, error)
}

func (s *APIKeyService) GetContributionBalance(ctx context.Context, userID int64) (float64, error) {
	if s == nil || userID <= 0 {
		return 0, nil
	}
	reader, ok := s.apiKeyRepo.(contributionBalanceReader)
	if !ok {
		return 0, nil
	}
	return reader.GetContributionBalance(ctx, userID)
}

// APIKeyRateLimitData holds rate limit usage and window state for an API key.
type APIKeyRateLimitData struct {
	Usage5h       float64
	Usage1d       float64
	Usage7d       float64
	Window5hStart *time.Time
	Window1dStart *time.Time
	Window7dStart *time.Time
}

// EffectiveUsage5h returns the 5h window usage, or 0 if the window has expired.
func (d *APIKeyRateLimitData) EffectiveUsage5h() float64 {
	if IsWindowExpired(d.Window5hStart, RateLimitWindow5h) {
		return 0
	}
	return d.Usage5h
}

// EffectiveUsage1d returns the 1d window usage, or 0 if the window has expired.
func (d *APIKeyRateLimitData) EffectiveUsage1d() float64 {
	if IsWindowExpired(d.Window1dStart, RateLimitWindow1d) {
		return 0
	}
	return d.Usage1d
}

// EffectiveUsage7d returns the 7d window usage, or 0 if the window has expired.
func (d *APIKeyRateLimitData) EffectiveUsage7d() float64 {
	if IsWindowExpired(d.Window7dStart, RateLimitWindow7d) {
		return 0
	}
	return d.Usage7d
}

// APIKeyQuotaUsageState captures the latest quota fields after an atomic quota update.
// It is intentionally small so repositories can return it from a single SQL statement.
type APIKeyQuotaUsageState struct {
	QuotaUsed float64
	Quota     float64
	Key       string
	Status    string
}

// APIKeyCache defines cache operations for API key service
type APIKeyCache interface {
	GetCreateAttemptCount(ctx context.Context, userID int64) (int, error)
	IncrementCreateAttemptCount(ctx context.Context, userID int64) error
	DeleteCreateAttemptCount(ctx context.Context, userID int64) error

	IncrementDailyUsage(ctx context.Context, apiKey string) error
	SetDailyUsageExpiry(ctx context.Context, apiKey string, ttl time.Duration) error

	GetAuthCache(ctx context.Context, key string) (*APIKeyAuthCacheEntry, error)
	SetAuthCache(ctx context.Context, key string, entry *APIKeyAuthCacheEntry, ttl time.Duration) error
	DeleteAuthCache(ctx context.Context, key string) error

	// Pub/Sub for L1 cache invalidation across instances
	PublishAuthCacheInvalidation(ctx context.Context, cacheKey string) error
	SubscribeAuthCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error
}

type authCacheSubscriptionReadyKey struct{}

func withAuthCacheSubscriptionReady(ctx context.Context, ready func()) context.Context {
	return context.WithValue(ctx, authCacheSubscriptionReadyKey{}, ready)
}

// NotifyAuthCacheSubscriptionReady lets cache implementations report that the
// server acknowledged the subscription without widening the public cache API.
func NotifyAuthCacheSubscriptionReady(ctx context.Context) {
	if ready, ok := ctx.Value(authCacheSubscriptionReadyKey{}).(func()); ok && ready != nil {
		ready()
	}
}

// APIKeyAuthCacheInvalidator 提供认证缓存失效能力
type APIKeyAuthCacheInvalidator interface {
	InvalidateAuthCacheByKey(ctx context.Context, key string)
	InvalidateAuthCacheByUserID(ctx context.Context, userID int64)
	InvalidateAuthCacheByGroupID(ctx context.Context, groupID int64)
}

// APIKeyAutoGroupSelectionInvalidator is intentionally separate from auth
// cache invalidation. Balance updates invalidate user auth snapshots on every
// billed request and must not reset a settled automatic-group choice.
type APIKeyAutoGroupSelectionInvalidator interface {
	InvalidateAutoGroupSelectionsByUserID(ctx context.Context, userID int64)
	InvalidateAutoGroupSelectionsByGroupID(ctx context.Context, groupID int64)
}

func invalidateAutoGroupSelectionsForUser(ctx context.Context, invalidator APIKeyAuthCacheInvalidator, userID int64) {
	if autoInvalidator, ok := invalidator.(APIKeyAutoGroupSelectionInvalidator); ok {
		autoInvalidator.InvalidateAutoGroupSelectionsByUserID(ctx, userID)
	}
}

func invalidateAutoGroupSelectionsForGroup(ctx context.Context, invalidator APIKeyAuthCacheInvalidator, groupID int64) {
	if autoInvalidator, ok := invalidator.(APIKeyAutoGroupSelectionInvalidator); ok {
		autoInvalidator.InvalidateAutoGroupSelectionsByGroupID(ctx, groupID)
	}
}

// CreateAPIKeyRequest 创建API Key请求
type CreateAPIKeyRequest struct {
	Name              string   `json:"name"`
	GroupID           *int64   `json:"group_id"`
	AutoGroup         bool     `json:"auto_group"`
	AutoGroupStrategy string   `json:"auto_group_strategy"`
	AutoGroupIDs      []int64  `json:"auto_group_ids"`
	CustomKey         *string  `json:"custom_key"`   // 可选的自定义key
	IPWhitelist       []string `json:"ip_whitelist"` // IP 白名单
	IPBlacklist       []string `json:"ip_blacklist"` // IP 黑名单

	// Quota fields
	Quota         float64 `json:"quota"`           // Quota limit in USD (0 = unlimited)
	ExpiresInDays *int    `json:"expires_in_days"` // Days until expiry (nil = never expires)

	// Rate limit fields (0 = unlimited)
	RateLimit5h float64 `json:"rate_limit_5h"`
	RateLimit1d float64 `json:"rate_limit_1d"`
	RateLimit7d float64 `json:"rate_limit_7d"`
}

const (
	PlaygroundChatAPIKeyName  = "Playground Chat"
	PlaygroundImageAPIKeyName = "Playground Images"
)

// UpdateAPIKeyRequest 更新API Key请求
type UpdateAPIKeyRequest struct {
	Name              *string   `json:"name"`
	GroupID           *int64    `json:"group_id"`
	AutoGroup         *bool     `json:"auto_group"`
	AutoGroupStrategy *string   `json:"auto_group_strategy"`
	AutoGroupIDs      *[]int64  `json:"auto_group_ids"`
	Status            *string   `json:"status"`
	IPWhitelist       *[]string `json:"ip_whitelist"` // IP 白名单（nil 不修改，空数组清空）
	IPBlacklist       *[]string `json:"ip_blacklist"` // IP 黑名单（nil 不修改，空数组清空）

	// Quota fields
	Quota           *float64   `json:"quota"`       // Quota limit in USD (nil = no change, 0 = unlimited)
	ExpiresAt       *time.Time `json:"expires_at"`  // Expiration time (nil = no change)
	ClearExpiration bool       `json:"-"`           // Clear expiration (internal use)
	ResetQuota      *bool      `json:"reset_quota"` // Reset quota_used to 0

	// Rate limit fields (nil = no change, 0 = unlimited)
	RateLimit5h         *float64 `json:"rate_limit_5h"`
	RateLimit1d         *float64 `json:"rate_limit_1d"`
	RateLimit7d         *float64 `json:"rate_limit_7d"`
	ResetRateLimitUsage *bool    `json:"reset_rate_limit_usage"` // Reset all usage counters to 0
}

// APIKeyService API Key服务
// RateLimitCacheInvalidator invalidates rate limit cache entries on manual reset.
type RateLimitCacheInvalidator interface {
	InvalidateAPIKeyRateLimit(ctx context.Context, keyID int64) error
}

type PlaygroundDefaultsProvider interface {
	GetPlaygroundDefaultConfig(ctx context.Context) (PlaygroundDefaultConfig, error)
}

type APIKeyService struct {
	apiKeyRepo                 APIKeyRepository
	userRepo                   UserRepository
	groupRepo                  GroupRepository
	userSubRepo                UserSubscriptionRepository
	userGroupRateRepo          UserGroupRateRepository
	cache                      APIKeyCache
	rateLimitCacheInvalid      RateLimitCacheInvalidator // optional: invalidate Redis rate limit cache
	playgroundDefaultsProvider PlaygroundDefaultsProvider
	concurrencyService         *ConcurrencyService
	autoGroupMetricsRepo       AutoGroupMetricRepository
	autoGroupModelRepo         AutoGroupModelAvailabilityRepository
	autoGroupRuntimeChecker    AutoGroupRuntimeModelChecker
	autoGroupMetricsCache      sync.Map // cache key -> autoGroupMetricsCacheEntry
	autoGroupPeerMetricsCache  sync.Map // cache key -> autoGroupPeerMetricsCacheEntry
	autoGroupMetricsSF         singleflight.Group
	autoGroupPeerMetricsSF     singleflight.Group
	autoGroupSelections        sync.Map // key -> autoGroupSelection
	autoGroupSwitchStates      sync.Map // API key -> autoGroupSwitchState
	autoGroupSelectionMu       sync.Mutex
	autoGroupUserGenerations   map[int64]uint64 // guarded by autoGroupSelectionMu
	autoGroupGroupGenerations  map[int64]uint64 // guarded by autoGroupSelectionMu
	autoGroupAllGroupsEpoch    uint64           // guarded by autoGroupSelectionMu
	autoGroupCacheOperations   atomic.Uint64
	authInvalidationOutbox     AuthCacheInvalidationOutboxRepository
	cfg                        *config.Config
	authCacheL1                *ristretto.Cache
	authNegativeCacheL1        *ristretto.Cache
	authCfg                    apiKeyAuthCacheConfig
	authGroup                  singleflight.Group
	authLookupSlots            chan struct{}
	authLookupTotal            atomic.Uint64
	authLookupRejected         atomic.Uint64
	authLookupInFlight         atomic.Int64
	invalidAuthAbuse           *invalidAuthAbuseLimiter
	authInvalidationStart      sync.Once
	authInvalidationStop       sync.Once
	authInvalidationCancel     context.CancelFunc
	authInvalidationWG         sync.WaitGroup
	authInvalidationConnected  atomic.Bool
	authInvalidationFailures   atomic.Uint64
	lastUsedTouchL1            sync.Map // keyID -> nextAllowedAt(time.Time)
	lastUsedTouchSF            singleflight.Group
}

// AutoGroupMetricRepository provides the narrow, routing-only usage query.
// Keeping it separate avoids coupling unrelated usage-log consumers to this
// feature and keeps existing test doubles small.
type AutoGroupMetricRepository interface {
	ListRecentGroupFirstTokenSamples(ctx context.Context, userID int64, groupIDs []int64, model string, since time.Time, limit int) (map[int64][]int64, error)
}

// AutoGroupModelAvailabilityRepository exposes the persistent account pool
// used to keep automatic model routing inside groups that can serve the
// requested model. It intentionally ignores transient cooldown state; the
// scheduler remains responsible for selecting a currently healthy account.
type AutoGroupModelAvailabilityRepository interface {
	ListModelAvailabilityCandidates(ctx context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]Account, error)
}

type AutoGroupPeerMetricRepository interface {
	ListRecentPeerGroupFirstTokenSamples(ctx context.Context, groupIDs []int64, model string, since time.Time, limit int) (map[int64]map[int64][]int64, error)
}

// AutoGroupRuntimeModelChecker reports whether a group currently has at least
// one account that is schedulable for the requested model, considering runtime
// model-specific rate limits set by HandleUpstreamModelNotFound. Unlike
// AutoGroupModelAvailabilityRepository, this check is state-sensitive and is
// used to skip groups whose entire account pool has been cooled down for the
// model after confirmed upstream rejections.
type AutoGroupRuntimeModelChecker interface {
	HasRuntimeSchedulableAccountForModel(ctx context.Context, groupID int64, platform string, model string) (bool, error)
}

type autoGroupMetricsCacheEntry struct {
	metrics   map[int64][]int64
	expiresAt time.Time
}

type autoGroupPeerMetricsCacheEntry struct {
	metrics   map[int64]map[int64][]int64
	expiresAt time.Time
}

type autoGroupGenerationSnapshot struct {
	selection          uint64
	user               uint64
	allCandidateGroups bool
	allGroupsEpoch     uint64
	groups             map[int64]uint64
	switchRevision     uint64
}

type autoGroupSwitchState struct {
	groupIDs   map[string]int64
	switchedAt []time.Time // healthy-group probing and price-review switch timestamps
	revision   uint64
	expiresAt  time.Time
}

type autoGroupSelection struct {
	userID                  int64
	candidateGroupIDs       []int64
	allCandidateGroups      bool
	groupID                 int64
	probedGroupIDs          []int64
	pendingGroupID          int64
	pendingSince            time.Time
	selectedGroup           *Group
	lastSelectedAt          time.Time
	configFingerprint       string
	needsEvaluation         bool
	eventGroupID            int64
	observedProbeSamples    int
	consecutiveSlowRequests int
	transientFailureStreak  int
	lastTransientFailureAt  time.Time
	failureTriggered        bool
	fallbackUntil           time.Time
	priceReviewAt           time.Time
	settled                 bool
	revision                uint64
	expiresAt               time.Time
}

type APIKeyAuthLookupMetrics struct {
	Total    uint64 `json:"total"`
	Rejected uint64 `json:"rejected"`
	InFlight int64  `json:"in_flight"`
	Capacity int    `json:"capacity"`
}

func (s *APIKeyService) AuthLookupMetrics() APIKeyAuthLookupMetrics {
	if s == nil {
		return APIKeyAuthLookupMetrics{}
	}
	return APIKeyAuthLookupMetrics{
		Total:    s.authLookupTotal.Load(),
		Rejected: s.authLookupRejected.Load(),
		InFlight: s.authLookupInFlight.Load(),
		Capacity: cap(s.authLookupSlots),
	}
}

// NewAPIKeyService 创建API Key服务实例
func NewAPIKeyService(
	apiKeyRepo APIKeyRepository,
	userRepo UserRepository,
	groupRepo GroupRepository,
	userSubRepo UserSubscriptionRepository,
	userGroupRateRepo UserGroupRateRepository,
	cache APIKeyCache,
	cfg *config.Config,
) *APIKeyService {
	svc := &APIKeyService{
		apiKeyRepo:                apiKeyRepo,
		userRepo:                  userRepo,
		groupRepo:                 groupRepo,
		userSubRepo:               userSubRepo,
		userGroupRateRepo:         userGroupRateRepo,
		cache:                     cache,
		cfg:                       cfg,
		autoGroupUserGenerations:  make(map[int64]uint64),
		autoGroupGroupGenerations: make(map[int64]uint64),
	}
	svc.initAuthCache(cfg)
	lookupConcurrency := defaultAuthLookupConcurrency
	if cfg != nil && cfg.APIKeyAuth.LookupConcurrency > 0 {
		lookupConcurrency = cfg.APIKeyAuth.LookupConcurrency
	}
	svc.authLookupSlots = make(chan struct{}, lookupConcurrency)
	svc.invalidAuthAbuse = newInvalidAuthAbuseLimiter(cfg)
	return svc
}

// SetRateLimitCacheInvalidator sets the optional rate limit cache invalidator.
// Called after construction (e.g. in wire) to avoid circular dependencies.
func (s *APIKeyService) SetRateLimitCacheInvalidator(inv RateLimitCacheInvalidator) {
	s.rateLimitCacheInvalid = inv
}

func (s *APIKeyService) SetConcurrencyService(concurrencyService *ConcurrencyService) {
	s.concurrencyService = concurrencyService
}

func (s *APIKeyService) SetPlaygroundDefaultsProvider(provider PlaygroundDefaultsProvider) {
	s.playgroundDefaultsProvider = provider
}

func (s *APIKeyService) SetAutoGroupMetricRepository(repo AutoGroupMetricRepository) {
	s.autoGroupMetricsRepo = repo
}

func (s *APIKeyService) SetAutoGroupModelAvailabilityRepository(repo AutoGroupModelAvailabilityRepository) {
	s.autoGroupModelRepo = repo
}

func (s *APIKeyService) SetAutoGroupRuntimeModelChecker(checker AutoGroupRuntimeModelChecker) {
	s.autoGroupRuntimeChecker = checker
}

func (s *APIKeyService) compileAPIKeyIPRules(apiKey *APIKey) {
	if apiKey == nil {
		return
	}
	apiKey.CompiledIPWhitelist = ip.CompileIPRules(apiKey.IPWhitelist)
	apiKey.CompiledIPBlacklist = ip.CompileIPRules(apiKey.IPBlacklist)
}

// GenerateKey 生成随机API Key
func (s *APIKeyService) GenerateKey() (string, error) {
	// 生成32字节随机数据
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}

	// 转换为十六进制字符串并添加前缀
	prefix := s.cfg.Default.APIKeyPrefix
	if prefix == "" {
		prefix = "sk-"
	}

	key := prefix + hex.EncodeToString(bytes)
	return key, nil
}

// ValidateCustomKey 验证自定义API Key格式
func (s *APIKeyService) ValidateCustomKey(key string) error {
	// 检查长度
	if len(key) < 16 {
		return ErrAPIKeyTooShort
	}

	// 检查字符：只允许字母、数字、下划线、连字符
	for _, c := range key {
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '_' || c == '-' {
			continue
		}
		return ErrAPIKeyInvalidChars
	}

	return nil
}

// checkAPIKeyRateLimit 检查用户创建自定义Key的错误次数是否超限
func (s *APIKeyService) checkAPIKeyRateLimit(ctx context.Context, userID int64) error {
	if s.cache == nil {
		return nil
	}

	count, err := s.cache.GetCreateAttemptCount(ctx, userID)
	if err != nil {
		// Redis 出错时不阻止用户操作
		return nil
	}

	if count >= apiKeyMaxErrorsPerHour {
		return ErrAPIKeyRateLimited
	}

	return nil
}

// incrementAPIKeyErrorCount 增加用户创建自定义Key的错误计数
func (s *APIKeyService) incrementAPIKeyErrorCount(ctx context.Context, userID int64) {
	if s.cache == nil {
		return
	}

	_ = s.cache.IncrementCreateAttemptCount(ctx, userID)
}

// canUserBindGroup 检查用户是否可以绑定指定分组
// 对于订阅类型分组：检查用户是否有有效订阅
// 对于标准类型分组：使用原有的 AllowedGroups 和 IsExclusive 逻辑
func (s *APIKeyService) canUserBindGroup(ctx context.Context, user *User, group *Group) bool {
	// 订阅类型分组：需要有效订阅
	if group.IsSubscriptionType() {
		_, err := s.userSubRepo.GetActiveByUserIDAndGroupID(ctx, user.ID, group.ID)
		return err == nil // 有有效订阅则允许
	}
	// 标准类型分组：使用原有逻辑
	return user.CanBindGroup(group.ID, group.IsExclusive)
}

// Create 创建API Key
func (s *APIKeyService) Create(ctx context.Context, userID int64, req CreateAPIKeyRequest) (*APIKey, error) {
	// 验证用户存在
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	// 验证 IP 白名单格式
	if len(req.IPWhitelist) > 0 {
		if invalid := ip.ValidateIPPatterns(req.IPWhitelist); len(invalid) > 0 {
			return nil, fmt.Errorf("%w: %v", ErrInvalidIPPattern, invalid)
		}
	}

	// 验证 IP 黑名单格式
	if len(req.IPBlacklist) > 0 {
		if invalid := ip.ValidateIPPatterns(req.IPBlacklist); len(invalid) > 0 {
			return nil, fmt.Errorf("%w: %v", ErrInvalidIPPattern, invalid)
		}
	}

	// 验证分组权限（如果指定了分组）
	if req.AutoGroup {
		req.GroupID = nil
		if err := s.validateAutoGroupIDs(ctx, userID, req.AutoGroupIDs); err != nil {
			return nil, err
		}
	}
	if req.GroupID != nil {
		group, err := s.groupRepo.GetByID(ctx, *req.GroupID)
		if err != nil {
			return nil, fmt.Errorf("get group: %w", err)
		}

		// 检查用户是否可以绑定该分组
		if !s.canUserBindGroup(ctx, user, group) {
			return nil, ErrGroupNotAllowed
		}
	}

	var key string

	// 判断是否使用自定义Key
	if req.CustomKey != nil && *req.CustomKey != "" {
		// 检查限流（仅对自定义key进行限流）
		if err := s.checkAPIKeyRateLimit(ctx, userID); err != nil {
			return nil, err
		}

		// 验证自定义Key格式
		if err := s.ValidateCustomKey(*req.CustomKey); err != nil {
			return nil, err
		}

		// 检查Key是否已存在
		exists, err := s.apiKeyRepo.ExistsByKey(ctx, *req.CustomKey)
		if err != nil {
			return nil, fmt.Errorf("check key exists: %w", err)
		}
		if exists {
			// Key已存在，增加错误计数
			s.incrementAPIKeyErrorCount(ctx, userID)
			return nil, ErrAPIKeyExists
		}

		key = *req.CustomKey
	} else {
		// 生成随机API Key
		var err error
		key, err = s.GenerateKey()
		if err != nil {
			return nil, fmt.Errorf("generate key: %w", err)
		}
	}

	// 创建API Key记录
	apiKey := &APIKey{
		UserID:            userID,
		Key:               key,
		Name:              html.EscapeString(req.Name),
		GroupID:           req.GroupID,
		AutoGroup:         req.AutoGroup,
		AutoGroupStrategy: normalizeAutoGroupStrategy(req.AutoGroupStrategy),
		AutoGroupIDs:      normalizeAutoGroupIDs(req.AutoGroupIDs),
		Status:            StatusActive,
		IPWhitelist:       req.IPWhitelist,
		IPBlacklist:       req.IPBlacklist,
		Quota:             req.Quota,
		QuotaUsed:         0,
		RateLimit5h:       req.RateLimit5h,
		RateLimit1d:       req.RateLimit1d,
		RateLimit7d:       req.RateLimit7d,
	}

	// Set expiration time if specified
	if req.ExpiresInDays != nil && *req.ExpiresInDays > 0 {
		expiresAt := time.Now().AddDate(0, 0, *req.ExpiresInDays)
		apiKey.ExpiresAt = &expiresAt
	}

	if err := s.apiKeyRepo.Create(ctx, apiKey); err != nil {
		return nil, fmt.Errorf("create api key: %w", err)
	}

	s.InvalidateAuthCacheByKey(ctx, apiKey.Key)
	s.compileAPIKeyIPRules(apiKey)

	return apiKey, nil
}

// EnsurePlaygroundAPIKeys creates the keys used as the initial playground
// selections. Existing purpose-bound keys are preserved so retries do not
// create duplicates or replace a user's working credentials.
func (s *APIKeyService) EnsurePlaygroundAPIKeys(ctx context.Context, userID int64) error {
	if s == nil || userID <= 0 || s.apiKeyRepo == nil {
		return nil
	}

	existing, err := s.SearchAPIKeys(ctx, userID, "Playground", 10)
	if err != nil {
		return fmt.Errorf("find existing playground api keys: %w", err)
	}
	created := make(map[string]struct{}, len(existing))
	for _, key := range existing {
		created[strings.TrimSpace(key.Name)] = struct{}{}
	}

	groups, err := s.GetAvailableGroups(ctx, userID)
	if err != nil {
		return fmt.Errorf("list available playground groups: %w", err)
	}
	defaults := PlaygroundDefaultConfig{
		ChatModel:     "gpt-5.4",
		ImageModel:    "gpt-image-2",
		ChatStrategy:  autoGroupStrategyPrice,
		ImageStrategy: autoGroupStrategyPrice,
	}
	if s.playgroundDefaultsProvider != nil {
		if configured, configErr := s.playgroundDefaultsProvider.GetPlaygroundDefaultConfig(ctx); configErr == nil {
			defaults = configured
		}
	}
	chatGroupIDs := selectPlaygroundGroupIDs(groups, defaults.ChatGroupIDs, false)
	imageGroupIDs := selectPlaygroundGroupIDs(groups, defaults.ImageGroupIDs, true)

	if _, exists := created[PlaygroundChatAPIKeyName]; !exists && len(chatGroupIDs) > 0 {
		if _, err := s.Create(ctx, userID, CreateAPIKeyRequest{
			Name:              PlaygroundChatAPIKeyName,
			AutoGroup:         true,
			AutoGroupStrategy: normalizeAutoGroupStrategy(defaults.ChatStrategy),
			AutoGroupIDs:      chatGroupIDs,
		}); err != nil {
			return fmt.Errorf("create playground chat api key: %w", err)
		}
	}
	if _, exists := created[PlaygroundImageAPIKeyName]; !exists && len(imageGroupIDs) > 0 {
		if _, err := s.Create(ctx, userID, CreateAPIKeyRequest{
			Name:              PlaygroundImageAPIKeyName,
			AutoGroup:         true,
			AutoGroupStrategy: normalizeAutoGroupStrategy(defaults.ImageStrategy),
			AutoGroupIDs:      imageGroupIDs,
		}); err != nil {
			return fmt.Errorf("create playground image api key: %w", err)
		}
	}
	return nil
}

func selectPlaygroundGroupIDs(groups []Group, configuredIDs []int64, imageOnly bool) []int64 {
	eligible := make(map[int64]struct{}, len(groups))
	for _, group := range groups {
		if group.Platform != PlatformOpenAI || (imageOnly && !group.AllowImageGeneration) {
			continue
		}
		eligible[group.ID] = struct{}{}
	}
	configured := normalizeAutoGroupIDs(configuredIDs)
	if len(configured) > 0 {
		selected := make([]int64, 0, len(configured))
		for _, groupID := range configured {
			if _, ok := eligible[groupID]; ok {
				selected = append(selected, groupID)
			}
		}
		return selected
	}
	selected := make([]int64, 0, len(eligible))
	for _, group := range groups {
		if _, ok := eligible[group.ID]; ok {
			selected = append(selected, group.ID)
		}
	}
	return normalizeAutoGroupIDs(selected)
}

func selectPlaygroundGroups(groups []Group) (chatGroup, imageGroup *Group) {
	var firstOpenAI *Group
	for i := range groups {
		group := &groups[i]
		if group.Platform != PlatformOpenAI {
			continue
		}
		if firstOpenAI == nil {
			firstOpenAI = group
		}
		if chatGroup == nil && !group.AllowImageGeneration {
			chatGroup = group
		}
		if imageGroup == nil && group.AllowImageGeneration {
			imageGroup = group
		}
	}
	if chatGroup == nil {
		chatGroup = firstOpenAI
	}
	return chatGroup, imageGroup
}

// List 获取用户的API Key列表
func (s *APIKeyService) List(ctx context.Context, userID int64, params pagination.PaginationParams, filters APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	if normalizedAPIKeySortBy(params.SortBy) == apiKeySortCurrentConcurrency {
		return s.listByCurrentConcurrency(ctx, userID, params, filters)
	}

	keys, pagination, err := s.apiKeyRepo.ListByUserID(ctx, userID, params, filters)
	if err != nil {
		return nil, nil, fmt.Errorf("list api keys: %w", err)
	}
	s.decorateAutoGroupCurrentSelections(keys)
	s.fillCurrentConcurrency(ctx, keys)
	return keys, pagination, nil
}

func (s *APIKeyService) listByCurrentConcurrency(ctx context.Context, userID int64, params pagination.PaginationParams, filters APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	repo, ok := s.apiKeyRepo.(apiKeyAllByUserIDLister)
	if !ok {
		return nil, nil, fmt.Errorf("list api keys by current concurrency: repository does not support unpaginated API key listing")
	}

	keys, err := repo.ListAllByUserID(ctx, userID, filters)
	if err != nil {
		return nil, nil, fmt.Errorf("list api keys: %w", err)
	}
	s.decorateAutoGroupCurrentSelections(keys)
	s.fillCurrentConcurrency(ctx, keys)
	sortAPIKeysByCurrentConcurrency(keys, params.NormalizedSortOrder(pagination.SortOrderDesc))
	return paginateAPIKeys(keys, params), apiKeyPaginationResult(int64(len(keys)), params), nil
}

// decorateAutoGroupCurrentSelections attaches the most recently selected
// runtime group to automatic keys for the user-facing key list. The selected
// group is deliberately kept separate from APIKey.Group: Group is the
// persisted fixed binding, while automatic routing can have a different
// selection for each model.
func (s *APIKeyService) decorateAutoGroupCurrentSelections(keys []APIKey) {
	if s == nil || len(keys) == 0 {
		return
	}
	byID := make(map[int64]*APIKey, len(keys))
	for i := range keys {
		if keys[i].AutoGroup {
			byID[keys[i].ID] = &keys[i]
		}
	}
	if len(byID) == 0 {
		return
	}

	type runtimeSelection struct {
		group *Group
		model string
		at    time.Time
	}
	latest := make(map[int64]runtimeSelection, len(byID))
	s.autoGroupSelections.Range(func(rawKey, rawValue any) bool {
		key, ok := rawKey.(string)
		if !ok {
			return true
		}
		separator := strings.IndexByte(key, ':')
		if separator <= 0 {
			return true
		}
		keyID, err := strconv.ParseInt(key[:separator], 10, 64)
		if err != nil {
			return true
		}
		apiKey, ok := byID[keyID]
		if !ok {
			return true
		}
		selection, ok := rawValue.(autoGroupSelection)
		if !ok || selection.selectedGroup == nil ||
			selection.configFingerprint != autoGroupConfigFingerprint(apiKey) {
			return true
		}
		// Older in-memory entries created before lastSelectedAt was introduced
		// have no reliable ordering and must not produce a misleading display.
		if selection.lastSelectedAt.IsZero() {
			return true
		}
		current, exists := latest[keyID]
		if exists && !selection.lastSelectedAt.After(current.at) {
			return true
		}
		model := ""
		if modelStart := strings.IndexByte(key[separator+1:], ':'); modelStart >= 0 {
			model = key[separator+1+modelStart+1:]
		}
		groupSnapshot := *selection.selectedGroup
		selectedAt := selection.lastSelectedAt
		latest[keyID] = runtimeSelection{group: &groupSnapshot, model: model, at: selectedAt}
		return true
	})

	for keyID, current := range latest {
		apiKey := byID[keyID]
		apiKey.AutoGroupCurrentGroup = current.group
		apiKey.AutoGroupCurrentModel = current.model
		if !current.at.IsZero() {
			selectedAt := current.at
			apiKey.AutoGroupCurrentSelectedAt = &selectedAt
		}
	}
}

func normalizedAPIKeySortBy(sortBy string) string {
	return strings.ToLower(strings.TrimSpace(sortBy))
}

func sortAPIKeysByCurrentConcurrency(keys []APIKey, sortOrder string) {
	desc := sortOrder != pagination.SortOrderAsc
	sort.SliceStable(keys, func(i, j int) bool {
		if keys[i].CurrentConcurrency == keys[j].CurrentConcurrency {
			if desc {
				return keys[i].ID > keys[j].ID
			}
			return keys[i].ID < keys[j].ID
		}
		if desc {
			return keys[i].CurrentConcurrency > keys[j].CurrentConcurrency
		}
		return keys[i].CurrentConcurrency < keys[j].CurrentConcurrency
	})
}

func paginateAPIKeys(keys []APIKey, params pagination.PaginationParams) []APIKey {
	if len(keys) == 0 {
		return []APIKey{}
	}
	limit := params.Limit()
	page := params.Page
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * limit
	if offset >= len(keys) {
		return []APIKey{}
	}
	end := offset + limit
	if end > len(keys) {
		end = len(keys)
	}
	return keys[offset:end]
}

func apiKeyPaginationResult(total int64, params pagination.PaginationParams) *pagination.PaginationResult {
	limit := params.Limit()
	pages := int(total) / limit
	if int(total)%limit > 0 {
		pages++
	}
	return &pagination.PaginationResult{
		Total:    total,
		Page:     params.Page,
		PageSize: limit,
		Pages:    pages,
	}
}

func (s *APIKeyService) fillCurrentConcurrency(ctx context.Context, keys []APIKey) {
	if s == nil || s.concurrencyService == nil || len(keys) == 0 {
		return
	}
	ids := make([]int64, 0, len(keys))
	for i := range keys {
		if keys[i].ID > 0 {
			ids = append(ids, keys[i].ID)
		}
	}
	counts, err := s.concurrencyService.GetAPIKeyConcurrencyBatch(ctx, ids)
	if err != nil {
		return
	}
	for i := range keys {
		keys[i].CurrentConcurrency = counts[keys[i].ID]
	}
}

func (s *APIKeyService) currentConcurrencyForAPIKey(ctx context.Context, apiKeyID int64) int {
	if s == nil || s.concurrencyService == nil || apiKeyID <= 0 {
		return 0
	}
	counts, err := s.concurrencyService.GetAPIKeyConcurrencyBatch(ctx, []int64{apiKeyID})
	if err != nil {
		return 0
	}
	return counts[apiKeyID]
}

func (s *APIKeyService) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	if len(apiKeyIDs) == 0 {
		return []int64{}, nil
	}

	validIDs, err := s.apiKeyRepo.VerifyOwnership(ctx, userID, apiKeyIDs)
	if err != nil {
		return nil, fmt.Errorf("verify api key ownership: %w", err)
	}
	return validIDs, nil
}

// GetByID 根据ID获取API Key
func (s *APIKeyService) GetByID(ctx context.Context, id int64) (*APIKey, error) {
	apiKey, err := s.apiKeyRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get api key: %w", err)
	}
	s.compileAPIKeyIPRules(apiKey)
	if apiKey != nil {
		apiKey.CurrentConcurrency = s.currentConcurrencyForAPIKey(ctx, apiKey.ID)
	}
	return apiKey, nil
}

// GetByIDForAuth loads a user-owned key for a request path that authenticates
// by key ID (for example the panel playground). Automatic keys do not persist
// a fixed group, so resolve a safe cold-start group on the request copy before
// downstream middleware checks platform and group availability.
func (s *APIKeyService) GetByIDForAuth(ctx context.Context, id int64) (*APIKey, error) {
	apiKey, err := s.GetByID(ctx, id)
	if err != nil || apiKey == nil || !apiKey.AutoGroup {
		return apiKey, err
	}
	resolved, err := s.finalizeAPIKeyForAuth(ctx, apiKey)
	if err != nil {
		return nil, err
	}
	s.compileAPIKeyIPRules(resolved)
	return resolved, nil
}

// GetByKey 根据Key字符串获取API Key（用于认证）
func (s *APIKeyService) GetByKey(ctx context.Context, key string) (*APIKey, error) {
	if len(key) == 0 || len(key) > MaxAPIKeyCredentialBytes {
		return nil, ErrAPIKeyNotFound
	}
	cacheKey := s.authCacheKey(key)

	if entry, ok := s.getAuthCacheEntry(ctx, cacheKey); ok {
		if apiKey, used, err := s.applyAuthCacheEntry(key, entry); used {
			if err != nil {
				return nil, fmt.Errorf("get api key: %w", err)
			}
			apiKey, err = s.finalizeAPIKeyForAuth(ctx, apiKey)
			if err != nil {
				return nil, fmt.Errorf("get api key: %w", err)
			}
			s.compileAPIKeyIPRules(apiKey)
			return apiKey, nil
		}
	}

	if s.authCfg.singleflight {
		value, err, _ := s.authGroup.Do(cacheKey, func() (any, error) {
			return s.loadAuthCacheEntry(ctx, key, cacheKey)
		})
		if err != nil {
			return nil, err
		}
		entry, _ := value.(*APIKeyAuthCacheEntry)
		if apiKey, used, err := s.applyAuthCacheEntry(key, entry); used {
			if err != nil {
				return nil, fmt.Errorf("get api key: %w", err)
			}
			apiKey, err = s.finalizeAPIKeyForAuth(ctx, apiKey)
			if err != nil {
				return nil, fmt.Errorf("get api key: %w", err)
			}
			s.compileAPIKeyIPRules(apiKey)
			return apiKey, nil
		}
	} else {
		entry, err := s.loadAuthCacheEntry(ctx, key, cacheKey)
		if err != nil {
			return nil, err
		}
		if apiKey, used, err := s.applyAuthCacheEntry(key, entry); used {
			if err != nil {
				return nil, fmt.Errorf("get api key: %w", err)
			}
			apiKey, err = s.finalizeAPIKeyForAuth(ctx, apiKey)
			if err != nil {
				return nil, fmt.Errorf("get api key: %w", err)
			}
			s.compileAPIKeyIPRules(apiKey)
			return apiKey, nil
		}
	}

	apiKey, err := s.lookupAPIKeyForAuth(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get api key: %w", err)
	}
	apiKey.Key = key
	apiKey, err = s.finalizeAPIKeyForAuth(ctx, apiKey)
	if err != nil {
		return nil, fmt.Errorf("get api key: %w", err)
	}
	s.compileAPIKeyIPRules(apiKey)
	return apiKey, nil
}

// finalizeAPIKeyForAuth resolves the persisted Auto mode for this request.
// The selected group is intentionally kept only on the request copy so group
// availability and user-specific rates are re-evaluated on every request.
func (s *APIKeyService) finalizeAPIKeyForAuth(ctx context.Context, apiKey *APIKey) (*APIKey, error) {
	if apiKey == nil || !apiKey.AutoGroup {
		return apiKey, nil
	}

	groups, err := s.GetAvailableGroups(ctx, apiKey.UserID)
	if err != nil {
		return nil, fmt.Errorf("list available groups for auto mode: %w", err)
	}
	groups = filterAutoGroupCandidates(groups, apiKey.AutoGroupIDs)
	rates, err := s.GetUserGroupRates(ctx, apiKey.UserID)
	if err != nil {
		return nil, fmt.Errorf("get user group rates for auto mode: %w", err)
	}
	selected := lowestAvailableRateGroup(groups, rates)
	if selected == nil {
		return nil, ErrAutoGroupUnavailable
	}

	resolved := *apiKey
	groupID := selected.ID
	resolved.GroupID = &groupID
	resolved.Group = selected
	return &resolved, nil
}

// ResolveAutoGroupForModel re-selects an automatic group once the request
// model is available. Authentication uses the cheapest currently usable group
// as a safe cold-start choice; handlers call this method before routing so the
// final choice can use model-specific latency data.
func (s *APIKeyService) ResolveAutoGroupForModel(ctx context.Context, apiKey *APIKey, model string) (*APIKey, error) {
	if apiKey == nil || !apiKey.AutoGroup {
		return apiKey, nil
	}
	model = strings.TrimSpace(model)
	selectionKey := autoGroupSelectionKey(apiKey, model)
	switchKey := autoGroupSwitchStateKey(apiKey)
	configFingerprint := autoGroupConfigFingerprint(apiKey)
	for attempt := 0; attempt < autoGroupResolveMaxAttempts; attempt++ {
		current, generation := s.autoGroupSelectionSnapshot(selectionKey, switchKey, apiKey.UserID, apiKey.AutoGroupIDs)
		now := time.Now()
		if current.configFingerprint != "" && current.configFingerprint != configFingerprint {
			current = autoGroupSelection{}
		}
		if current.selectedGroup != nil && current.configFingerprint == configFingerprint {
			// Validate the settled choice against the same account-level capability
			// and runtime model-cooldown filters used during a full selection. A
			// group can retain the right image flag while its account pool no longer
			// supports this model; the fast path must not reuse that stale choice.
			eligible, filterErr := s.filterAutoGroupCandidatesForModel(ctx, []Group{*current.selectedGroup}, model)
			if filterErr != nil {
				return nil, fmt.Errorf("filter automatic groups for model %q: %w", model, filterErr)
			}
			if len(eligible) == 0 {
				current = autoGroupSelection{}
			}
		}
		if current.selectedGroup != nil && current.configFingerprint == configFingerprint && !current.needsEvaluation {
			fallbackCoolingDown := !current.fallbackUntil.IsZero() && now.Before(current.fallbackUntil)
			priceReviewDue := current.priceReviewAt.IsZero() || !now.Before(current.priceReviewAt)
			if current.settled && (current.fallbackUntil.IsZero() || fallbackCoolingDown) && !priceReviewDue {
				return resolveAPIKeyWithAutoGroup(apiKey, current.selectedGroup), nil
			}
			probeStillPending := current.pendingGroupID != 0 && !current.pendingSince.IsZero() &&
				now.Before(current.pendingSince.Add(autoGroupProbeWaitTimeout))
			if probeStillPending {
				return resolveAPIKeyWithAutoGroup(apiKey, current.selectedGroup), nil
			}
		}

		fallbackCoolingDown := !current.fallbackUntil.IsZero() && now.Before(current.fallbackUntil)
		priceReviewOnly := current.selectedGroup != nil && current.configFingerprint == configFingerprint &&
			!current.needsEvaluation && current.settled &&
			(current.fallbackUntil.IsZero() || fallbackCoolingDown) &&
			(current.priceReviewAt.IsZero() || !now.Before(current.priceReviewAt))

		groups, err := s.GetAvailableGroups(ctx, apiKey.UserID)
		if err != nil {
			if priceReviewOnly {
				if resolved, stored := s.deferAutoGroupPriceReview(apiKey, selectionKey, switchKey, current, nil, autoGroupPriceReviewRetry, generation); stored {
					return resolved, nil
				}
				continue
			}
			return nil, fmt.Errorf("list available groups for auto mode: %w", err)
		}
		groups = filterAutoGroupCandidates(groups, apiKey.AutoGroupIDs)
		groups, err = s.filterAutoGroupCandidatesForModel(ctx, groups, model)
		if err != nil {
			return nil, fmt.Errorf("filter automatic groups for model %q: %w", model, err)
		}
		if len(groups) == 0 {
			return nil, ErrAutoGroupUnavailable
		}
		rates, err := s.GetUserGroupRates(ctx, apiKey.UserID)
		if err != nil {
			if priceReviewOnly {
				if resolved, stored := s.deferAutoGroupPriceReview(apiKey, selectionKey, switchKey, current, nil, autoGroupPriceReviewRetry, generation); stored {
					return resolved, nil
				}
				continue
			}
			return nil, fmt.Errorf("get user group rates for auto mode: %w", err)
		}
		if priceReviewOnly {
			currentGroup := findAvailableAutoGroup(groups, current.groupID)
			if currentGroup != nil {
				peerCandidate, reviewErr := s.peerVerifiedCheaperAutoGroup(ctx, apiKey.UserID, groups, rates, current.groupID, model)
				if reviewErr != nil {
					if resolved, stored := s.deferAutoGroupPriceReview(apiKey, selectionKey, switchKey, current, currentGroup, autoGroupPriceReviewRetry, generation); stored {
						return resolved, nil
					}
					continue
				}
				if peerCandidate == nil {
					if resolved, stored := s.deferAutoGroupPriceReview(apiKey, selectionKey, switchKey, current, currentGroup, autoGroupPriceReviewInterval, generation); stored {
						return resolved, nil
					}
					continue
				}
			}
		}
		strategy := normalizeAutoGroupStrategy(apiKey.AutoGroupStrategy)
		metrics := map[int64][]int64(nil)
		if strategy != autoGroupStrategyPrice {
			metrics, err = s.recentAutoGroupMetrics(ctx, apiKey.UserID, groups, model)
			if err != nil {
				return nil, fmt.Errorf("load auto group metrics: %w", err)
			}
		}
		if current.needsEvaluation && current.eventGroupID != 0 {
			metrics = cloneAutoGroupMetrics(metrics)
			metrics[current.eventGroupID] = []int64{
				autoGroupMaxP95FirstToken.Milliseconds() + 1,
				autoGroupMaxP95FirstToken.Milliseconds() + 1,
				autoGroupMaxP95FirstToken.Milliseconds() + 1,
			}
			current.settled = false
		}
		selected, nextSelection, err := selectStableAutoGroup(groups, rates, metrics, strategy, current)
		if err != nil {
			return nil, err
		}
		if selected == nil {
			return nil, ErrAutoGroupUnavailable
		}
		selectedSnapshot := *selected
		nextSelection.selectedGroup = &selectedSnapshot
		nextSelection.configFingerprint = configFingerprint
		nextSelection.userID = apiKey.UserID
		nextSelection.candidateGroupIDs = normalizeAutoGroupIDs(apiKey.AutoGroupIDs)
		nextSelection.allCandidateGroups = len(nextSelection.candidateGroupIDs) == 0
		nextSelection.needsEvaluation = false
		nextSelection.eventGroupID = 0
		nextSelection.observedProbeSamples = 0
		nextSelection.consecutiveSlowRequests = 0
		nextSelection.transientFailureStreak = 0
		nextSelection.lastTransientFailureAt = time.Time{}
		nextSelection.failureTriggered = false
		if nextSelection.settled {
			nextSelection.priceReviewAt = time.Now().Add(autoGroupPriceReviewInterval)
		} else {
			nextSelection.priceReviewAt = time.Time{}
		}
		committed, stored := s.commitAutoGroupSelectionIfCurrent(selectionKey, switchKey, model, apiKey.UserID, generation, current, nextSelection, groups, time.Now())
		if !stored {
			continue
		}

		return resolveAPIKeyWithAutoGroup(apiKey, committed.selectedGroup), nil
	}
	return nil, fmt.Errorf("automatic group selection changed repeatedly while resolving")
}

// ResolveAutoGroupForModelExcluding resolves the next automatic candidate for
// the current request after one or more groups have already failed. The normal
// resolver intentionally reuses a settled choice; that is correct between
// requests, but would keep an exhausted group pinned for the remainder of the
// current request. Each excluded candidate is observed as a hard failure so
// the persisted in-memory selection advances through the same state machine as
// the normal post-request observer. The exclusion set is request-local and is
// never persisted on the API key itself.
func (s *APIKeyService) ResolveAutoGroupForModelExcluding(ctx context.Context, apiKey *APIKey, model string, excludedGroupIDs map[int64]struct{}) (*APIKey, error) {
	if apiKey == nil || !apiKey.AutoGroup || len(excludedGroupIDs) == 0 {
		return s.ResolveAutoGroupForModel(ctx, apiKey, model)
	}

	// Failed groups must be removed before ranking candidates. The previous
	// retry loop allowed the price strategy to pick an already failed cheap
	// group again, which could exhaust the loop before a viable higher-priced
	// image group was ever considered.
	working := *apiKey
	if apiKey.AutoGroupIDs == nil {
		available, err := s.GetAvailableGroups(ctx, apiKey.UserID)
		if err != nil {
			return nil, fmt.Errorf("list available groups for auto mode: %w", err)
		}
		working.AutoGroupIDs = availableAutoGroupIDsExcluding(available, excludedGroupIDs)
	} else {
		working.AutoGroupIDs = autoGroupIDsExcluding(apiKey.AutoGroupIDs, excludedGroupIDs)
	}
	if len(working.AutoGroupIDs) == 0 {
		return nil, ErrAutoGroupUnavailable
	}

	// The native account scheduler has exhausted every candidate in the current
	// group for this request. Force the full-candidate snapshot to re-evaluate
	// before the next request while this call resolves only among the remaining
	// groups.
	s.markAutoGroupSelectionForImmediateFailure(apiKey, model)
	resolved, err := s.ResolveAutoGroupForModel(ctx, &working, model)
	if err != nil {
		return nil, err
	}
	if resolved == nil || resolved.GroupID == nil {
		return nil, ErrAutoGroupUnavailable
	}
	if _, excluded := excludedGroupIDs[*resolved.GroupID]; excluded {
		return nil, ErrAutoGroupUnavailable
	}
	return resolved, nil
}

func autoGroupIDsExcluding(groupIDs []int64, excluded map[int64]struct{}) []int64 {
	candidates := normalizeAutoGroupIDs(groupIDs)
	remaining := make([]int64, 0, len(candidates))
	for _, groupID := range candidates {
		if _, failed := excluded[groupID]; !failed {
			remaining = append(remaining, groupID)
		}
	}
	return remaining
}

func availableAutoGroupIDsExcluding(groups []Group, excluded map[int64]struct{}) []int64 {
	remaining := make([]int64, 0, len(groups))
	for _, group := range groups {
		if _, failed := excluded[group.ID]; !failed {
			remaining = append(remaining, group.ID)
		}
	}
	return normalizeAutoGroupIDs(remaining)
}

// markAutoGroupSelectionForImmediateFailure records a request-local hard
// failure without changing any account state. It is used only after the
// native account scheduler has exhausted the current group's candidates.
func (s *APIKeyService) markAutoGroupSelectionForImmediateFailure(apiKey *APIKey, model string) bool {
	if s == nil || apiKey == nil || !apiKey.AutoGroup {
		return false
	}
	key := autoGroupSelectionKey(apiKey, model)
	s.autoGroupSelectionMu.Lock()
	defer s.autoGroupSelectionMu.Unlock()

	value, ok := s.autoGroupSelections.Load(key)
	selection, valid := value.(autoGroupSelection)
	if !ok || !valid || selection.configFingerprint != autoGroupConfigFingerprint(apiKey) {
		return false
	}
	if selection.groupID == 0 && apiKey.GroupID != nil {
		selection.groupID = *apiKey.GroupID
	}
	if selection.groupID == 0 {
		return false
	}

	now := time.Now()
	selection.needsEvaluation = true
	selection.eventGroupID = selection.groupID
	selection.failureTriggered = true
	selection.transientFailureStreak = autoGroupTransientFailureThreshold
	selection.lastTransientFailureAt = now
	selection.pendingGroupID = 0
	selection.pendingSince = time.Time{}
	selection.observedProbeSamples = 0
	selection.consecutiveSlowRequests = 0
	selection.revision++
	selection.expiresAt = now.Add(autoGroupSelectionTTL)
	s.autoGroupSelections.Store(key, selection)
	return true
}

func (s *APIKeyService) deferAutoGroupPriceReview(apiKey *APIKey, selectionKey, switchKey string, current autoGroupSelection, currentGroup *Group, delay time.Duration, generation autoGroupGenerationSnapshot) (*APIKey, bool) {
	if currentGroup != nil {
		groupSnapshot := *currentGroup
		current.selectedGroup = &groupSnapshot
	}
	current.priceReviewAt = time.Now().Add(delay)
	if !s.storeAutoGroupSelectionIfCurrent(selectionKey, switchKey, apiKey.UserID, generation, current) {
		return nil, false
	}
	return resolveAPIKeyWithAutoGroup(apiKey, current.selectedGroup), true
}

func cloneAutoGroupMetrics(metrics map[int64][]int64) map[int64][]int64 {
	cloned := make(map[int64][]int64, len(metrics)+1)
	for groupID, samples := range metrics {
		cloned[groupID] = append([]int64(nil), samples...)
	}
	return cloned
}

func resolveAPIKeyWithAutoGroup(apiKey *APIKey, group *Group) *APIKey {
	if apiKey == nil || group == nil {
		return apiKey
	}
	resolved := *apiKey
	groupSnapshot := *group
	groupID := groupSnapshot.ID
	resolved.GroupID = &groupID
	resolved.Group = &groupSnapshot
	return &resolved
}

func (s *APIKeyService) recentAutoGroupMetrics(ctx context.Context, userID int64, groups []Group, model string) (map[int64][]int64, error) {
	model = strings.TrimSpace(model)
	if s == nil || s.autoGroupMetricsRepo == nil || model == "" {
		return nil, nil
	}
	s.maybeSweepAutoGroupCaches(time.Now())
	groupIDs := make([]int64, 0, len(groups))
	for _, group := range groups {
		if group.ActiveAccountCount > 0 {
			groupIDs = append(groupIDs, group.ID)
		}
	}
	if len(groupIDs) == 0 {
		return nil, nil
	}
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })
	keyParts := make([]string, 0, len(groupIDs)+2)
	keyParts = append(keyParts, strconv.FormatInt(userID, 10), model)
	for _, id := range groupIDs {
		keyParts = append(keyParts, strconv.FormatInt(id, 10))
	}
	cacheKey := strings.Join(keyParts, ":")
	if metrics, ok := s.cachedAutoGroupMetrics(cacheKey, time.Now()); ok {
		return metrics, nil
	}

	value, err, _ := s.autoGroupMetricsSF.Do(cacheKey, func() (any, error) {
		now := time.Now()
		if metrics, ok := s.cachedAutoGroupMetrics(cacheKey, now); ok {
			return metrics, nil
		}
		metrics, queryErr := s.autoGroupMetricsRepo.ListRecentGroupFirstTokenSamples(ctx, userID, groupIDs, model, now.Add(-autoGroupMetricWindow), autoGroupSampleLimit)
		if queryErr != nil {
			return nil, queryErr
		}
		s.autoGroupMetricsCache.Store(cacheKey, autoGroupMetricsCacheEntry{metrics: metrics, expiresAt: now.Add(autoGroupMetricCacheTTL)})
		return metrics, nil
	})
	if err != nil {
		return nil, err
	}
	metrics, _ := value.(map[int64][]int64)
	return metrics, nil
}

func (s *APIKeyService) recentPeerAutoGroupMetrics(ctx context.Context, excludeUserID int64, groups []Group, model string) (map[int64][]int64, error) {
	model = strings.TrimSpace(model)
	if s == nil || s.autoGroupMetricsRepo == nil || model == "" {
		return nil, nil
	}
	s.maybeSweepAutoGroupCaches(time.Now())
	peerRepo, ok := s.autoGroupMetricsRepo.(AutoGroupPeerMetricRepository)
	if !ok {
		return nil, nil
	}
	groupIDs := make([]int64, 0, len(groups))
	for _, group := range groups {
		if group.ActiveAccountCount > 0 {
			groupIDs = append(groupIDs, group.ID)
		}
	}
	if len(groupIDs) == 0 {
		return nil, nil
	}
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })
	keyParts := make([]string, 0, len(groupIDs)+2)
	keyParts = append(keyParts, "peer", model)
	for _, id := range groupIDs {
		keyParts = append(keyParts, strconv.FormatInt(id, 10))
	}
	cacheKey := strings.Join(keyParts, ":")
	if metrics, ok := s.cachedPeerAutoGroupMetrics(cacheKey, time.Now()); ok {
		return excludeAutoGroupPeerMetrics(metrics, excludeUserID), nil
	}

	value, err, _ := s.autoGroupPeerMetricsSF.Do(cacheKey, func() (any, error) {
		now := time.Now()
		if metrics, ok := s.cachedPeerAutoGroupMetrics(cacheKey, now); ok {
			return metrics, nil
		}
		metrics, queryErr := peerRepo.ListRecentPeerGroupFirstTokenSamples(ctx, groupIDs, model, now.Add(-autoGroupMetricWindow), autoGroupSampleLimit)
		if queryErr != nil {
			return nil, queryErr
		}
		s.autoGroupPeerMetricsCache.Store(cacheKey, autoGroupPeerMetricsCacheEntry{metrics: metrics, expiresAt: now.Add(autoGroupMetricCacheTTL)})
		return metrics, nil
	})
	if err != nil {
		return nil, err
	}
	metrics, _ := value.(map[int64]map[int64][]int64)
	return excludeAutoGroupPeerMetrics(metrics, excludeUserID), nil
}

func (s *APIKeyService) cachedAutoGroupMetrics(cacheKey string, now time.Time) (map[int64][]int64, bool) {
	value, ok := s.autoGroupMetricsCache.Load(cacheKey)
	if !ok {
		return nil, false
	}
	cached, ok := value.(autoGroupMetricsCacheEntry)
	if !ok || !now.Before(cached.expiresAt) {
		s.autoGroupMetricsCache.Delete(cacheKey)
		return nil, false
	}
	return cached.metrics, true
}

func (s *APIKeyService) cachedPeerAutoGroupMetrics(cacheKey string, now time.Time) (map[int64]map[int64][]int64, bool) {
	value, ok := s.autoGroupPeerMetricsCache.Load(cacheKey)
	if !ok {
		return nil, false
	}
	cached, ok := value.(autoGroupPeerMetricsCacheEntry)
	if !ok || !now.Before(cached.expiresAt) {
		s.autoGroupPeerMetricsCache.Delete(cacheKey)
		return nil, false
	}
	return cached.metrics, true
}

func excludeAutoGroupPeerMetrics(metrics map[int64]map[int64][]int64, excludeUserID int64) map[int64][]int64 {
	filtered := make(map[int64][]int64, len(metrics))
	for groupID, byUser := range metrics {
		for userID, samples := range byUser {
			if userID != excludeUserID {
				filtered[groupID] = append(filtered[groupID], samples...)
			}
		}
	}
	return filtered
}

func (s *APIKeyService) peerVerifiedCheaperAutoGroup(ctx context.Context, userID int64, groups []Group, userRates map[int64]float64, currentGroupID int64, model string) (*Group, error) {
	cheaper := cheaperAvailableAutoGroups(groups, userRates, currentGroupID)
	if len(cheaper) == 0 {
		return nil, nil
	}
	metrics, err := s.recentPeerAutoGroupMetrics(ctx, userID, cheaper, model)
	if err != nil {
		return nil, err
	}
	return peerVerifiedCheaperAutoGroup(groups, userRates, currentGroupID, metrics), nil
}

func cheaperAvailableAutoGroups(groups []Group, userRates map[int64]float64, currentGroupID int64) []Group {
	current := findAvailableAutoGroup(groups, currentGroupID)
	if current == nil || current.ActiveAccountCount <= 0 {
		return nil
	}
	currentRate := effectiveGroupRate(*current, userRates)
	cheaper := make([]Group, 0, len(groups))
	for _, group := range groups {
		if group.ID != currentGroupID && group.ActiveAccountCount > 0 && effectiveGroupRate(group, userRates) < currentRate {
			cheaper = append(cheaper, group)
		}
	}
	return cheaper
}

func peerVerifiedCheaperAutoGroup(groups []Group, userRates map[int64]float64, currentGroupID int64, metrics map[int64][]int64) *Group {
	candidates := cheaperAvailableAutoGroups(groups, userRates, currentGroupID)
	var selected *Group
	for i := range candidates {
		candidate := &candidates[i]
		current := findAvailableAutoGroup(groups, currentGroupID)
		if current == nil {
			continue
		}
		currentRate := effectiveGroupRate(*current, userRates)
		candidateRate := effectiveGroupRate(*candidate, userRates)
		// A healthy settled group is sticky. Price review may wake a cheaper
		// peer only when the saving is material enough to justify changing the
		// upstream account and rebuilding its model context.
		if currentRate <= 0 || candidateRate > currentRate*(1-autoGroupMinPriceDiscount) {
			continue
		}
		if !autoGroupIsReliableGreen(metrics[candidate.ID]) {
			continue
		}
		if selected == nil || effectiveGroupRate(*candidate, userRates) < effectiveGroupRate(*selected, userRates) ||
			(effectiveGroupRate(*candidate, userRates) == effectiveGroupRate(*selected, userRates) &&
				(percentile95(metrics[candidate.ID]) < percentile95(metrics[selected.ID]) ||
					(percentile95(metrics[candidate.ID]) == percentile95(metrics[selected.ID]) && autoGroupGroupLess(*candidate, *selected)))) {
			selected = candidate
		}
	}
	return selected
}

// lowestAvailableRateGroup chooses the cheapest group that currently has at
// least one schedulable account. Active group status alone is not sufficient:
// a group can be active while every bound account is disabled, rate-limited,
// expired, or temporarily unschedulable.
func lowestAvailableRateGroup(groups []Group, userRates map[int64]float64) *Group {
	var best *Group
	var bestRate float64
	for i := range groups {
		candidate := &groups[i]
		if candidate.ActiveAccountCount <= 0 {
			continue
		}

		candidateRate := effectiveGroupRate(*candidate, userRates)
		if best == nil || candidateRate < bestRate ||
			(candidateRate == bestRate && (candidate.SortOrder < best.SortOrder ||
				(candidate.SortOrder == best.SortOrder && candidate.ID < best.ID))) {
			best = candidate
			bestRate = candidateRate
		}
	}
	return best
}

func effectiveGroupRate(group Group, userRates map[int64]float64) float64 {
	if userRate, ok := userRates[group.ID]; ok {
		return userRate
	}
	return group.RateMultiplier
}

func normalizeAutoGroupStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case autoGroupStrategyPrice:
		return autoGroupStrategyPrice
	case autoGroupStrategySpeed:
		return autoGroupStrategySpeed
	case autoGroupStrategyBalanced:
		return autoGroupStrategyBalanced
	default:
		return autoGroupStrategyPrice
	}
}

// selectStableAutoGroup is deliberately user-first. It measures the cheapest
// available group before spending any request on alternatives. A green
// cheapest group remains selected. Only after it has three personal samples
// and is red do we probe at most three other groups, then settle on the
// strategy-specific winner.
func selectStableAutoGroup(groups []Group, userRates map[int64]float64, metrics map[int64][]int64, strategy string, current autoGroupSelection) (*Group, autoGroupSelection, error) {
	available := make([]Group, 0, len(groups))
	for _, group := range groups {
		if group.ActiveAccountCount > 0 {
			available = append(available, group)
		}
	}
	if len(available) == 0 {
		return nil, current, ErrAutoGroupUnavailable
	}

	cheapest := lowestAvailableRateGroup(available, userRates)
	if cheapest == nil {
		return nil, current, ErrAutoGroupUnavailable
	}
	if normalizeAutoGroupStrategy(strategy) == autoGroupStrategyPrice {
		if current.needsEvaluation && current.eventGroupID == cheapest.ID {
			var fallback *Group
			for i := range available {
				candidate := &available[i]
				if candidate.ID == current.eventGroupID {
					continue
				}
				if fallback == nil || effectiveGroupRate(*candidate, userRates) < effectiveGroupRate(*fallback, userRates) ||
					(effectiveGroupRate(*candidate, userRates) == effectiveGroupRate(*fallback, userRates) && autoGroupGroupLess(*candidate, *fallback)) {
					fallback = candidate
				}
			}
			if fallback != nil {
				return fallback, autoGroupSelection{groupID: fallback.ID, settled: true}, nil
			}
		}
		return cheapest, autoGroupSelection{groupID: cheapest.ID, settled: true}, nil
	}

	cheapestSamples := metrics[cheapest.ID]
	if len(cheapestSamples) < autoGroupMinReliableSamples {
		return cheapest, autoGroupSelection{
			groupID:        cheapest.ID,
			pendingGroupID: cheapest.ID,
			pendingSince:   time.Now(),
		}, nil
	}
	if percentile95(cheapestSamples) <= autoGroupMaxP95FirstToken.Milliseconds() {
		return cheapest, autoGroupSelection{groupID: cheapest.ID, settled: true}, nil
	}

	if current.settled {
		if selected := findAvailableAutoGroup(available, current.groupID); selected != nil && autoGroupIsReliableGreen(metrics[selected.ID]) {
			best := selectMeasuredAutoGroup(available, userRates, metrics, strategy)
			if best == nil || best.ID == selected.ID || !autoGroupShouldSwitch(selected, best, available, userRates, metrics, strategy) {
				return selected, current, nil
			}
			return best, autoGroupSelection{
				groupID:        best.ID,
				probedGroupIDs: append([]int64(nil), current.probedGroupIDs...),
				settled:        true,
			}, nil
		}
	}

	// Metrics are cached for one minute, so keep using a newly probed group
	// until its result is visible. This prevents hopping through all
	// alternatives before any of them has enough data to be evaluated.
	completedProbeGroupID := int64(0)
	if current.pendingGroupID != 0 {
		if pending := findAvailableAutoGroup(available, current.pendingGroupID); pending != nil &&
			len(metrics[pending.ID]) < autoGroupMinReliableSamples &&
			!current.pendingSince.IsZero() && time.Since(current.pendingSince) < autoGroupProbeWaitTimeout {
			return pending, current, nil
		}
		completedProbeGroupID = current.pendingGroupID
		current.pendingGroupID = 0
		current.pendingSince = time.Time{}
	}
	if completedProbeGroupID != 0 && autoGroupIsReliableGreen(metrics[completedProbeGroupID]) {
		selected := selectMeasuredAutoGroup(available, userRates, metrics, strategy)
		if selected != nil {
			return selected, autoGroupSelection{
				groupID:        selected.ID,
				probedGroupIDs: current.probedGroupIDs,
				settled:        true,
			}, nil
		}
	}

	probed := autoGroupProbedSet(current.probedGroupIDs)
	alternatives := autoGroupAlternatives(available, cheapest.ID, userRates)
	for _, candidate := range alternatives {
		if len(probed) >= autoGroupMaxAlternativeProbes {
			break
		}
		if len(metrics[candidate.ID]) >= autoGroupMinReliableSamples || probed[candidate.ID] {
			continue
		}
		next := autoGroupSelection{
			groupID:        candidate.ID,
			probedGroupIDs: append(append([]int64(nil), current.probedGroupIDs...), candidate.ID),
			pendingGroupID: candidate.ID,
			pendingSince:   time.Now(),
		}
		return &candidate, next, nil
	}

	selected := selectMeasuredAutoGroup(available, userRates, metrics, strategy)
	if selected == nil {
		return cheapest, autoGroupSelection{
			groupID:        cheapest.ID,
			probedGroupIDs: current.probedGroupIDs,
			fallbackUntil:  time.Now().Add(autoGroupFallbackRetryAfter),
			settled:        true,
		}, nil
	}
	return selected, autoGroupSelection{groupID: selected.ID, probedGroupIDs: current.probedGroupIDs, settled: true}, nil
}

func (s *APIKeyService) autoGroupSelectionSnapshot(key, switchKey string, userID int64, candidateGroupIDs []int64) (autoGroupSelection, autoGroupGenerationSnapshot) {
	if s == nil {
		return autoGroupSelection{}, autoGroupGenerationSnapshot{}
	}
	s.maybeSweepAutoGroupCaches(time.Now())
	s.autoGroupSelectionMu.Lock()
	defer s.autoGroupSelectionMu.Unlock()

	selection := autoGroupSelection{}
	if value, ok := s.autoGroupSelections.Load(key); ok {
		selection, _ = value.(autoGroupSelection)
		if !selection.expiresAt.IsZero() && !time.Now().Before(selection.expiresAt) {
			s.autoGroupSelections.Delete(key)
			selection = autoGroupSelection{}
		}
	}
	normalizedGroupIDs := normalizeAutoGroupIDs(candidateGroupIDs)
	generation := autoGroupGenerationSnapshot{
		selection:          selection.revision,
		user:               s.autoGroupUserGenerations[userID],
		allCandidateGroups: len(normalizedGroupIDs) == 0,
	}
	if value, ok := s.autoGroupSwitchStates.Load(switchKey); ok {
		if state, ok := value.(autoGroupSwitchState); ok {
			generation.switchRevision = state.revision
		}
	}
	if generation.allCandidateGroups {
		generation.allGroupsEpoch = s.autoGroupAllGroupsEpoch
	} else {
		generation.groups = make(map[int64]uint64, len(normalizedGroupIDs))
		for _, groupID := range normalizedGroupIDs {
			generation.groups[groupID] = s.autoGroupGroupGenerations[groupID]
		}
	}
	return selection, generation
}

func (s *APIKeyService) autoGroupSelection(key string) autoGroupSelection {
	selection, _ := s.autoGroupSelectionSnapshot(key, "", 0, nil)
	return selection
}

func (s *APIKeyService) storeAutoGroupSelection(key string, selection autoGroupSelection) {
	if s == nil {
		return
	}
	s.autoGroupSelectionMu.Lock()
	currentRevision := uint64(0)
	if value, ok := s.autoGroupSelections.Load(key); ok {
		if current, ok := value.(autoGroupSelection); ok {
			currentRevision = current.revision
		}
	}
	selection.revision = currentRevision + 1
	selection.expiresAt = time.Now().Add(autoGroupSelectionTTL)
	s.autoGroupSelections.Store(key, selection)
	s.autoGroupSelectionMu.Unlock()
}

func (s *APIKeyService) storeAutoGroupSelectionIfCurrent(key, switchKey string, userID int64, expected autoGroupGenerationSnapshot, selection autoGroupSelection) bool {
	if s == nil {
		return false
	}
	s.autoGroupSelectionMu.Lock()
	defer s.autoGroupSelectionMu.Unlock()

	currentRevision := uint64(0)
	if value, ok := s.autoGroupSelections.Load(key); ok {
		if current, ok := value.(autoGroupSelection); ok {
			currentRevision = current.revision
		}
	}
	if currentRevision != expected.selection || s.autoGroupUserGenerations[userID] != expected.user {
		return false
	}
	if autoGroupSwitchStateRevision(&s.autoGroupSwitchStates, switchKey) != expected.switchRevision {
		return false
	}
	if expected.allCandidateGroups {
		if s.autoGroupAllGroupsEpoch != expected.allGroupsEpoch {
			return false
		}
	} else {
		for groupID, generation := range expected.groups {
			if s.autoGroupGroupGenerations[groupID] != generation {
				return false
			}
		}
	}

	selection.revision = currentRevision + 1
	selection.expiresAt = time.Now().Add(autoGroupSelectionTTL)
	s.autoGroupSelections.Store(key, selection)
	return true
}

func (s *APIKeyService) commitAutoGroupSelectionIfCurrent(key, switchKey, model string, userID int64, expected autoGroupGenerationSnapshot, current, next autoGroupSelection, groups []Group, now time.Time) (autoGroupSelection, bool) {
	if s == nil {
		return autoGroupSelection{}, false
	}
	s.autoGroupSelectionMu.Lock()
	defer s.autoGroupSelectionMu.Unlock()

	currentRevision := uint64(0)
	if value, ok := s.autoGroupSelections.Load(key); ok {
		if stored, ok := value.(autoGroupSelection); ok {
			currentRevision = stored.revision
		}
	}
	if currentRevision != expected.selection || s.autoGroupUserGenerations[userID] != expected.user ||
		autoGroupSwitchStateRevision(&s.autoGroupSwitchStates, switchKey) != expected.switchRevision {
		return autoGroupSelection{}, false
	}
	if expected.allCandidateGroups {
		if s.autoGroupAllGroupsEpoch != expected.allGroupsEpoch {
			return autoGroupSelection{}, false
		}
	} else {
		for groupID, generation := range expected.groups {
			if s.autoGroupGroupGenerations[groupID] != generation {
				return autoGroupSelection{}, false
			}
		}
	}

	switchState := autoGroupSwitchState{}
	if value, ok := s.autoGroupSwitchStates.Load(switchKey); ok {
		switchState, _ = value.(autoGroupSwitchState)
	}
	switchState.switchedAt = pruneAutoGroupSwitchHistory(switchState.switchedAt, now)
	previousGroupID := current.groupID
	if previousGroupID == 0 {
		previousGroupID = switchState.groupIDs[model]
	}
	actual := next
	if previousGroupID != 0 && next.groupID != previousGroupID {
		previousGroup := findAvailableAutoGroup(groups, previousGroupID)

		switch {
		case previousGroup == nil:
			// The current group has no active accounts — it cannot serve the
			// request at all. Switch immediately without consuming any budget.
			previousGroupID = next.groupID

		case current.failureTriggered:
			// A confirmed repeated 503/5xx means the current group is not usable.
			// Failure-triggered switches must be immediate; only healthy-group
			// probing and price review use the low-frequency switch budget below.
			previousGroupID = next.groupID

		default:
			// Normal voluntary switch (probing, price review, etc.).
			blockedUntil, limited := autoGroupSwitchBlockedUntil(switchState.switchedAt, now)
			if limited {
				actual = next
				groupSnapshot := *previousGroup
				actual.groupID = previousGroupID
				actual.selectedGroup = &groupSnapshot
				actual.probedGroupIDs = nil
				actual.pendingGroupID = 0
				actual.pendingSince = time.Time{}
				actual.needsEvaluation = false
				actual.eventGroupID = 0
				actual.observedProbeSamples = 0
				actual.consecutiveSlowRequests = 0
				actual.transientFailureStreak = 0
				actual.lastTransientFailureAt = time.Time{}
				actual.failureTriggered = false
				actual.fallbackUntil = time.Time{}
				actual.priceReviewAt = blockedUntil
				actual.settled = true
			} else {
				switchState.switchedAt = append(switchState.switchedAt, now)
				previousGroupID = next.groupID
			}
		}
	}
	if previousGroupID == 0 {
		previousGroupID = actual.groupID
	}
	if switchState.groupIDs == nil {
		switchState.groupIDs = make(map[string]int64)
	}
	switchState.groupIDs[model] = previousGroupID
	switchState.revision++
	switchState.expiresAt = now.Add(autoGroupSelectionTTL)
	s.autoGroupSwitchStates.Store(switchKey, switchState)

	actual.revision = currentRevision + 1
	actual.expiresAt = now.Add(autoGroupSelectionTTL)
	actual.lastSelectedAt = now
	s.autoGroupSelections.Store(key, actual)
	return actual, true
}

func autoGroupSwitchStateRevision(states *sync.Map, key string) uint64 {
	if states == nil || key == "" {
		return 0
	}
	if value, ok := states.Load(key); ok {
		if state, ok := value.(autoGroupSwitchState); ok {
			return state.revision
		}
	}
	return 0
}

func pruneAutoGroupSwitchHistory(history []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-autoGroupSwitchWindow)
	pruned := make([]time.Time, 0, len(history))
	for _, switchedAt := range history {
		if switchedAt.After(cutoff) {
			pruned = append(pruned, switchedAt)
		}
	}
	return pruned
}

func autoGroupSwitchBlockedUntil(history []time.Time, now time.Time) (time.Time, bool) {
	var blockedUntil time.Time
	if len(history) > 0 {
		intervalUntil := history[len(history)-1].Add(autoGroupSwitchInterval)
		if intervalUntil.After(now) {
			blockedUntil = intervalUntil
		}
	}
	if len(history) >= autoGroupMaxSwitchesPerWindow {
		windowUntil := history[0].Add(autoGroupSwitchWindow)
		if windowUntil.After(blockedUntil) {
			blockedUntil = windowUntil
		}
	}
	return blockedUntil, blockedUntil.After(now)
}

func (s *APIKeyService) maybeSweepAutoGroupCaches(now time.Time) {
	if s == nil || s.autoGroupCacheOperations.Add(1)%autoGroupCacheSweepEvery != 0 {
		return
	}
	inspected := 0
	s.autoGroupMetricsCache.Range(func(key, value any) bool {
		inspected++
		entry, ok := value.(autoGroupMetricsCacheEntry)
		if !ok || !now.Before(entry.expiresAt) {
			s.autoGroupMetricsCache.Delete(key)
		}
		return inspected < autoGroupCacheSweepLimit
	})
	inspected = 0
	s.autoGroupPeerMetricsCache.Range(func(key, value any) bool {
		inspected++
		entry, ok := value.(autoGroupPeerMetricsCacheEntry)
		if !ok || !now.Before(entry.expiresAt) {
			s.autoGroupPeerMetricsCache.Delete(key)
		}
		return inspected < autoGroupCacheSweepLimit
	})
	inspected = 0
	s.autoGroupSwitchStates.Range(func(key, value any) bool {
		inspected++
		entry, ok := value.(autoGroupSwitchState)
		if !ok || !now.Before(entry.expiresAt) {
			s.autoGroupSwitchStates.Delete(key)
		}
		return inspected < autoGroupCacheSweepLimit
	})
	s.autoGroupSelectionMu.Lock()
	inspected = 0
	s.autoGroupSelections.Range(func(key, value any) bool {
		inspected++
		entry, ok := value.(autoGroupSelection)
		if !ok || (!entry.expiresAt.IsZero() && !now.Before(entry.expiresAt)) {
			s.autoGroupSelections.Delete(key)
		}
		return inspected < autoGroupCacheSweepLimit
	})
	s.autoGroupSelectionMu.Unlock()
}

func autoGroupSelectionKey(apiKey *APIKey, model string) string {
	if apiKey == nil {
		return ""
	}
	return strings.Join([]string{
		strconv.FormatInt(apiKey.ID, 10),
		normalizeAutoGroupStrategy(apiKey.AutoGroupStrategy),
		strings.TrimSpace(model),
	}, ":")
}

func autoGroupSwitchStateKey(apiKey *APIKey) string {
	if apiKey == nil {
		return ""
	}
	return strconv.FormatInt(apiKey.ID, 10)
}

func autoGroupConfigFingerprint(apiKey *APIKey) string {
	if apiKey == nil {
		return ""
	}
	groupIDs := append([]int64(nil), apiKey.AutoGroupIDs...)
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })
	parts := make([]string, 0, len(groupIDs)+1)
	parts = append(parts, strconv.FormatBool(apiKey.AutoGroup))
	for _, groupID := range groupIDs {
		parts = append(parts, strconv.FormatInt(groupID, 10))
	}
	return strings.Join(parts, ":")
}

// ObserveAutoGroupRequestResult records only routing events. Normal requests
// reuse the settled selection without re-running the scoring algorithm.
func (s *APIKeyService) ObserveAutoGroupRequestResult(apiKey *APIKey, model string, status int, firstTokenMs *int64) {
	if s == nil || apiKey == nil || !apiKey.AutoGroup {
		return
	}
	key := autoGroupSelectionKey(apiKey, model)
	s.autoGroupSelectionMu.Lock()
	value, ok := s.autoGroupSelections.Load(key)
	if !ok {
		s.autoGroupSelectionMu.Unlock()
		return
	}
	selection, ok := value.(autoGroupSelection)
	if !ok || selection.configFingerprint != autoGroupConfigFingerprint(apiKey) {
		s.autoGroupSelectionMu.Unlock()
		return
	}

	changed := false
	now := time.Now()
	// 529 is an upstream overload signal, not evidence that this account or
	// group is broken. Do not turn it into an automatic group switch event.
	// The upstream retry/cooldown layers handle the request-level backoff.
	if status == 529 {
		s.autoGroupSelectionMu.Unlock()
		return
	}
	transientFailure := status == 408 || status == 429 || status >= 500
	if transientFailure {
		if selection.needsEvaluation && selection.failureTriggered {
			// A resolver is already handling this confirmed failure. Repeated
			// terminal observations must not keep bumping the selection revision
			// and starve that resolver under a burst of identical errors.
			s.autoGroupSelectionMu.Unlock()
			return
		}
		if selection.lastTransientFailureAt.IsZero() || now.Sub(selection.lastTransientFailureAt) > autoGroupTransientFailureWindow {
			selection.transientFailureStreak = 0
		}
		selection.transientFailureStreak++
		selection.lastTransientFailureAt = now
		changed = true
		if selection.transientFailureStreak >= autoGroupTransientFailureThreshold {
			if !selection.needsEvaluation || selection.eventGroupID != selection.groupID ||
				selection.consecutiveSlowRequests != 0 || selection.observedProbeSamples != 0 || !selection.failureTriggered {
				selection.needsEvaluation = true
				selection.eventGroupID = selection.groupID
				selection.consecutiveSlowRequests = 0
				selection.observedProbeSamples = 0
				selection.failureTriggered = true
			}
		}
	} else if firstTokenMs != nil && *firstTokenMs > 0 {
		if status < 400 && selection.transientFailureStreak != 0 {
			selection.transientFailureStreak = 0
			selection.lastTransientFailureAt = time.Time{}
			changed = true
		}
		if selection.pendingGroupID != 0 {
			if !selection.needsEvaluation {
				selection.observedProbeSamples++
				if selection.observedProbeSamples >= autoGroupMinReliableSamples {
					selection.needsEvaluation = true
				}
				changed = true
			}
		} else if !selection.needsEvaluation && (selection.fallbackUntil.IsZero() || now.After(selection.fallbackUntil)) {
			if *firstTokenMs > autoGroupMaxP95FirstToken.Milliseconds() {
				selection.consecutiveSlowRequests++
				if selection.consecutiveSlowRequests >= autoGroupSlowEventThreshold {
					selection.needsEvaluation = true
					selection.eventGroupID = selection.groupID
					selection.failureTriggered = false
				}
				changed = true
			} else if selection.consecutiveSlowRequests != 0 {
				selection.consecutiveSlowRequests = 0
				changed = true
			}
		}
	} else if status < 400 && selection.transientFailureStreak != 0 {
		selection.transientFailureStreak = 0
		selection.lastTransientFailureAt = time.Time{}
		changed = true
	}

	if !changed {
		s.autoGroupSelectionMu.Unlock()
		return
	}
	selection.revision++
	selection.expiresAt = time.Now().Add(autoGroupSelectionTTL)
	s.autoGroupSelections.Store(key, selection)
	s.autoGroupSelectionMu.Unlock()
	if selection.needsEvaluation {
		s.invalidateAutoGroupMetrics(apiKey.UserID, model)
	}
}

func (s *APIKeyService) invalidateAutoGroupMetrics(userID int64, model string) {
	if s == nil {
		return
	}
	prefix := strings.Join([]string{strconv.FormatInt(userID, 10), strings.TrimSpace(model), ""}, ":")
	s.autoGroupMetricsCache.Range(func(key, _ any) bool {
		cacheKey, ok := key.(string)
		if ok && strings.HasPrefix(cacheKey, prefix) {
			s.autoGroupMetricsCache.Delete(key)
		}
		return true
	})
}

func autoGroupIsGreen(samples []int64) bool {
	return len(samples) > 0 && percentile95(samples) <= autoGroupMaxP95FirstToken.Milliseconds()
}

func autoGroupIsReliableGreen(samples []int64) bool {
	return len(samples) >= autoGroupMinReliableSamples && autoGroupIsGreen(samples)
}

func autoGroupProbedSet(groupIDs []int64) map[int64]bool {
	probed := make(map[int64]bool, len(groupIDs))
	for _, groupID := range groupIDs {
		probed[groupID] = true
	}
	return probed
}

func autoGroupAlternatives(groups []Group, cheapestID int64, userRates map[int64]float64) []Group {
	alternatives := make([]Group, 0, len(groups)-1)
	for _, group := range groups {
		if group.ID != cheapestID {
			alternatives = append(alternatives, group)
		}
	}
	sort.SliceStable(alternatives, func(i, j int) bool {
		leftRate := effectiveGroupRate(alternatives[i], userRates)
		rightRate := effectiveGroupRate(alternatives[j], userRates)
		if leftRate != rightRate {
			return leftRate < rightRate
		}
		return autoGroupGroupLess(alternatives[i], alternatives[j])
	})
	return alternatives
}

func findAvailableAutoGroup(groups []Group, groupID int64) *Group {
	for i := range groups {
		if groups[i].ID == groupID {
			return &groups[i]
		}
	}
	return nil
}

func selectMeasuredAutoGroup(groups []Group, userRates map[int64]float64, metrics map[int64][]int64, strategy string) *Group {
	measured := rankMeasuredAutoGroups(groups, userRates, metrics, strategy)
	if len(measured) == 0 {
		return nil
	}
	return &measured[0].group
}

func autoGroupShouldSwitch(current, candidate *Group, groups []Group, userRates map[int64]float64, metrics map[int64][]int64, strategy string) bool {
	if current == nil || candidate == nil || current.ID == candidate.ID {
		return false
	}
	ranked := rankMeasuredAutoGroups(groups, userRates, metrics, strategy)
	var currentScore, candidateScore float64
	var currentFound, candidateFound bool
	for _, scored := range ranked {
		switch scored.group.ID {
		case current.ID:
			currentScore, currentFound = scored.score, true
		case candidate.ID:
			candidateScore, candidateFound = scored.score, true
		}
	}
	return currentFound && candidateFound && candidateScore >= currentScore+autoGroupSwitchScoreMargin
}

func rankMeasuredAutoGroups(groups []Group, userRates map[int64]float64, metrics map[int64][]int64, strategy string) []autoGroupScoredCandidate {
	measured := make([]autoGroupScoredCandidate, 0, len(groups))
	for _, group := range groups {
		samples := metrics[group.ID]
		if !autoGroupIsReliableGreen(samples) {
			continue
		}
		measured = append(measured, autoGroupScoredCandidate{group: group, rate: effectiveGroupRate(group, userRates), p95FirstToken: percentile95(samples)})
	}
	if len(measured) == 0 {
		return nil
	}

	lowestRate := measured[0].rate
	for _, candidate := range measured[1:] {
		if candidate.rate < lowestRate {
			lowestRate = candidate.rate
		}
	}
	priceWeight, speedWeight := autoGroupWeights(strategy)
	for i := range measured {
		priceScore := 0.0
		if measured[i].rate > 0 {
			priceScore = clampAutoGroupScore(lowestRate / measured[i].rate)
		}
		speedScore := clampAutoGroupScore(float64(autoGroupMaxP95FirstToken.Milliseconds()-measured[i].p95FirstToken) / float64(autoGroupMaxP95FirstToken.Milliseconds()))
		measured[i].score = priceWeight*priceScore + speedWeight*speedScore
	}
	sort.SliceStable(measured, func(i, j int) bool {
		if measured[i].score != measured[j].score {
			return measured[i].score > measured[j].score
		}
		return autoGroupGroupLess(measured[i].group, measured[j].group)
	})
	return measured
}

type autoGroupScoredCandidate struct {
	group         Group
	rate          float64
	p95FirstToken int64
	score         float64
}

func autoGroupWeights(strategy string) (price, speed float64) {
	switch normalizeAutoGroupStrategy(strategy) {
	case autoGroupStrategyPrice:
		return 1, 0
	case autoGroupStrategySpeed:
		return 0, 1
	default:
		// Balanced mode favors the lowest-cost stable group. Speed still
		// breaks ties and avoids choosing a materially slower green group.
		return 0.70, 0.30
	}
}

func percentile95(values []int64) int64 {
	ordered := append([]int64(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := (95*len(ordered) + 99) / 100
	if index <= 0 {
		return 0
	}
	return ordered[index-1]
}

func clampAutoGroupScore(score float64) float64 {
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func autoGroupGroupLess(left, right Group) bool {
	return left.SortOrder < right.SortOrder || (left.SortOrder == right.SortOrder && left.ID < right.ID)
}

// Update 更新API Key
func (s *APIKeyService) Update(ctx context.Context, id int64, userID int64, req UpdateAPIKeyRequest) (*APIKey, error) {
	apiKey, err := s.apiKeyRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get api key: %w", err)
	}

	// 验证所有权
	if apiKey.UserID != userID {
		return nil, ErrInsufficientPerms
	}

	// 验证 IP 白名单格式
	if req.IPWhitelist != nil && len(*req.IPWhitelist) > 0 {
		if invalid := ip.ValidateIPPatterns(*req.IPWhitelist); len(invalid) > 0 {
			return nil, fmt.Errorf("%w: %v", ErrInvalidIPPattern, invalid)
		}
	}

	// 验证 IP 黑名单格式
	if req.IPBlacklist != nil && len(*req.IPBlacklist) > 0 {
		if invalid := ip.ValidateIPPatterns(*req.IPBlacklist); len(invalid) > 0 {
			return nil, fmt.Errorf("%w: %v", ErrInvalidIPPattern, invalid)
		}
	}

	// fields 只登记本次请求真正要改的列。quota_used 与 usage_5h/1d/7d 由计费热路径
	// 原子递增，除非用户显式点了"重置"，否则这里不用快照把它们写回去。
	var fields APIKeyUpdateFields
	// 下面若干分支会顺带把 Status 改回 active（配额扩容、清除过期等），
	// 所以用原始值比对来决定是否写 status，而不是只看 req.Status。
	originalStatus := apiKey.Status

	// 更新字段
	if req.Name != nil {
		apiKey.Name = html.EscapeString(*req.Name)
		fields.Name = true
	}

	if req.AutoGroup != nil {
		apiKey.AutoGroup = *req.AutoGroup
		fields.AutoGroup = true
		if apiKey.AutoGroup {
			apiKey.GroupID = nil
			fields.GroupID = true
			if req.AutoGroupIDs == nil {
				return nil, infraerrors.BadRequest("AUTO_GROUP_CANDIDATES_REQUIRED", "select at least one group for automatic routing")
			}
		}
	}
	if req.AutoGroupIDs != nil {
		if !apiKey.AutoGroup {
			return nil, infraerrors.BadRequest("AUTO_GROUP_CANDIDATES_INVALID", "automatic routing must be enabled before selecting candidate groups")
		}
		if err := s.validateAutoGroupIDs(ctx, userID, *req.AutoGroupIDs); err != nil {
			return nil, err
		}
		apiKey.AutoGroupIDs = normalizeAutoGroupIDs(*req.AutoGroupIDs)
		fields.AutoGroupIDs = true
	}
	if req.AutoGroupStrategy != nil {
		apiKey.AutoGroupStrategy = normalizeAutoGroupStrategy(*req.AutoGroupStrategy)
		fields.AutoGroupStrategy = true
	}

	if req.GroupID != nil {
		// 验证分组权限
		user, err := s.userRepo.GetByID(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("get user: %w", err)
		}

		group, err := s.groupRepo.GetByID(ctx, *req.GroupID)
		if err != nil {
			return nil, fmt.Errorf("get group: %w", err)
		}

		if !s.canUserBindGroup(ctx, user, group) {
			return nil, ErrGroupNotAllowed
		}

		apiKey.GroupID = req.GroupID
		apiKey.AutoGroup = false
		fields.AutoGroup = true
		fields.GroupID = true
	}

	if req.Status != nil {
		apiKey.Status = *req.Status
		fields.Status = true
		// 如果状态改变，清除Redis缓存
		if s.cache != nil {
			_ = s.cache.DeleteCreateAttemptCount(ctx, apiKey.UserID)
		}
	}

	// Update quota fields
	if req.Quota != nil {
		apiKey.Quota = *req.Quota
		fields.Quota = true
		// If quota now has room, or is changed to unlimited, reactivate exhausted keys.
		if apiKey.Status == StatusAPIKeyQuotaExhausted && (*req.Quota <= 0 || *req.Quota > apiKey.QuotaUsed) {
			apiKey.Status = StatusActive
		}
	}
	if req.ResetQuota != nil && *req.ResetQuota {
		apiKey.QuotaUsed = 0
		fields.QuotaUsed = true
		// If resetting quota and status was quota_exhausted, reactivate
		if apiKey.Status == StatusAPIKeyQuotaExhausted {
			apiKey.Status = StatusActive
		}
	}
	if req.ClearExpiration {
		apiKey.ExpiresAt = nil
		fields.ExpiresAt = true
		// If clearing expiry and status was expired, reactivate
		if apiKey.Status == StatusAPIKeyExpired {
			apiKey.Status = StatusActive
		}
	} else if req.ExpiresAt != nil {
		apiKey.ExpiresAt = req.ExpiresAt
		fields.ExpiresAt = true
		// If extending expiry and status was expired, reactivate
		if apiKey.Status == StatusAPIKeyExpired && time.Now().Before(*req.ExpiresAt) {
			apiKey.Status = StatusActive
		}
	}

	// 更新 IP 限制（nil 不修改，空数组清空设置）
	if req.IPWhitelist != nil {
		apiKey.IPWhitelist = *req.IPWhitelist
		fields.IPRules = true
	}
	if req.IPBlacklist != nil {
		apiKey.IPBlacklist = *req.IPBlacklist
		fields.IPRules = true
	}

	// Update rate limit configuration
	if req.RateLimit5h != nil {
		apiKey.RateLimit5h = *req.RateLimit5h
		fields.RateLimits = true
	}
	if req.RateLimit1d != nil {
		apiKey.RateLimit1d = *req.RateLimit1d
		fields.RateLimits = true
	}
	if req.RateLimit7d != nil {
		apiKey.RateLimit7d = *req.RateLimit7d
		fields.RateLimits = true
	}
	resetRateLimit := req.ResetRateLimitUsage != nil && *req.ResetRateLimitUsage
	if resetRateLimit {
		apiKey.Usage5h = 0
		apiKey.Usage1d = 0
		apiKey.Usage7d = 0
		apiKey.Window5hStart = nil
		apiKey.Window1dStart = nil
		apiKey.Window7dStart = nil
		fields.RateLimitUsage = true
	}

	// 上面的自动复活分支可能改了 status，这里统一登记。
	if apiKey.Status != originalStatus {
		fields.Status = true
	}

	if err := s.apiKeyRepo.Update(ctx, apiKey, fields); err != nil {
		return nil, fmt.Errorf("update api key: %w", err)
	}

	s.InvalidateAuthCacheByKey(ctx, apiKey.Key)
	if fields.AutoGroup || fields.AutoGroupStrategy || fields.AutoGroupIDs || fields.GroupID {
		s.InvalidateAutoGroupSelectionsByUserID(ctx, apiKey.UserID)
	}
	s.compileAPIKeyIPRules(apiKey)

	// Invalidate Redis rate limit cache so reset takes effect immediately
	if resetRateLimit && s.rateLimitCacheInvalid != nil {
		_ = s.rateLimitCacheInvalid.InvalidateAPIKeyRateLimit(ctx, apiKey.ID)
	}

	return apiKey, nil
}

// Delete 删除API Key
func (s *APIKeyService) Delete(ctx context.Context, id int64, userID int64) error {
	key, ownerID, err := s.apiKeyRepo.GetKeyAndOwnerID(ctx, id)
	if err != nil {
		return fmt.Errorf("get api key: %w", err)
	}

	// 验证当前用户是否为该 API Key 的所有者
	if ownerID != userID {
		return ErrInsufficientPerms
	}

	// 事务内:写审计 + 软删除(tombstone)。
	if err := s.apiKeyRepo.DeleteWithAudit(ctx, id); err != nil {
		return fmt.Errorf("delete api key: %w", err)
	}

	// 删除成功后再清理缓存,避免"缓存已清但删除失败"的竞态。
	if s.cache != nil {
		_ = s.cache.DeleteCreateAttemptCount(ctx, userID)
	}
	s.InvalidateAuthCacheByKey(ctx, key)
	s.InvalidateAutoGroupSelectionsByUserID(ctx, userID)
	s.lastUsedTouchL1.Delete(id)

	return nil
}

// ValidateKey 验证API Key是否有效（用于认证中间件）
func (s *APIKeyService) ValidateKey(ctx context.Context, key string) (*APIKey, *User, error) {
	// 获取API Key
	apiKey, err := s.GetByKey(ctx, key)
	if err != nil {
		return nil, nil, err
	}

	// 检查API Key状态
	if !apiKey.IsActive() {
		return nil, nil, infraerrors.Unauthorized("API_KEY_INACTIVE", "api key is not active")
	}

	// 获取用户信息
	user, err := s.userRepo.GetByID(ctx, apiKey.UserID)
	if err != nil {
		return nil, nil, fmt.Errorf("get user: %w", err)
	}

	// 检查用户状态
	if !user.IsActive() {
		return nil, nil, ErrUserNotActive
	}

	return apiKey, user, nil
}

// TouchLastUsed 通过防抖更新 api_keys.last_used_at，减少高频写放大。
// 该操作为尽力而为，不应阻塞主请求链路。
func (s *APIKeyService) TouchLastUsed(ctx context.Context, keyID int64) error {
	if keyID <= 0 {
		return nil
	}

	now := time.Now()
	if v, ok := s.lastUsedTouchL1.Load(keyID); ok {
		if nextAllowedAt, ok := v.(time.Time); ok && now.Before(nextAllowedAt) {
			return nil
		}
	}

	_, err, _ := s.lastUsedTouchSF.Do(strconv.FormatInt(keyID, 10), func() (any, error) {
		latest := time.Now()
		if v, ok := s.lastUsedTouchL1.Load(keyID); ok {
			if nextAllowedAt, ok := v.(time.Time); ok && latest.Before(nextAllowedAt) {
				return nil, nil
			}
		}

		if err := s.apiKeyRepo.UpdateLastUsed(ctx, keyID, latest); err != nil {
			s.lastUsedTouchL1.Store(keyID, latest.Add(apiKeyLastUsedFailBackoff))
			return nil, fmt.Errorf("touch api key last used: %w", err)
		}
		s.lastUsedTouchL1.Store(keyID, latest.Add(apiKeyLastUsedMinTouch))
		return nil, nil
	})
	return err
}

// IncrementUsage 增加API Key使用次数（可选：用于统计）
func (s *APIKeyService) IncrementUsage(ctx context.Context, keyID int64) error {
	// 使用Redis计数器
	if s.cache != nil {
		cacheKey := fmt.Sprintf("apikey:usage:%d:%s", keyID, timezone.Now().Format("2006-01-02"))
		if err := s.cache.IncrementDailyUsage(ctx, cacheKey); err != nil {
			return fmt.Errorf("increment usage: %w", err)
		}
		// 设置24小时过期
		_ = s.cache.SetDailyUsageExpiry(ctx, cacheKey, 24*time.Hour)
	}
	return nil
}

// GetAvailableGroups 获取用户有权限绑定的分组列表
// 返回用户可以选择的分组：
// - 标准类型分组：公开的（非专属）或用户被明确允许的
// - 订阅类型分组：用户有有效订阅的
func (s *APIKeyService) GetAvailableGroups(ctx context.Context, userID int64) ([]Group, error) {
	if s == nil || s.userRepo == nil || s.groupRepo == nil || s.userSubRepo == nil {
		return nil, fmt.Errorf("auto group resolution repositories are not configured")
	}
	// 获取用户信息
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	// 获取所有活跃分组
	allGroups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active groups: %w", err)
	}

	// 获取用户的所有有效订阅
	activeSubscriptions, err := s.userSubRepo.ListActiveByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list active subscriptions: %w", err)
	}

	// 构建订阅分组 ID 集合
	subscribedGroupIDs := make(map[int64]bool)
	for _, sub := range activeSubscriptions {
		subscribedGroupIDs[sub.GroupID] = true
	}

	// 过滤出用户有权限的分组
	availableGroups := make([]Group, 0)
	for _, group := range allGroups {
		if s.canUserBindGroupInternal(user, &group, subscribedGroupIDs) {
			availableGroups = append(availableGroups, group)
		}
	}

	return availableGroups, nil
}

// GetActiveSubscriptionForGroup exposes the narrow subscription lookup needed
// when an automatic key fails over between groups during one request. Keeping
// this lookup beside GetAvailableGroups ensures the request snapshot does not
// carry a subscription belonging to the previous group.
func (s *APIKeyService) GetActiveSubscriptionForGroup(ctx context.Context, userID, groupID int64) (*UserSubscription, error) {
	if s == nil || s.userSubRepo == nil || userID <= 0 || groupID <= 0 {
		return nil, nil
	}
	return s.userSubRepo.GetActiveByUserIDAndGroupID(ctx, userID, groupID)
}

// canUserBindGroupInternal 内部方法，检查用户是否可以绑定分组（使用预加载的订阅数据）
func (s *APIKeyService) canUserBindGroupInternal(user *User, group *Group, subscribedGroupIDs map[int64]bool) bool {
	// 订阅类型分组：需要有效订阅
	if group.IsSubscriptionType() {
		return subscribedGroupIDs[group.ID]
	}
	// 标准类型分组：使用原有逻辑
	return user.CanBindGroup(group.ID, group.IsExclusive)
}

func normalizeAutoGroupIDs(groupIDs []int64) []int64 {
	seen := make(map[int64]struct{}, len(groupIDs))
	normalized := make([]int64, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		if groupID <= 0 {
			continue
		}
		if _, ok := seen[groupID]; ok {
			continue
		}
		seen[groupID] = struct{}{}
		normalized = append(normalized, groupID)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	return normalized
}

func filterAutoGroupCandidates(groups []Group, allowedIDs []int64) []Group {
	// nil is the legacy representation for "all available groups". An
	// explicitly persisted empty list means every configured candidate was
	// removed (for example when a group was deleted), so it must not silently
	// broaden routing to unrelated groups.
	if allowedIDs == nil {
		return groups
	}
	if len(allowedIDs) == 0 {
		return []Group{}
	}
	allowed := make(map[int64]struct{}, len(allowedIDs))
	for _, groupID := range allowedIDs {
		allowed[groupID] = struct{}{}
	}
	filtered := make([]Group, 0, len(allowed))
	for _, group := range groups {
		if _, ok := allowed[group.ID]; ok {
			filtered = append(filtered, group)
		}
	}
	return filtered
}

// filterAutoGroupCandidatesForModel keeps model-aware automatic routing from
// selecting a group that cannot serve the model requested by the caller.
// Image requests additionally require the group's image capability flag and
// the platform associated with the image model family.
func (s *APIKeyService) filterAutoGroupCandidatesForModel(ctx context.Context, groups []Group, model string) ([]Group, error) {
	model = strings.TrimSpace(model)
	if model == "" || len(groups) == 0 {
		return groups, nil
	}

	filtered := make([]Group, 0, len(groups))
	for i := range groups {
		group := groups[i]
		if !groupMatchesRequestedModel(&group, model) {
			continue
		}

		// Older/unit-test service instances may not provide the optional account
		// repository. Preserve their existing behavior while production wiring
		// uses account-level model mappings as an additional capability check.
		if s != nil && s.autoGroupModelRepo != nil {
			groupID := group.ID
			accounts, err := s.autoGroupModelRepo.ListModelAvailabilityCandidates(ctx, &groupID, []string{group.Platform}, true)
			if err != nil {
				return nil, err
			}
			modelSupported := false
			for i := range accounts {
				if accounts[i].IsModelSupported(model) {
					modelSupported = true
					break
				}
			}
			if !modelSupported {
				continue
			}
		}

		// Runtime check: skip groups whose entire account pool has been cooled
		// down for this model by HandleUpstreamModelNotFound. Unlike the static
		// check above, this check considers runtime model-specific rate limits so
		// auto-group routing doesn't keep sending requests to a group that has
		// already confirmed it cannot serve the model.
		if s != nil && s.autoGroupRuntimeChecker != nil {
			hasSchedulable, err := s.autoGroupRuntimeChecker.HasRuntimeSchedulableAccountForModel(ctx, group.ID, group.Platform, model)
			if err != nil {
				// Conservative fallback: include the group on any check error
				// so a transient DB hiccup does not silently drop all candidates.
			} else if !hasSchedulable {
				continue
			}
		}

		filtered = append(filtered, group)
	}
	return filtered, nil
}

func groupMatchesRequestedModel(group *Group, model string) bool {
	if group == nil {
		return false
	}
	if IsImageGenerationIntent("", model, nil) {
		if !GroupAllowsImageGeneration(group) {
			return false
		}
		switch {
		case IsGPTImageGenerationModel(model) && group.Platform != PlatformOpenAI:
			return false
		case isGrokImageGenerationModel(model) && group.Platform != PlatformGrok:
			return false
		}
	}
	return !group.CustomModelsListEnabled() || groupCustomModelsListSupports(group, model)
}

func groupCustomModelsListSupports(group *Group, model string) bool {
	if group == nil {
		return false
	}
	for _, candidate := range group.ModelsListConfig.Models {
		candidate = strings.TrimSpace(candidate)
		if candidate == model || matchModelPattern(candidate, model) {
			return true
		}
	}
	return false
}

func (s *APIKeyService) validateAutoGroupIDs(ctx context.Context, userID int64, groupIDs []int64) error {
	normalized := normalizeAutoGroupIDs(groupIDs)
	if len(normalized) == 0 {
		return infraerrors.BadRequest("AUTO_GROUP_CANDIDATES_REQUIRED", "select at least one group for automatic routing")
	}
	available, err := s.GetAvailableGroups(ctx, userID)
	if err != nil {
		return fmt.Errorf("list available groups for automatic routing: %w", err)
	}
	availableByID := make(map[int64]Group, len(available))
	for _, group := range available {
		availableByID[group.ID] = group
	}
	candidates := make([]Group, 0, len(normalized))
	for _, groupID := range normalized {
		group, ok := availableByID[groupID]
		if !ok {
			return ErrGroupNotAllowed
		}
		candidates = append(candidates, group)
	}
	if !autoGroupCandidatesSharePlatform(candidates) {
		return infraerrors.BadRequest("AUTO_GROUP_CANDIDATE_PLATFORM_MISMATCH", "automatic routing candidates must use the same model type")
	}
	return nil
}

func autoGroupCandidatesSharePlatform(groups []Group) bool {
	if len(groups) < 2 {
		return true
	}
	platform := groups[0].Platform
	for _, group := range groups[1:] {
		if group.Platform != platform {
			return false
		}
	}
	return true
}

func (s *APIKeyService) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]APIKey, error) {
	keys, err := s.apiKeyRepo.SearchAPIKeys(ctx, userID, keyword, limit)
	if err != nil {
		return nil, fmt.Errorf("search api keys: %w", err)
	}
	return keys, nil
}

// GetUserAllowedGroupIDSet 返回 user_allowed_groups 授权给该用户的专属分组 ID 集合。
//
// 与 GetAvailableGroups 的区别：这里是「橱窗」语义（模型广场用），不检查订阅有效性，
// 也不关心分组是否活跃——仅回答"哪些专属分组对该用户可见"。返回值恒非 nil。
func (s *APIKeyService) GetUserAllowedGroupIDSet(ctx context.Context, userID int64) (map[int64]struct{}, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	allowed := make(map[int64]struct{}, len(user.AllowedGroups))
	for _, id := range user.AllowedGroups {
		allowed[id] = struct{}{}
	}
	return allowed, nil
}

// GetUserGroupRates 获取用户的专属分组倍率配置
// 返回 map[groupID]rateMultiplier
func (s *APIKeyService) GetUserGroupRates(ctx context.Context, userID int64) (map[int64]float64, error) {
	if s.userGroupRateRepo == nil {
		return nil, nil
	}
	rates, err := s.userGroupRateRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user group rates: %w", err)
	}
	return rates, nil
}

// CheckAPIKeyQuotaAndExpiry checks if the API key is valid for use (not expired, quota not exhausted)
// Returns nil if valid, error if invalid
func (s *APIKeyService) CheckAPIKeyQuotaAndExpiry(apiKey *APIKey) error {
	// Check expiration
	if apiKey.IsExpired() {
		return ErrAPIKeyExpired
	}

	// Check quota
	if apiKey.IsQuotaExhausted() {
		return ErrAPIKeyQuotaExhausted
	}

	return nil
}

// UpdateQuotaUsed updates the quota_used field after a request
// Also checks if quota is exhausted and updates status accordingly
func (s *APIKeyService) UpdateQuotaUsed(ctx context.Context, apiKeyID int64, cost float64) error {
	if cost <= 0 {
		return nil
	}

	type quotaStateReader interface {
		IncrementQuotaUsedAndGetState(ctx context.Context, id int64, amount float64) (*APIKeyQuotaUsageState, error)
	}

	if repo, ok := s.apiKeyRepo.(quotaStateReader); ok {
		state, err := repo.IncrementQuotaUsedAndGetState(ctx, apiKeyID, cost)
		if err != nil {
			return fmt.Errorf("increment quota used: %w", err)
		}
		if state != nil && state.Status == StatusAPIKeyQuotaExhausted && strings.TrimSpace(state.Key) != "" {
			s.InvalidateAuthCacheByKey(ctx, state.Key)
		}
		return nil
	}

	// Use repository to atomically increment quota_used
	newQuotaUsed, err := s.apiKeyRepo.IncrementQuotaUsed(ctx, apiKeyID, cost)
	if err != nil {
		return fmt.Errorf("increment quota used: %w", err)
	}

	// Check if quota is now exhausted and update status if needed
	apiKey, err := s.apiKeyRepo.GetByID(ctx, apiKeyID)
	if err != nil {
		return nil // Don't fail the request, just log
	}

	// If quota is set and now exhausted, update status
	if apiKey.Quota > 0 && newQuotaUsed >= apiKey.Quota {
		apiKey.Status = StatusAPIKeyQuotaExhausted
		// 只写 status：这条位于计费热路径，若整行回写会把刚刚原子递增的
		// quota_used 与限流用量按快照覆盖掉。
		if err := s.apiKeyRepo.Update(ctx, apiKey, APIKeyUpdateFields{Status: true}); err != nil {
			return nil // Don't fail the request
		}
		// Invalidate cache so next request sees the new status
		s.InvalidateAuthCacheByKey(ctx, apiKey.Key)
	}

	return nil
}

// GetRateLimitData returns rate limit usage and window state for an API key.
func (s *APIKeyService) GetRateLimitData(ctx context.Context, id int64) (*APIKeyRateLimitData, error) {
	return s.apiKeyRepo.GetRateLimitData(ctx, id)
}

// UpdateRateLimitUsage atomically increments rate limit usage counters in the DB.
func (s *APIKeyService) UpdateRateLimitUsage(ctx context.Context, apiKeyID int64, cost float64) error {
	if cost <= 0 {
		return nil
	}
	return s.apiKeyRepo.IncrementRateLimitUsage(ctx, apiKeyID, cost)
}
