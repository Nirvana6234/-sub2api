package my_sites

import (
	"context"
	"time"

	"transithub/backend/internal/modules/upstream"
)

const (
	ErrorAuthRequired           = "admin.mySites.errors.authRequired"
	ErrorAdminOnly              = "admin.mySites.errors.adminOnly"
	ErrorRequest                = "admin.mySites.errors.request"
	ErrorUnknown                = "admin.mySites.errors.unknown"
	ErrorInvalidAutoPricingConf = "admin.mySites.errors.invalidAutoPricingConfig"
	ErrorConnectionExists       = "admin.mySites.errors.connectionExists"
	ErrorManagedDeleteOnly      = "admin.mySites.errors.managedDeleteOnly"
	// ErrorTesterUnavailable：没有注入探活能力，测试接口不可用（部署装配问题）。
	ErrorTesterUnavailable = "admin.mySites.errors.testerUnavailable"
	// ErrorCredentialNotFound：这个上游分组下找不到可用凭据，通常是还没对接。
	ErrorCredentialNotFound = "admin.mySites.errors.credentialNotFound"
)

// 上游 Key 测试各阶段的失败原因，前端据此显示可读文案。
const (
	ErrorModelListUnavailable = "admin.mySites.errors.modelListUnavailable"
	ErrorModelListEmpty       = "admin.mySites.errors.modelListEmpty"
	ErrorChatProbeFailed      = "admin.mySites.errors.chatProbeFailed"
)

const (
	ProvisioningModeLegacy   = "legacy"
	ProvisioningModeManaged  = "managed"
	ProvisioningModeExisting = "existing"
	ConnectionStatusActive   = "active"
)

// MappingRequest 前端保存映射关系时的请求体，包含自动调价配置字段。
// FixedIncrease / PercentageIncrease / AdjustThresholdPercent 使用指针区分「未传」和「传了 0」。
type MappingRequest struct {
	OwnGroup                  string             `json:"ownGroup"`
	UpstreamTargets           []UpstreamGroupRef `json:"upstreamTargets"`
	EnableAutoPricing         bool               `json:"enableAutoPricing"`
	AutoPricingSource         string             `json:"autoPricingSource"`
	PrimaryUpstreamSiteID     string             `json:"primaryUpstreamSiteId"`
	PrimaryUpstreamGroupName  string             `json:"primaryUpstreamGroupName"`
	AutoPricingStrategy       string             `json:"autoPricingStrategy"`
	FixedIncrease             *float64           `json:"fixedIncrease"`
	PercentageIncrease        *float64           `json:"percentageIncrease"`
	AdjustThresholdPercent    *float64           `json:"adjustThresholdPercent"`
	MinMultiplier             *float64           `json:"minMultiplier"`
	MaxMultiplier             *float64           `json:"maxMultiplier"`
	EnableAutoPricingNotify   bool               `json:"enableAutoPricingNotify"`
	AutoPricingNotifyBotIDs   []string           `json:"autoPricingNotifyBotIds"`
	AutoPricingNotifyTemplate string             `json:"autoPricingNotifyTemplate"`
}

// UpstreamGroupRef 上游分组的引用（站点 ID + 分组名）。
type UpstreamGroupRef struct {
	SiteID    string `json:"siteId"`
	GroupName string `json:"groupName"`
	// Sub2APIAccountID 把这个调价数据源绑定到本方 Sub2API 的某个账号，用于按
	// 该账号的真实成本倍率核算毛利。nil = 未绑定（旧数据一律如此，必须兼容）。
	//
	// 为什么要人工绑定而不按域名自动认：同一个上游域名下往往挂着多个成本迥异的
	// 账号（tntapi.com 有 3 个，探测倍率 0.16 / 0.079 / 无），域名不足以判定用哪个。
	// 未绑定时成本按"未知"处理，不参与毛利计算——绝不拿上游标称倍率顶替，
	// 那正是 mcgrox.top 按 0.8 算出 -1130% 毛利、而真实手工成本只有 0.04 的原因。
	Sub2APIAccountID *string `json:"sub2apiAccountId,omitempty"`
}

// TargetAccountCost is an authoritative CNY purchase cost assigned to one
// configured upstream target through its explicitly bound local account.
type TargetAccountCost struct {
	SiteID    string
	GroupName string
	CostCNY   float64
}

// UnresolvedReason 说明某个上游目标为什么归集不到采购成本。
type UnresolvedReason string

const (
	// ReasonUnbound 该上游目标没有绑定本方 Sub2API 账号。
	ReasonUnbound UnresolvedReason = "unbound"
	// ReasonGroupMissing 自有分组在本方 Sub2API 上找不到（改名或已删除）。
	ReasonGroupMissing UnresolvedReason = "group_missing"
	// ReasonAmbiguous 同一个「账号 + 自有分组」被多个上游目标引用，归属不明。
	ReasonAmbiguous UnresolvedReason = "ambiguous"
	// ReasonQueryFailed 成本接口调用失败，或未返回 account_cost 字段。
	ReasonQueryFailed UnresolvedReason = "query_failed"
)

// UnresolvedTarget 是一个「有消费但算不出采购成本」的上游目标。
//
// 这类目标必须在简报里单独列出来，不能只是从统计里悄悄消失：
// 否则总成本偏低，而看的人无从知道少算了谁。
type UnresolvedTarget struct {
	OwnGroup  string
	SiteID    string
	GroupName string
	Reason    UnresolvedReason
}

// AccountCostResult 同时带回算得出的成本和算不出的目标。
type AccountCostResult struct {
	Costs      []TargetAccountCost
	Unresolved []UnresolvedTarget
}

// State 用户的分组映射持久化状态，存储于 my_site_states 表。
type State struct {
	UserID         string           `json:"-"`
	AdminAccountID string           `json:"-"`
	BaseURL        string           `json:"baseUrl"`
	Email          string           `json:"email"`
	Session        upstream.Session `json:"-"`
	Mappings       []GroupMapping   `json:"mappings"`
	OwnGroups      []GroupOption    `json:"ownGroups"`
	USDToCNYRate   float64          `json:"usdToCnyRate"`
}

// GroupMapping 一个自有分组到多个上游分组的映射关系，并可配置该分组的自动调价策略。
// 自动调价配置绑定在自有分组上，计算时根据 AutoPricingSource 从关联上游中取参考倍率。
type GroupMapping struct {
	OwnGroup                  string                `json:"ownGroup"`
	UpstreamTargets           []UpstreamGroupRef    `json:"upstreamTargets"`
	EnableAutoPricing         bool                  `json:"enableAutoPricing"`
	AutoPricingSource         string                `json:"autoPricingSource"`
	PrimaryUpstreamSiteID     string                `json:"primaryUpstreamSiteId"`
	PrimaryUpstreamGroupName  string                `json:"primaryUpstreamGroupName"`
	AutoPricingStrategy       string                `json:"autoPricingStrategy"`
	FixedIncrease             float64               `json:"fixedIncrease"`
	PercentageIncrease        float64               `json:"percentageIncrease"`
	AdjustThresholdPercent    float64               `json:"adjustThresholdPercent"`
	MinMultiplier             *float64              `json:"minMultiplier"`
	MaxMultiplier             *float64              `json:"maxMultiplier"`
	EnableAutoPricingNotify   bool                  `json:"enableAutoPricingNotify"`
	AutoPricingNotifyBotIDs   []string              `json:"autoPricingNotifyBotIds"`
	AutoPricingNotifyTemplate string                `json:"autoPricingNotifyTemplate"`
	LastAutoPricingRun        *AutoPricingRunStatus `json:"lastAutoPricingRun,omitempty"`
}

// AutoPricingRunStatus 是后端写入的最近一次自动调价执行状态。
// Status、Reason 和 Trigger 均为稳定机器可读值；错误场景只记录安全原因码，不暴露远端响应或凭据。
type AutoPricingRunStatus struct {
	Status           string    `json:"status"`
	Reason           string    `json:"reason,omitempty"`
	Trigger          string    `json:"trigger"`
	RanAt            time.Time `json:"ranAt"`
	OldReference     *float64  `json:"oldReference"`
	NewReference     *float64  `json:"newReference"`
	OldOwnMultiplier *float64  `json:"oldOwnMultiplier"`
	NewOwnMultiplier *float64  `json:"newOwnMultiplier"`
	TargetMultiplier *float64  `json:"targetMultiplier"`
}

// AutoPricingRunRequest 手动触发自动调价请求体。
type AutoPricingRunRequest struct {
	OwnGroup string `json:"ownGroup"`
}

// AutoPricingRunResponse 手动触发自动调价的响应体，保持原有 mapping 字段不变并追加结构化结果。
type AutoPricingRunResponse struct {
	Result  AutoPricingRunStatus `json:"result"`
	Mapping GroupMapping         `json:"mapping"`
}

// StatusResponse 保存映射后返回的状态。
type StatusResponse struct {
	Authenticated bool           `json:"authenticated"`
	BaseURL       string         `json:"baseUrl"`
	Email         string         `json:"email"`
	Mappings      []GroupMapping `json:"mappings"`
}

// MappingOptionsResponse mapping-options 接口的响应体。
type MappingOptionsResponse struct {
	OwnGroups                 []MappingOwnGroupOption     `json:"ownGroups"`
	Mappings                  []GroupMapping              `json:"mappings"`
	UpstreamTargetMultipliers []MappingUpstreamTargetRate `json:"upstreamTargetMultipliers"`
	StaleOwnGroups            []string                    `json:"staleOwnGroups,omitempty"`
	StaleTargets              []UpstreamGroupRef          `json:"staleTargets,omitempty"`
	ConnectionCapabilities    *ConnectionCapabilities     `json:"connectionCapabilities,omitempty"`
	// CostAccounts 是本方 Sub2API 的账号清单，供前端把调价数据源绑定到具体账号，
	// 并按该账号的真实成本倍率算毛利。拉取失败时为空数组——绑定界面会退化成
	// 无候选可选，但已保存的绑定不受影响（成本此时按未知处理）。
	CostAccounts []MappingCostAccount `json:"costAccounts"`
}

// MappingCostAccount 是一个可被绑定的 Sub2API 账号及其成本倍率。
//
// CostRateMultiplier 由 Sub2API 按"手工值 > 新鲜探测值 > 列值"解析后给出，
// nil 表示无人声明过该账号成本。前端遇到 nil 必须把这条数据源排除出毛利计算，
// 不得回退成上游标称倍率——上游标称的是它的售价，不是我们的进货成本。
type MappingCostAccount struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	BaseURL            string   `json:"baseUrl,omitempty"`
	CostRateMultiplier *float64 `json:"costRateMultiplier"`
	CostRateSource     string   `json:"costRateSource"`
}

// MappingUpstreamTargetRate is the current effective multiplier for an
// upstream target referenced by a pricing mapping. It is read live from the
// upstream session, rather than the normal site-sync cache.
type MappingUpstreamTargetRate struct {
	SiteID     string   `json:"siteId"`
	GroupName  string   `json:"groupName"`
	Multiplier *float64 `json:"multiplier"`
	Stale      bool     `json:"stale"`
	// Source distinguishes a fresh upstream read from the last successful
	// TransitHub observation used while the upstream is temporarily unavailable.
	Source string `json:"source,omitempty"`
}

// ConnectionCapabilities describes the platform-specific fields required by
// RealConnect so the Vue layer does not need to own upstream channel semantics.
type ConnectionCapabilities struct {
	Mode                        string              `json:"mode"`
	RequiresGroupType           bool                `json:"requiresGroupType"`
	RequiresChannelType         bool                `json:"requiresChannelType"`
	ChannelTypes                []ChannelTypeOption `json:"channelTypes,omitempty"`
	SuggestedChannelTypeByGroup map[string]int      `json:"suggestedChannelTypeByGroup,omitempty"`
}

type ChannelTypeOption struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// MappingOwnGroupOption 自有分组选项，包含 ID、平台、状态、专属性等属性。
type MappingOwnGroupOption struct {
	ID               string  `json:"id"`
	SiteName         string  `json:"siteName"`
	GroupName        string  `json:"groupName"`
	Multiplier       float64 `json:"multiplier"`
	Platform         string  `json:"platform"`
	Status           string  `json:"status"`
	IsExclusive      bool    `json:"isExclusive"`
	SubscriptionType string  `json:"subscriptionType"`
}

// GroupOption 自有分组的名称与倍率，缓存于 State.OwnGroups。
type GroupOption struct {
	Name       string  `json:"name"`
	Multiplier float64 `json:"multiplier"`
}

// RealConnectRequest 真实对接请求体。
// 前端传入上游站点 ID、上游分组 ID/名称和自有分组 ID 列表。
// GroupType 可选：为空时后端从上游站点缓存的分组列表中自动识别平台类型。
type RealConnectRequest struct {
	UpstreamSiteID    string   `json:"upstreamSiteId"`
	UpstreamGroupID   string   `json:"upstreamGroupId"`
	UpstreamGroupName string   `json:"upstreamGroupName"`
	GroupType         string   `json:"groupType"`
	ChannelType       int      `json:"channelType"`
	OwnGroupIDs       []string `json:"ownGroupIds"`
	// AddToPricingMapping is a pointer so rolling deployments preserve the old
	// behavior: an omitted field still adds the target to automatic pricing.
	AddToPricingMapping *bool  `json:"addToPricingMapping"`
	OperationID         string `json:"operationId"`
}

// RealDisconnectRequest 取消真实对接请求体。
// Mode: "unlink" 仅删除本地绑定记录，"delete-key" 同时删除该对接创建的上游 Key。
// 兼容旧客户端的 "full"，但它也只删除上游 Key，绝不删除 Admin 转发账号。
type RealDisconnectRequest struct {
	ConnectionID         string `json:"connectionId"`
	Mode                 string `json:"mode"`
	RemovePricingMapping *bool  `json:"removePricingMapping"`
}

// RealBindRequest 手动绑定请求体。
// 用户从上游 key 列表中选择要绑定的 key，此接口仅创建绑定记录，不调用任何 platform API。
type RealBindRequest struct {
	UpstreamSiteID    string   `json:"upstreamSiteId"`
	UpstreamGroupID   string   `json:"upstreamGroupId"`
	UpstreamGroupName string   `json:"upstreamGroupName"`
	UpstreamKeyID     string   `json:"upstreamKeyId"`
	UpstreamKey       string   `json:"upstreamKey"`
	OwnGroupIDs       []string `json:"ownGroupIds"`
	GroupType         string   `json:"groupType"`
	// AdminGroupID and AdminResourceID identify an already-created account or
	// channel on the current admin site. They are optional only for compatibility
	// with older clients, whose records continue to be stored as legacy bindings.
	AdminGroupID        string `json:"adminGroupId"`
	AdminResourceID     string `json:"adminResourceId"`
	AddToPricingMapping *bool  `json:"addToPricingMapping"`
	OperationID         string `json:"operationId"`
}

// UpstreamCredentialOption is the non-secret credential metadata returned to
// the browser for existing-resource binding. The backend resolves the full key
// again after submission and never trusts a secret echoed by the client.
type UpstreamCredentialOption struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	GroupID    string `json:"groupId"`
	GroupName  string `json:"groupName"`
	Status     string `json:"status"`
	KeyPreview string `json:"keyPreview"`
}

// UpstreamKeyTester 是「上游 Key 连通性测试」对探活能力的窄依赖。
//
// 【为什么用基础类型而不是直接引 connection_health】：connection_health 已经
// import 了 my_sites（分组健康要读 RealConnection 和倍率快照），这边反过来 import
// 会形成循环。所以这里只声明能力，由 httpserver 注入一个适配器。
type UpstreamKeyTester interface {
	// ListModels 打 {baseURL}/v1/models，返回模型 ID 列表。
	ListModels(ctx context.Context, baseURL string, key string) ([]string, error)
	// ProbeChat 用该 key 对指定模型发一次最小请求（max_tokens=1）。
	ProbeChat(ctx context.Context, baseURL string, key string, model string) UpstreamProbeResult
}

// UpstreamProbeResult 是一次真实请求探测的结果。Result 是 connection_health
// 的结果分类（ok / auth_failed / model_not_found / ...），Detail 已脱敏。
type UpstreamProbeResult struct {
	OK        bool
	Result    string
	LatencyMs int
	Detail    string
}

// UpstreamKeyTestRequest 描述要测哪个上游 Key。
//
// UpstreamKeyID 为空时后端自己挑：优先用该站点+分组已对接连接记录里的那个 Key，
// 没有再退回该分组下第一个可用凭据。这样列表行内的「测试」按钮不需要前端先去
// 查一遍 key 列表。**客户端永远不传明文 key**，后端自己去上游解析。
type UpstreamKeyTestRequest struct {
	UpstreamSiteID    string `json:"upstreamSiteId"`
	UpstreamGroupID   string `json:"upstreamGroupId"`
	UpstreamGroupName string `json:"upstreamGroupName"`
	UpstreamKeyID     string `json:"upstreamKeyId"`
	// Model 为空时由后端从模型列表里挑一个最便宜的候选。
	Model string `json:"model"`
}

// UpstreamKeyTestStage 是测试里一个阶段的结果。ErrorKey 是 i18n key，
// Detail 是给运维看的补充说明（已脱敏，可能为空）。
type UpstreamKeyTestStage struct {
	OK        bool   `json:"ok"`
	Skipped   bool   `json:"skipped"`
	LatencyMs int    `json:"latencyMs"`
	ErrorKey  string `json:"errorKey"`
	Detail    string `json:"detail"`
}

// UpstreamKeyTestResponse 分两段汇报：先看 key 能不能列出模型，再看拿其中一个
// 模型发真请求能不能出词。
//
// 【为什么两段都要】只列模型会漏掉最常见的一类坑：模型列表里挂着一堆名字，
// 实际请求却回「无可用渠道」503。反过来只发真请求，失败时又分不清是 key 废了
// 还是这个模型没挂上。分开报才能直接指向要改什么。
type UpstreamKeyTestResponse struct {
	KeyID       string               `json:"keyId"`
	KeyName     string               `json:"keyName"`
	KeyPreview  string               `json:"keyPreview"`
	Models      UpstreamKeyTestStage `json:"models"`
	Chat        UpstreamKeyTestStage `json:"chat"`
	ModelCount  int                  `json:"modelCount"`
	ModelSample []string             `json:"modelSample"`
	TestedModel string               `json:"testedModel"`
}

// UpstreamKeyModelsResponse is the non-secret model inventory used before a
// user chooses which model to probe.
type UpstreamKeyModelsResponse struct {
	KeyID      string   `json:"keyId"`
	KeyName    string   `json:"keyName"`
	KeyPreview string   `json:"keyPreview"`
	Models     []string `json:"models"`
}

// UpstreamKeyGroupSnapshot is the non-secret, current association between an
// upstream credential and its pricing group. It is intentionally suitable for
// both UI reads and background accounting snapshots.
type UpstreamKeyGroupSnapshot struct {
	SiteID     string
	KeyID      string
	GroupID    string
	GroupName  string
	Multiplier *float64
}

// AdminResourceOption describes an existing admin forwarding resource without
// exposing credentials. GroupIDs are the actual groups read from the admin API.
type AdminResourceOption struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Status   string   `json:"status"`
	Platform string   `json:"platform"`
	GroupIDs []string `json:"groupIds"`
}

// RealConnectResponse 真实对接成功后返回的绑定记录。
type RealConnectResponse struct {
	Connection RealConnection `json:"connection"`
}

// RealConnection 一条真实对接的绑定记录，存储于 real_connections 表。
// 记录上游 key、admin 账号、关联的自有分组 ID 列表等完整信息。
//
// 注意：此结构体有两个含义不同的 admin account 字段，不要混淆：
//   - WorkspaceAdminAccountID: TransitHub 工作区归属字段（对应 admin_accounts 表），
//     用于 workspace 数据隔离，标识这条绑定记录属于哪个 admin workspace。
//     数据库列名为 workspace_admin_account_id，与其他业务表的 admin_account_id 语义相同。
//   - AdminAccountID: 上游平台的 admin 转发账号 ID，是真实对接业务逻辑中的字段，
//     表示在上游 sub2api/new-api 站点上为 key 创建或绑定的管理员账号。
type RealConnection struct {
	ID                      string   `json:"id"`
	UserID                  string   `json:"-"`
	WorkspaceAdminAccountID string   `json:"-"` // TransitHub workspace 归属（隔离字段）
	UpstreamSiteID          string   `json:"upstreamSiteId"`
	UpstreamGroupID         string   `json:"upstreamGroupId"`
	UpstreamGroupName       string   `json:"upstreamGroupName"`
	UpstreamKeyID           string   `json:"upstreamKeyId"`
	UpstreamKey             string   `json:"upstreamKey"`
	AdminAccountID          string   `json:"adminAccountId"` // 上游平台 admin 转发账号 ID（业务字段）
	AdminAccountName        string   `json:"adminAccountName"`
	OwnGroupIDs             []string `json:"ownGroupIds"`
	OwnGroupNames           []string `json:"ownGroupNames"`
	GroupType               string   `json:"groupType"`
	ProvisioningMode        string   `json:"provisioningMode"`
	Status                  string   `json:"status"`
	UpstreamPlatform        string   `json:"upstreamPlatform"`
	AdminPlatform           string   `json:"adminPlatform"`
	PricingMappingEnabled   bool     `json:"pricingMappingEnabled"`
	OperationID             string   `json:"-"`
	CanDeleteRemote         bool     `json:"canDeleteRemote"`
	CreatedAt               string   `json:"createdAt"`
}
