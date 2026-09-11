package my_sites

import (
	"context"
	"log"
	"strings"
	"time"

	"transithub/backend/internal/modules/upstream"
)

// impactTimezone 与成本侧 reportingDate 保持一致。日界错开一天，
// 「近 7 天」就会算进/漏掉一整天的成本。
const impactTimezone = "Asia/Shanghai"

// OwnGroupRate 是一个自有分组当前对外的倍率。
type OwnGroupRate struct {
	Name       string
	Multiplier float64
}

// GroupImpact 回答倍率预警最该回答的那个问题：这个上游分组变价了，对我方影响多大。
//
// 光报「0.055x → 0.065x」没法判断要不要动作——同样涨 18%，一个月跑 3 块钱的通道
// 和一个月跑 3000 块的通道，紧急程度差了三个数量级。所以这里把「对接的是我方哪个
// 分组、那个分组现在卖多少、这条通道最近实际花了多少钱」一并带出来。
type GroupImpact struct {
	// OwnGroups 是对接了这个上游分组的自有分组，可能不止一个。
	OwnGroups []OwnGroupRate
	// CostCNY 是最近 Days 天这条通道的采购成本合计。
	// 【口径】Sub2API 的 account_cost 本身就是人民币，绝不能再乘汇率或充值倍率。
	CostCNY float64
	Days    int
	// CostResolved=false 表示这笔成本根本没查到，原因见 CostUnresolvedReason。
	// 必须与「确实是 0 元」区分开：前者要去补绑定，后者是真没跑量。
	CostResolved         bool
	CostUnresolvedReason UnresolvedReason
}

// HasOwnGroups 表示找到了对接关系。没有的话预警里就别硬凑一句空话。
func (g GroupImpact) HasOwnGroups() bool { return len(g.OwnGroups) > 0 }

// UpstreamGroupImpact 查一个上游分组（siteID + groupName）对我方的影响面。
//
// 只读，且失败一律降级成「查不到」而不是报错：预警本身比这些补充信息重要得多，
// 不能因为成本接口抽风就把整条预警吞掉。
func (s *Service) UpstreamGroupImpact(ctx context.Context, userID, adminAccountID, siteID, groupName string, days int) GroupImpact {
	impact := GroupImpact{Days: days}
	if days <= 0 {
		impact.Days = 7
	}

	state, err := s.repository.Get(ctx, userID, adminAccountID)
	if err != nil || state == nil {
		return impact
	}
	var fallbackAccounts map[string]string
	if s.connRepository != nil {
		connections, listErr := s.connRepository.ListRealConnections(ctx, userID, adminAccountID)
		if listErr != nil {
			log.Printf("[alert] 读取真实对接记录失败，成本账号回退不可用: %v", listErr)
		} else {
			fallbackAccounts = realConnectionAccountIndex(connections)
		}
	}

	siteID = strings.TrimSpace(siteID)
	groupName = strings.TrimSpace(groupName)

	var boundAccountID *string
	var boundOwnGroup string
	seen := make(map[string]struct{})
	for _, mapping := range state.Mappings {
		ownGroup := strings.TrimSpace(mapping.OwnGroup)
		for _, target := range mapping.UpstreamTargets {
			if strings.TrimSpace(target.SiteID) != siteID || strings.TrimSpace(target.GroupName) != groupName {
				continue
			}
			if _, dup := seen[ownGroup]; !dup {
				seen[ownGroup] = struct{}{}
				impact.OwnGroups = append(impact.OwnGroups, OwnGroupRate{Name: ownGroup})
			}
			// 成本只按第一条绑定算。同一个上游分组被多个自有分组引用时，
			// 把成本重复累加会虚增，宁可少报也不虚报。
			if boundAccountID == nil {
				accountID := normalizeSub2APIAccountID(target.Sub2APIAccountID)
				if accountID == nil {
					accountID = fallbackRealConnectionAccount(fallbackAccounts, target.SiteID, target.GroupName)
				}
				if accountID != nil {
					boundAccountID = accountID
					boundOwnGroup = ownGroup
				}
			}
		}
	}

	if len(impact.OwnGroups) == 0 {
		return impact
	}
	if state.Session.Platform != upstream.PlatformSub2API || !state.Session.IsAuthenticated() {
		impact.CostUnresolvedReason = ReasonQueryFailed
		return impact
	}

	// 自有分组的当前倍率必须实时查。State.OwnGroups 那份缓存在生产上是 null，
	// 拿它填出来的只会是一片「倍率未知」——2026-08-22 实测确认过。
	groups, err := s.platformService.FetchAdminAllGroups(state.Session)
	if err != nil {
		log.Printf("[alert] 读取自有分组失败，倍率预警不带我方倍率 site=%s group=%s err=%v", siteID, groupName, err)
		if boundAccountID == nil {
			impact.CostUnresolvedReason = ReasonUnbound
		} else {
			impact.CostUnresolvedReason = ReasonQueryFailed
		}
		return impact
	}

	groupIDs := make(map[string]string, len(groups))
	for index := range groups {
		name := strings.TrimSpace(groups[index].Name)
		groupIDs[name] = strings.TrimSpace(groups[index].ID)
		if groups[index].Multiplier == nil {
			continue
		}
		for position := range impact.OwnGroups {
			if impact.OwnGroups[position].Name == name {
				impact.OwnGroups[position].Multiplier = *groups[index].Multiplier
			}
		}
	}

	// 分组倍率查到了就先算数；成本要另外看有没有绑账号。
	if boundAccountID == nil {
		impact.CostUnresolvedReason = ReasonUnbound
		return impact
	}
	groupID := groupIDs[boundOwnGroup]
	if groupID == "" {
		impact.CostUnresolvedReason = ReasonGroupMissing
		return impact
	}

	startDate, endDate := impactDateRange(time.Now(), impact.Days)
	cost, err := s.platformService.FetchSub2APIAccountCostRange(state.Session, startDate, endDate, *boundAccountID, groupID)
	if err != nil {
		log.Printf("[alert] 读取账号成本失败，倍率预警不带成本 account=%s group=%s err=%v", *boundAccountID, groupID, err)
		impact.CostUnresolvedReason = ReasonQueryFailed
		return impact
	}
	impact.CostCNY = cost
	impact.CostResolved = true
	return impact
}

// GroupAccountingRange 返回各自有分组在指定日期区间内的营收与采购成本。
//
// 运营报告用它算分组毛利。取不到时返回空切片而不是错误：报告少一段
// 总好过整条发不出去。
func (s *Service) GroupAccountingRange(ctx context.Context, userID, adminAccountID, startDate, endDate string) []upstream.GroupAccounting {
	state, err := s.repository.Get(ctx, userID, adminAccountID)
	if err != nil || state == nil {
		return nil
	}
	if state.Session.Platform != upstream.PlatformSub2API || !state.Session.IsAuthenticated() {
		return nil
	}
	accounting, err := s.platformService.FetchSub2APIGroupAccountingRange(state.Session, startDate, endDate)
	if err != nil {
		log.Printf("[daily-report] 读取分组营收/成本失败 user_id=%s err=%v", userID, err)
		return nil
	}
	return accounting
}

// impactDateRange 返回「含今天在内的最近 days 天」的起止日期（Asia/Shanghai）。
func impactDateRange(now time.Time, days int) (string, string) {
	location, err := time.LoadLocation(impactTimezone)
	if err != nil {
		location = time.FixedZone("CST", 8*3600)
	}
	end := now.In(location)
	start := end.AddDate(0, 0, -(days - 1))
	return start.Format("2006-01-02"), end.Format("2006-01-02")
}
