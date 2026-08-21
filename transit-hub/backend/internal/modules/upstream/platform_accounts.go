package upstream

import (
	"log"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AdminGroupAccountInfo 是「某个 admin 分组下的账号(sub2api) / 渠道(new-api)」的平台中性信息，
// 供 connection_health 分组健康主列表的账号弹窗展示。
//
// 安全约束：这里只保留展示所需的基础字段与探活策略相关字段，绝不包含 credentials / key /
// token / cookie 等敏感明文——上游响应里的敏感字段在解析阶段就被丢弃，不会进入本结构。
//
// 字段可空性：不同平台/不同上游版本返回的字段并不一致，凡是「缺省时应展示为占位符」的
// 数值/布尔字段一律用指针，nil 表示上游未提供，由前端决定展示 "-" 还是隐藏，避免把
// 「上游没给」误当成「值为 0」。
type AdminGroupAccountInfo struct {
	ID                      string   // sub2api account id / new-api channel id
	Name                    string   // 账号或渠道名称
	Platform                string   // 上游平台标识（openai / anthropic / ...），可能为空
	Type                    string   // sub2api 账号类型 / new-api channel 类型（数值转字符串）
	Status                  string   // 状态（字符串或数值转字符串）
	Priority                *int     // 优先级
	GroupPriority           *int     // Sub2API account_groups.priority for this group
	Concurrency             *int     // 并发（sub2api）
	CurrentConcurrency      *int     // Sub2API 管理 API 返回的实时并发
	SchedulerScore          *float64 // Sub2API 在当前分组中的实时基础调度分
	UsageP95FirstTokenMs    *int     // Sub2API 最近一小时真实请求的首 Token 延迟 P95（至少 3 个样本）
	UsageSampleCount        int      // 上述 P95 使用的有效样本数；不足 3 时只保留样本数
	RateMultiplier          *float64 // Sub2API admin 转发账号记录自身的 rate_multiplier，不代表上游 API Key 所属分组倍率。
	// CostRateMultiplier 是 Sub2API 已按"手工值 > 新鲜探测值 > 列值"解析好的上游成本倍率。
	// nil 表示无人声明过该账号成本（含探测失败而列上只剩建表默认 1.0 的情况）。
	// 【不要用 RateMultiplier 代替它做成本核算】：那一列常年停在默认 1.0000，
	// 生产上 mcgrox.top 的账号手工成本 0.04、上游标称 0.8，只看列值会差 20 倍。
	// 也【不要】在 nil 时回退 1.0 或回退上游标称倍率——没人声明就是不知道。
	CostRateMultiplier *float64
	// CostRateSource 是上面那个倍率的出处："manual" / "probe" / "column" / "none"。
	CostRateSource string
	LoadFactor              *int     // 负载因子（sub2api）
	Weight                  *int     // 权重（仅 new-api channel 有；sub2api 为 nil）
	Models                  string   // 模型列表（new-api channel.models 等）
	GroupIDs                []string // 所属分组 ID/名称列表
	Schedulable             *bool    // 是否可调度（sub2api）
	RateLimitResetAt        *time.Time
	OverloadUntil           *time.Time
	TempUnschedulableUntil  *time.Time
	TempUnschedulableReason string
	ExpiresAt               *time.Time
	AutoPauseOnExpired      *bool
	// SchedulabilitySource 是 Sub2API 给出的权威停用来源：manual / automatic / none。
	// manual=管理员手动关闭（永久优先，永不自动恢复）；automatic=系统自动停用（可恢复）；
	// none=当前无停用来源。空值表示 Sub2API 未提供该字段，消费方必须失败关闭。
	// 严禁再用 Schedulable + Status 的组合推断来源。
	SchedulabilitySource    string
	SchedulabilityReason    string
	SchedulabilityChangedAt *time.Time
	// BaseURL 是 new-api channel 转发到的上游 provider 地址（channel.base_url）。
	// 独立探活需要用它 + channel key 直接对上游发起 OpenAI 兼容请求。sub2api 账号在列表阶段
	// 拿不到 base_url，探活前再从单账号导出凭据里解析，故此处可能为空。
	BaseURL string
}

// ListAdminGroupAccounts 平台中性地读取某个 admin 分组下的账号/渠道列表。
// sub2api 走 /api/v1/admin/accounts?group=<groupID>，new-api 走 channel 查询。
// 返回的每个条目都不含敏感字段。
func (s *PlatformService) ListAdminGroupAccounts(session Session, group AdminGroupInfo) ([]AdminGroupAccountInfo, error) {
	switch session.Platform {
	case PlatformNewAPI:
		return s.listNewAPIGroupChannels(session, group)
	default:
		return s.listSub2APIGroupAccounts(session, group)
	}
}

// listSub2APIGroupAccounts 分页拉取 sub2api 某分组下的账号。
// 注意 query 参数是 group=<分组ID>（不是 group_id）。逐页拉取直到没有下一页或达到 total。
func (s *PlatformService) listSub2APIGroupAccounts(session Session, group AdminGroupInfo) ([]AdminGroupAccountInfo, error) {
	if session.Platform != PlatformSub2API || !session.IsAuthenticated() {
		return nil, newRequestError(ErrorAuth, PlatformSub2API)
	}
	if strings.TrimSpace(group.ID) == "" {
		return []AdminGroupAccountInfo{}, nil
	}
	authOptions := adminAuthOptions(session)

	const pageSize = 100
	const maxPages = 100 // 安全上限，防止上游分页字段异常导致死循环
	accounts := make([]AdminGroupAccountInfo, 0)
	for page := 1; page <= maxPages; page++ {
		pageURL := session.BaseURL + "/api/v1/admin/accounts?group=" + url.QueryEscape(group.ID) +
			"&page=" + strconvInt(int64(page)) + "&page_size=" + strconvInt(pageSize) + "&include_scheduler_score=1"
		response, err := s.httpClient.requestJSON(pageURL, authOptions)
		if err != nil {
			return nil, err
		}
		items := dataArray(response.Payload)
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			accounts = append(accounts, parseSub2APIAccount(record, group.ID))
		}
		total, hasTotal := paginationTotal(response.Payload)
		if hasTotal && page*pageSize >= total {
			break
		}
		if !hasTotal && len(items) < pageSize {
			break
		}
	}
	// Auto 优先级直接复用 Sub2API 已记录的真实请求 TTFT。这里只读取管理 usage，
	// 不会为评分发起任何模型请求；接口不可用或样本不足时由 connection_health
	// 回退到 TransitHub 已有探活事件。
	s.enrichSub2APIAccountUsageLatency(session, group.ID, accounts, time.Now())
	return accounts, nil
}

// ListAdminAllAccounts 拉取本方 Sub2API 的全部账号，不按分组过滤。
//
// 调价映射需要它来把"上游数据源"绑定到具体账号并读取该账号的真实成本倍率：
// 绑定关系是跨分组的（一个上游站点的账号可能挂在任意自有分组下，也可能一个
// 分组都没挂），按分组逐个拉既慢又会漏掉未分组账号。
//
// 与 listSub2APIGroupAccounts 的差别仅在于不带 group 参数，因此分组维度的字段
// （GroupPriority / SchedulerScore）在这里恒为 nil——调价映射不需要它们。
// 这里也不做 usage latency 富化：那是给调度评分用的，每次多打一轮接口不值当。
func (s *PlatformService) ListAdminAllAccounts(session Session) ([]AdminGroupAccountInfo, error) {
	if session.Platform != PlatformSub2API || !session.IsAuthenticated() {
		return nil, newRequestError(ErrorAuth, PlatformSub2API)
	}
	authOptions := adminAuthOptions(session)

	const pageSize = 100
	const maxPages = 100 // 安全上限，防止上游分页字段异常导致死循环
	accounts := make([]AdminGroupAccountInfo, 0)
	for page := 1; page <= maxPages; page++ {
		pageURL := session.BaseURL + "/api/v1/admin/accounts?page=" + strconvInt(int64(page)) +
			"&page_size=" + strconvInt(pageSize)
		response, err := s.httpClient.requestJSON(pageURL, authOptions)
		if err != nil {
			return nil, err
		}
		items := dataArray(response.Payload)
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			accounts = append(accounts, parseSub2APIAccount(record, ""))
		}
		total, hasTotal := paginationTotal(response.Payload)
		if hasTotal && page*pageSize >= total {
			break
		}
		if !hasTotal && len(items) < pageSize {
			break
		}
	}
	return accounts, nil
}

const (
	sub2APIUsageMetricWindow       = time.Hour
	sub2APIUsageSampleLimit        = 20
	sub2APIUsageMinReliableSamples = 3
	sub2APIUsagePageSize           = 100
	sub2APIUsageMaxPages           = 20
)

type sub2APIUsageLatencyMetric struct {
	p95Ms       *int
	sampleCount int
}

func (s *PlatformService) enrichSub2APIAccountUsageLatency(session Session, groupID string, accounts []AdminGroupAccountInfo, now time.Time) {
	if len(accounts) == 0 {
		return
	}
	accountIDs := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		accountIDs[account.ID] = struct{}{}
	}
	samplesByAccount := make(map[string][]int, len(accounts))
	cutoff := now.Add(-sub2APIUsageMetricWindow)
	query := url.Values{}
	query.Set("group_id", strings.TrimSpace(groupID))
	query.Set("page_size", strconv.Itoa(sub2APIUsagePageSize))
	query.Set("sort_by", "created_at")
	query.Set("sort_order", "desc")
	for page := 1; page <= sub2APIUsageMaxPages; page++ {
		query.Set("page", strconv.Itoa(page))
		response, err := s.httpClient.requestJSON(session.BaseURL+"/api/v1/admin/usage?"+query.Encode(), adminAuthOptions(session))
		if err != nil {
			break
		}
		items := dataArray(response.Payload)
		if len(items) == 0 {
			break
		}
		reachedCutoff := collectSub2APIUsageLatencySamples(items, groupID, accountIDs, samplesByAccount, cutoff, now)
		if allSub2APIUsageSampleLimitsReached(accountIDs, samplesByAccount) || reachedCutoff {
			break
		}
		total, hasTotal := paginationTotal(response.Payload)
		if hasTotal && page*sub2APIUsagePageSize >= total {
			break
		}
		if !hasTotal && len(items) < sub2APIUsagePageSize {
			break
		}
	}

	metrics := sub2APIUsageLatencyByAccount(samplesByAccount)
	for index := range accounts {
		metric, ok := metrics[accounts[index].ID]
		if !ok {
			continue
		}
		accounts[index].UsageP95FirstTokenMs = metric.p95Ms
		accounts[index].UsageSampleCount = metric.sampleCount
	}
}

func collectSub2APIUsageLatencySamples(
	items []any,
	groupID string,
	accountIDs map[string]struct{},
	samplesByAccount map[string][]int,
	cutoff time.Time,
	now time.Time,
) bool {
	reachedCutoff := false
	for _, item := range items {
		record := dataRecord(item)
		accountID := firstStringy(record, []string{"account_id", "accountId"})
		if accountID == "" {
			continue
		}
		if recordGroupID := firstStringy(record, []string{"group_id", "groupId"}); recordGroupID != "" && recordGroupID != groupID {
			continue
		}
		createdAt := parseFlexibleTime(firstAny(record, []string{"created_at", "createdAt"}))
		if createdAt != nil && createdAt.Before(cutoff) {
			reachedCutoff = true
		}
		if _, tracked := accountIDs[accountID]; !tracked || len(samplesByAccount[accountID]) >= sub2APIUsageSampleLimit {
			continue
		}
		firstTokenMs := firstInt(record, []string{"first_token_ms", "firstTokenMs"})
		if firstTokenMs == nil || *firstTokenMs <= 0 || createdAt == nil || createdAt.Before(cutoff) || createdAt.After(now) {
			continue
		}
		samplesByAccount[accountID] = append(samplesByAccount[accountID], *firstTokenMs)
	}
	return reachedCutoff
}

func allSub2APIUsageSampleLimitsReached(accountIDs map[string]struct{}, samplesByAccount map[string][]int) bool {
	for accountID := range accountIDs {
		if len(samplesByAccount[accountID]) < sub2APIUsageSampleLimit {
			return false
		}
	}
	return true
}

func sub2APIUsageLatencyByAccount(samplesByAccount map[string][]int) map[string]sub2APIUsageLatencyMetric {
	metrics := make(map[string]sub2APIUsageLatencyMetric, len(samplesByAccount))
	for accountID, samples := range samplesByAccount {
		metric := sub2APIUsageLatencyMetric{sampleCount: len(samples)}
		if len(samples) >= sub2APIUsageMinReliableSamples {
			sorted := append([]int(nil), samples...)
			sort.Ints(sorted)
			index := (95*len(sorted) + 99) / 100
			value := sorted[index-1]
			metric.p95Ms = &value
		}
		metrics[accountID] = metric
	}
	return metrics
}

// sub2APIAccountBaseURL 从账号记录里取上游地址，供"按域名给绑定建议"使用。
//
// Sub2API 的账号响应会带一份脱敏后的 credentials：RedactCredentials 只剥离
// SensitiveCredentialKeys（api_key / access_token / cookie / private_key 等），
// base_url 不在其中，因此列表阶段就能拿到。取不到时返回空串，调用方只是少一条
// 预填建议，绝不能因此影响成本取值。
func sub2APIAccountBaseURL(record map[string]any) string {
	if direct := firstStringy(record, []string{"base_url", "baseURL", "baseUrl"}); direct != "" {
		return direct
	}
	credentials, ok := record["credentials"].(map[string]any)
	if !ok {
		return ""
	}
	return firstStringy(credentials, []string{"base_url", "baseURL", "baseUrl"})
}

// parseSub2APIAccount 把 sub2api 账号原始记录解析为平台中性结构，主动丢弃 credentials 等敏感字段。
func parseSub2APIAccount(record map[string]any, groupID string) AdminGroupAccountInfo {
	account := AdminGroupAccountInfo{
		ID:                      groupID2(record),
		Name:                    safeString(record, "name"),
		Type:                    stringOrNumberField(record, []string{"type"}),
		Status:                  stringOrNumberField(record, []string{"status"}),
		Priority:                firstInt(record, []string{"priority"}),
		GroupPriority:           sub2APIGroupPriority(record, groupID),
		Concurrency:             firstInt(record, []string{"concurrency"}),
		CurrentConcurrency:      firstInt(record, []string{"current_concurrency", "currentConcurrency"}),
		SchedulerScore:          sub2APIGroupSchedulerScore(record, groupID),
		RateMultiplier:          firstNumber(record, []string{"rate_multiplier", "rateMultiplier"}),
		CostRateMultiplier:      firstNumber(record, []string{"cost_rate_multiplier", "costRateMultiplier"}),
		CostRateSource:          firstStringy(record, []string{"cost_rate_source", "costRateSource"}),
		BaseURL:                 sub2APIAccountBaseURL(record),
		LoadFactor:              firstInt(record, []string{"load_factor", "loadFactor"}),
		GroupIDs:                parseGroupIDList(record),
		Schedulable:             firstBoolValue(record, []string{"schedulable"}),
		RateLimitResetAt:        parseFlexibleTime(firstAny(record, []string{"rate_limit_reset_at", "rateLimitResetAt"})),
		OverloadUntil:           parseFlexibleTime(firstAny(record, []string{"overload_until", "overloadUntil"})),
		TempUnschedulableUntil:  parseFlexibleTime(firstAny(record, []string{"temp_unschedulable_until", "tempUnschedulableUntil"})),
		TempUnschedulableReason: firstStringy(record, []string{"temp_unschedulable_reason", "tempUnschedulableReason"}),
		ExpiresAt:               parseFlexibleTime(firstAny(record, []string{"expires_at", "expiresAt"})),
		AutoPauseOnExpired:      firstBoolValue(record, []string{"auto_pause_on_expired", "autoPauseOnExpired"}),
		// 来源字段按原文透传：字段缺失时保持空串，由消费方失败关闭，
		// 绝不在解析层补默认值（补 none 就等于替 Sub2API 猜来源）。
		SchedulabilitySource:    firstStringy(record, []string{"schedulability_source", "schedulabilitySource"}),
		SchedulabilityReason:    firstStringy(record, []string{"schedulability_reason", "schedulabilityReason"}),
		SchedulabilityChangedAt: parseFlexibleTime(firstAny(record, []string{"schedulability_changed_at", "schedulabilityChangedAt"})),
	}
	if p := firstString(record, []string{"platform"}); p != nil {
		account.Platform = *p
	}
	if m := firstString(record, []string{"models"}); m != nil {
		account.Models = *m
	}
	return account
}

func sub2APIGroupPriority(record map[string]any, groupID string) *int {
	if priority := firstInt(record, []string{"group_priority", "groupPriority"}); priority != nil {
		return priority
	}
	rawScores, ok := record["scheduler_scores"].([]any)
	if !ok {
		return nil
	}
	for _, raw := range rawScores {
		score := dataRecord(raw)
		if firstStringy(score, []string{"group_id", "groupId"}) != groupID {
			continue
		}
		return firstInt(score, []string{"group_priority", "groupPriority", "priority"})
	}
	return nil
}

func sub2APIGroupSchedulerScore(record map[string]any, groupID string) *float64 {
	rawScores, ok := record["scheduler_scores"].([]any)
	if ok {
		for _, raw := range rawScores {
			score := dataRecord(raw)
			if firstStringy(score, []string{"group_id", "groupId"}) != groupID {
				continue
			}
			if value := firstNumber(score, []string{"base_score", "baseScore"}); value != nil {
				return value
			}
		}
	}
	if score := dataRecord(record["scheduler_score"]); score != nil {
		return firstNumber(score, []string{"base_score", "baseScore"})
	}
	return nil
}

// listNewAPIGroupChannels 读取 new-api 某分组下的 channel 列表。
// 优先使用 /api/channel/search?group=<分组名>（server 端已按分组过滤，兼容较老部署也普遍支持）；
// search 失败时兜底 /api/channel/ 分页拉取后在本地按「逗号分组精确匹配」过滤。
func (s *PlatformService) listNewAPIGroupChannels(session Session, group AdminGroupInfo) ([]AdminGroupAccountInfo, error) {
	if session.Platform != PlatformNewAPI || !session.IsAuthenticated() {
		return nil, newRequestError(ErrorAuth, PlatformNewAPI)
	}
	groupName := strings.TrimSpace(group.Name)
	if groupName == "" {
		return []AdminGroupAccountInfo{}, nil
	}

	channels, err := s.searchNewAPIGroupChannels(session, groupName)
	if err == nil {
		return channels, nil
	}
	log.Printf("[connection-health] new-api /api/channel/search 拉取失败，回退 /api/channel/ 本地过滤 base_url=%s group=%s err=%v", session.BaseURL, groupName, err)
	return s.listNewAPIChannelsWithLocalFilter(session, groupName)
}

// searchNewAPIGroupChannels 通过 /api/channel/search?group= 分页读取指定分组的 channel。
func (s *PlatformService) searchNewAPIGroupChannels(session Session, groupName string) ([]AdminGroupAccountInfo, error) {
	cookieOptions := newAPIAuthOptions(session)
	const pageSize = 100
	const maxPages = 100
	channels := make([]AdminGroupAccountInfo, 0)
	for page := 1; page <= maxPages; page++ {
		pageURL := session.BaseURL + "/api/channel/search?group=" + url.QueryEscape(groupName) +
			"&p=" + strconvInt(int64(page)) + "&page_size=" + strconvInt(pageSize)
		response, err := s.httpClient.requestJSON(pageURL, cookieOptions)
		if err != nil {
			return nil, err
		}
		items := dataArray(response.Payload)
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			channels = append(channels, parseNewAPIChannel(record))
		}
		total, hasTotal := paginationTotal(response.Payload)
		if hasTotal && page*pageSize >= total {
			break
		}
		if !hasTotal && len(items) < pageSize {
			break
		}
	}
	return channels, nil
}

// listNewAPIChannelsWithLocalFilter 兜底：分页拉取 /api/channel/ 全量 channel，
// 再在本地按「逗号分组精确匹配」过滤出属于 groupName 的 channel。
// 精确匹配：channel.group 按逗号拆分后逐段 TrimSpace 比较，避免 "vip" 命中 "vip2"（substring）。
func (s *PlatformService) listNewAPIChannelsWithLocalFilter(session Session, groupName string) ([]AdminGroupAccountInfo, error) {
	cookieOptions := newAPIAuthOptions(session)
	const pageSize = 100
	const maxPages = 100
	channels := make([]AdminGroupAccountInfo, 0)
	for page := 1; page <= maxPages; page++ {
		pageURL := session.BaseURL + "/api/channel/?p=" + strconvInt(int64(page)) + "&page_size=" + strconvInt(pageSize)
		response, err := s.httpClient.requestJSON(pageURL, cookieOptions)
		if err != nil {
			return nil, err
		}
		items := dataArray(response.Payload)
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if !channelBelongsToGroup(record, groupName) {
				continue
			}
			channels = append(channels, parseNewAPIChannel(record))
		}
		total, hasTotal := paginationTotal(response.Payload)
		if hasTotal && page*pageSize >= total {
			break
		}
		if !hasTotal && len(items) < pageSize {
			break
		}
	}
	return channels, nil
}

// channelBelongsToGroup 判断 channel 是否属于指定分组：channel.group 是逗号分隔的分组名字符串，
// 拆分后按段精确匹配，不做 substring 匹配。
func channelBelongsToGroup(record map[string]any, groupName string) bool {
	raw := firstString(record, []string{"group"})
	if raw == nil {
		return false
	}
	for _, part := range strings.Split(*raw, ",") {
		if strings.TrimSpace(part) == groupName {
			return true
		}
	}
	return false
}

// parseNewAPIChannel 把 new-api channel 原始记录解析为平台中性结构，主动丢弃 key 等敏感字段。
func parseNewAPIChannel(record map[string]any) AdminGroupAccountInfo {
	channel := AdminGroupAccountInfo{
		ID:       groupID2(record),
		Name:     safeString(record, "name"),
		Type:     stringOrNumberField(record, []string{"type"}),
		Status:   stringOrNumberField(record, []string{"status"}),
		Priority: firstInt(record, []string{"priority"}),
		Weight:   firstInt(record, []string{"weight"}),
	}
	if m := firstString(record, []string{"models"}); m != nil {
		channel.Models = *m
	}
	if b := firstString(record, []string{"base_url", "baseUrl"}); b != nil {
		channel.BaseURL = strings.TrimSpace(*b)
	}
	// channel.group 是逗号分隔的分组名字符串，拆成列表方便前端展示。
	if raw := firstString(record, []string{"group"}); raw != nil {
		parts := make([]string, 0)
		for _, part := range strings.Split(*raw, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				parts = append(parts, trimmed)
			}
		}
		channel.GroupIDs = parts
	}
	return channel
}

// stringOrNumberField 依次尝试把字段解析成字符串；不是字符串时回退按数值解析并转成字符串。
// 用于 status/type 这类上游可能返回字符串也可能返回数值枚举的字段。
func stringOrNumberField(record map[string]any, keys []string) string {
	if v := firstString(record, keys); v != nil {
		return *v
	}
	if n := firstNumber(record, keys); n != nil {
		return strconv.FormatInt(int64(*n), 10)
	}
	return ""
}

// firstInt 复用 firstNumber 读取整数字段，缺省或非法时返回 nil（区分「未提供」和「值为 0」）。
func firstInt(record map[string]any, keys []string) *int {
	if n := firstNumber(record, keys); n != nil {
		v := int(*n)
		return &v
	}
	return nil
}

// firstBoolValue 读取布尔字段，仅当上游明确给出 bool 时返回指针，否则返回 nil。
func firstBoolValue(record map[string]any, keys []string) *bool {
	for _, key := range keys {
		if b, ok := record[key].(bool); ok {
			return &b
		}
	}
	return nil
}

// parseGroupIDList 解析账号所属分组 ID 列表，兼容数值数组、字符串数组和逗号分隔字符串三种形态。
func parseGroupIDList(record map[string]any) []string {
	for _, key := range []string{"group_ids", "groupIds"} {
		value, ok := record[key]
		if !ok {
			continue
		}
		if arr, ok := value.([]any); ok {
			ids := make([]string, 0, len(arr))
			for _, item := range arr {
				switch typed := item.(type) {
				case float64:
					ids = append(ids, strconv.FormatInt(int64(typed), 10))
				case string:
					if trimmed := strings.TrimSpace(typed); trimmed != "" {
						ids = append(ids, trimmed)
					}
				}
			}
			return ids
		}
		if str, ok := value.(string); ok {
			ids := make([]string, 0)
			for _, part := range strings.Split(str, ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					ids = append(ids, trimmed)
				}
			}
			return ids
		}
	}
	return nil
}
