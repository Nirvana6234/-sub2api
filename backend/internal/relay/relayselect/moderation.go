package relayselect

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// admittedWindow 是违规上报认可的准入窗口（设计 3.4）：只认这台节点最近准入过的用户，防止被攻破的节点
// 随意上报别人的违规、让主节点封号。WebSocket 连接每一轮都会续上。
const admittedWindow = 30 * time.Minute

// admittedUsers 按节点记最近准入过的用户（准入、选号、WebSocket 每一轮时记）。
type admittedUsers struct {
	mu    sync.Mutex
	seen  map[int64]map[int64]time.Time
	calls int
}

func (a *admittedUsers) note(nodeID, userID int64, now time.Time) {
	if userID <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.seen == nil {
		a.seen = map[int64]map[int64]time.Time{}
	}
	users := a.seen[nodeID]
	if users == nil {
		users = map[int64]time.Time{}
		a.seen[nodeID] = users
	}
	users[userID] = now
	// 顺带清掉过期的（每 4096 次一轮），不另起定时任务。
	if a.calls++; a.calls%4096 == 0 {
		for n, us := range a.seen {
			for u, at := range us {
				if now.Sub(at) > admittedWindow {
					delete(us, u)
				}
			}
			if len(us) == 0 {
				delete(a.seen, n)
			}
		}
	}
}

func (a *admittedUsers) recent(nodeID, userID int64, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	at, ok := a.seen[nodeID][userID]
	return ok && now.Sub(at) <= admittedWindow
}

// reportedViolation 解出从节点上报的审核记录，只留主节点记违规、封号、通知要用的字段（设计 3.4：
// 输入内容摘录、命中的关键词、错误信息留在从节点，不进主节点的库）；收件人按主节点库里的用户。
func (s *selector) reportedViolation(ctx context.Context, nodeID int64, raw []byte) (*service.ContentModerationLog, error) {
	var in service.ContentModerationLog
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid moderation log")
	}
	if in.UserID == nil || *in.UserID <= 0 || !in.Flagged {
		return nil, status.Error(codes.InvalidArgument, "a violation needs a user and a flagged record")
	}
	if !s.admitted.recent(nodeID, *in.UserID, s.now()) {
		slog.Warn("relay: moderation violation for a user this node has not admitted recently", "node_id", nodeID, "user_id", *in.UserID)
		return nil, status.Error(codes.PermissionDenied, "user was not admitted by this node recently")
	}
	node := nodeID
	out := &service.ContentModerationLog{
		RequestID: in.RequestID, UserID: in.UserID, APIKeyID: in.APIKeyID, APIKeyName: in.APIKeyName,
		GroupID: in.GroupID, GroupName: in.GroupName, Endpoint: in.Endpoint, Provider: in.Provider, Model: in.Model,
		Mode: in.Mode, Action: in.Action, Flagged: true, HighestCategory: in.HighestCategory, HighestScore: in.HighestScore,
		CategoryScores: in.CategoryScores, ThresholdSnapshot: in.ThresholdSnapshot, NodeID: &node, CreatedAt: s.now(),
	}
	if s.deps.Users != nil {
		if user, err := s.deps.Users.GetByID(ctx, *in.UserID); err == nil && user != nil {
			out.UserEmail = user.Email
		}
	}
	return out, nil
}

// ModerationViolation 见 RelayControl.ModerationViolation。
func (s *selector) ModerationViolation(ctx context.Context, nodeID int64, req *relayv1.ModerationViolationRequest) (*relayv1.ModerationViolationResponse, error) {
	if s.deps.Moderation == nil {
		return &relayv1.ModerationViolationResponse{}, nil
	}
	log, err := s.reportedViolation(ctx, nodeID, req.GetLog())
	if err != nil {
		return nil, err
	}
	just, err := s.deps.Moderation.ApplyRelayViolation(ctx, log)
	if err != nil {
		return nil, err
	}
	return &relayv1.ModerationViolationResponse{ViolationCount: int32(log.ViolationCount), AutoBanned: log.AutoBanned, AutoBanJustApplied: just}, nil
}

// ModerationNotify 见 RelayControl.ModerationNotify。
func (s *selector) ModerationNotify(ctx context.Context, nodeID int64, req *relayv1.ModerationNotifyRequest) (*relayv1.ModerationNotifyResponse, error) {
	if s.deps.Moderation == nil {
		return &relayv1.ModerationNotifyResponse{}, nil
	}
	log, err := s.reportedViolation(ctx, nodeID, req.GetLog())
	if err != nil {
		return nil, err
	}
	var in service.ContentModerationLog
	_ = json.Unmarshal(req.GetLog(), &in)
	log.ViolationCount, log.AutoBanned = in.ViolationCount, in.AutoBanned
	if req.GetCyber() {
		// cyber 通知里带上游的原话（上游错误信息，不是用户输入），只用于这封信，不落库。
		log.Error = in.Error
	}
	sent, err := s.deps.Moderation.NotifyRelayViolation(ctx, log, req.GetAutoBanJustApplied(), req.GetCyber())
	if err != nil {
		return nil, err
	}
	return &relayv1.ModerationNotifyResponse{EmailSent: sent}, nil
}

// flaggedHashPage 是整份拉取名单时每页的条数。
const flaggedHashPage = 5000

// FetchFlaggedHashes 见 RelayControl.FetchFlaggedHashes。
func (s *selector) FetchFlaggedHashes(ctx context.Context, _ int64, req *relayv1.FetchFlaggedHashesRequest) (*relayv1.FetchFlaggedHashesResponse, error) {
	if s.deps.Moderation == nil {
		return &relayv1.FetchFlaggedHashesResponse{}, nil
	}
	hashes, next, err := s.deps.Moderation.ScanFlaggedInputHashes(ctx, req.GetCursor(), flaggedHashPage)
	if err != nil {
		return nil, err
	}
	return &relayv1.FetchFlaggedHashesResponse{Hashes: hashes, NextCursor: next}, nil
}

// RecordFlaggedHash 见 RelayControl.RecordFlaggedHash。
func (s *selector) RecordFlaggedHash(ctx context.Context, nodeID int64, req *relayv1.RecordFlaggedHashRequest) (*relayv1.RecordFlaggedHashResponse, error) {
	if s.deps.Moderation == nil {
		return &relayv1.RecordFlaggedHashResponse{}, nil
	}
	if err := s.deps.Moderation.RecordRelayFlaggedHash(ctx, req.GetHash()); err != nil {
		slog.Warn("relay: record flagged hash from node failed", "node_id", nodeID, "error", err)
		return nil, status.Error(codes.InvalidArgument, "invalid flagged hash")
	}
	return &relayv1.RecordFlaggedHashResponse{}, nil
}
