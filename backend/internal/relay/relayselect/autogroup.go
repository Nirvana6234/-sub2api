package relayselect

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/relay/keycodec"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// SwitchAutoGroup 见 RelayControl.SwitchAutoGroup：本地 tryOpenAIAutoGroupFailover 在主节点的那一半。Key 按准入同一段
// 复查（核对当前分组是候选），把当前分组记为失败，按同一个选组器选下一个这次请求没试过的候选；订阅按本地的规则换。
// 换到的分组顺带做计费资格复查，结果交给从节点在本地复查的那一处用。
func (s *selector) SwitchAutoGroup(ctx context.Context, nodeID int64, req *relayv1.SwitchAutoGroupRequest) (*relayv1.SwitchAutoGroupResponse, error) {
	none := &relayv1.SwitchAutoGroupResponse{}
	current := req.GetCurrentGroupId()
	if current == 0 || req.GetModel() == "" {
		return none, nil
	}
	adm, rej, err := s.admitAPIKey(ctx, req.GetApiKey(), req.GetClientIp(), req.GetMethod(), req.GetPath(), nil, autoGroupChoice{pinned: current})
	if err != nil {
		return nil, err
	}
	if rej != nil || !adm.APIKey.AutoGroup {
		return none, nil
	}
	s.admitted.note(nodeID, adm.APIKey.User.ID, s.now())
	failed := map[int64]struct{}{current: {}}
	for _, id := range req.GetFailedGroupIds() {
		failed[id] = struct{}{}
	}
	resolved, err := s.deps.APIKeys.ResolveAutoGroupForModelExcluding(ctx, adm.APIKey, req.GetModel(), failed)
	if err != nil || resolved == nil || resolved.GroupID == nil {
		return none, nil
	}
	if _, tried := failed[*resolved.GroupID]; tried {
		return none, nil
	}
	subscription := adm.Billing.Subscription
	if resolved.Group != nil && resolved.Group.IsSubscriptionType() {
		subscription, _ = s.deps.APIKeys.GetActiveSubscriptionForGroup(ctx, resolved.UserID, *resolved.GroupID)
	} else if subscription != nil && subscription.GroupID != *resolved.GroupID {
		subscription = nil
	}
	out := &relayv1.SwitchAutoGroupResponse{Switched: true}
	if out.ApiKey, err = keycodec.EncodeAPIKey(resolved); err != nil {
		return nil, err
	}
	if out.Subscription, err = keycodec.EncodeSubscription(subscription); err != nil {
		return nil, err
	}
	if err := s.checkBilling(ctx, nodeID, req.GetHeldQuota(), resolved, subscription, service.QuotaPlatform(ctx, resolved)); err != nil {
		out.BillingRejection = gatewayRejection(billingRejection(err, false)).GetRejection()
	}
	return out, nil
}

// ReportAutoGroupResult 见 RelayControl.ReportAutoGroupResult：本地自动分组中间件请求结束时的观察。只认这台节点
// 最近准入过的用户；分组不是这把 Key 当前选定的，选组器自己会忽略（与本地一样）。
func (s *selector) ReportAutoGroupResult(ctx context.Context, nodeID int64, req *relayv1.AutoGroupResult) (*relayv1.AutoGroupResultAck, error) {
	ack := &relayv1.AutoGroupResultAck{}
	groupID := req.GetGroupId()
	if groupID <= 0 || req.GetModel() == "" {
		return ack, nil
	}
	apiKey, err := s.deps.APIKeys.GetByKey(ctx, req.GetApiKey())
	if err != nil || apiKey == nil || !apiKey.AutoGroup || !s.admitted.recent(nodeID, apiKey.UserID, s.now()) {
		return ack, nil
	}
	observed := *apiKey
	observed.GroupID, observed.Group = &groupID, nil
	var firstTokenMs *int64
	if req.GetHasFirstTokenMs() {
		v := req.GetFirstTokenMs()
		firstTokenMs = &v
	}
	if s.observeAutoGroup != nil {
		s.observeAutoGroup(&observed, req.GetModel(), int(req.GetStatus()), firstTokenMs)
		return ack, nil
	}
	s.deps.APIKeys.ObserveAutoGroupRequestResult(&observed, req.GetModel(), int(req.GetStatus()), firstTokenMs)
	return ack, nil
}

// endRequest 结束这台节点上的一次请求（有的话）：同一请求后来的选号在查到请求之前就被拒（Key 停用、自动分组换到
// 从节点接不了的分组等）时，从节点照拒绝写响应或交给主节点转发，这次请求到此为止。先占着的用户槽要放掉：
// 交给主节点转发时主节点要重新占。
func (s *selector) endRequest(nodeID int64, requestID string) {
	s.mu.Lock()
	r := s.requests[requestKey{nodeID: nodeID, requestID: requestID}]
	s.mu.Unlock()
	if r != nil {
		s.dropRequest(r)
	}
}
