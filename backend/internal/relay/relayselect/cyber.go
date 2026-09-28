package relayselect

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/relay/master"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// cyberHitTextLimit 是 cyber 命中消息里上游 message、body 的上限（单机截到 4 KiB，见 markOpenAICyberPolicyEvent），
// 超过的截断，不让从节点把大块内容写进风控记录和运维日志。
const cyberHitTextLimit = 8 << 10

// CyberPolicyHit 上游 cyber 策略命中（设计 3.4）：用单机同一段代码（handler.CyberPolicyRecorder）写会话屏蔽标记、
// 风控记录和运维日志。用户、Key、分组、账号取自这次选号的记录，屏蔽的键由选号时上送的查询键推导，
// 都不信消息里的；消息只提供上游标记和请求上的事实。每次请求只记一次（与单机一致）。
func (s *selector) CyberPolicyHit(_ context.Context, nodeID int64, req *relayv1.CyberPolicyHitRequest) (*relayv1.CyberPolicyHitResponse, error) {
	s.mu.Lock()
	sel, ok := s.selections[req.GetSelectionId()]
	if !ok || sel.nodeID != nodeID || sel.account == nil || sel.account.ID != req.GetAccountId() || sel.apiKey == nil {
		s.mu.Unlock()
		return nil, master.ErrSelectionNotFound
	}
	if sel.request.cyberRecorded {
		s.mu.Unlock()
		return &relayv1.CyberPolicyHitResponse{}, nil
	}
	sel.request.cyberRecorded = true
	apiKey, account, lookup := sel.apiKey, sel.account, sel.cyber
	s.mu.Unlock()

	hit := handler.CyberPolicyHit{
		Mark: service.CyberPolicyMark{
			Code:           "cyber_policy",
			Message:        truncateText(req.GetMessage(), cyberHitTextLimit),
			Body:           truncateText(req.GetBody(), cyberHitTextLimit),
			UpstreamStatus: int(req.GetUpstreamStatus()),
			UpstreamInTok:  int(req.GetUpstreamInputTokens()),
			UpstreamOutTok: int(req.GetUpstreamOutputTokens()),
		},
		RequestID:       req.GetRequestId(),
		ClientRequestID: req.GetClientRequestId(),
		Platform:        req.GetPlatform(),
		Model:           req.GetModel(),
		RequestPath:     req.GetRequestPath(),
		Stream:          req.GetStream(),
		InboundEndpoint: req.GetInboundEndpoint(),
		UserAgent:       req.GetUserAgent(),
		ClientIP:        req.GetClientIp(),
		CreatedAt:       s.now(),
	}
	if ms := req.GetCreatedAtUnixMs(); ms > 0 {
		hit.CreatedAt = time.UnixMilli(ms)
	}
	blockScope, blockKeys := lookup.BlockWritePlan()
	node := nodeID
	s.recordCyber(hit, handler.CyberPolicySubject{APIKey: apiKey, Account: account, NodeID: &node}, blockScope, blockKeys)
	return &relayv1.CyberPolicyHitResponse{}, nil
}

// cyberLookup 把选号请求里的 cyber 查询键转成服务层的结构（没带时为空：从节点上屏蔽开关是关的）。
func cyberLookup(c *relayv1.CyberSessionLookup) service.CyberSessionLookup {
	if c == nil {
		return service.CyberSessionLookup{}
	}
	return service.CyberSessionLookup{
		ExplicitKey: c.GetExplicitKey(), ScopeKey: c.GetScopeKey(), TranscriptKeys: c.GetTranscriptKeys(),
		TranscriptTruncated: c.GetTranscriptTruncated(), PreLatestUserKey: c.GetPreLatestUserKey(),
	}
}

// truncateText 按字节截断（不切开 UTF-8 字符）。
func truncateText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && cut < len(s) && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut]
}
