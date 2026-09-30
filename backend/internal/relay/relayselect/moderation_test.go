package relayselect

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// moderationLogs 是主节点的审核记录仓储（只记写入；按单机的条件累计违规次数）。
type moderationLogs struct {
	mu   sync.Mutex
	logs []service.ContentModerationLog
}

func (r *moderationLogs) CreateLog(_ context.Context, log *service.ContentModerationLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, *log)
	return nil
}

func (r *moderationLogs) CountFlaggedByUserSince(_ context.Context, userID int64, _ time.Time, excludeCyber bool) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, l := range r.logs {
		if l.UserID != nil && *l.UserID == userID && l.Flagged && l.Action != "hash_block" && (!excludeCyber || l.Action != "cyber_policy") {
			n++
		}
	}
	return n, nil
}

func (r *moderationLogs) ListLogs(context.Context, service.ContentModerationLogFilter) ([]service.ContentModerationLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (r *moderationLogs) CleanupExpiredLogs(context.Context, time.Time, time.Time) (*service.ContentModerationCleanupResult, error) {
	return &service.ContentModerationCleanupResult{}, nil
}
func (r *moderationLogs) UpdateLogEmailSent(context.Context, int64, bool) error { return nil }

type banUsers struct {
	service.UserRepository
	mu     sync.Mutex
	users  map[int64]*service.User
	banned []int64
}

func (u *banUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if user, ok := u.users[id]; ok {
		cp := *user
		return &cp, nil
	}
	return nil, service.ErrUserNotFound
}

func (u *banUsers) Update(_ context.Context, user *service.User, _ service.UserUpdateFields) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.users[user.ID] = user
	if user.Status == service.StatusDisabled {
		u.banned = append(u.banned, user.ID)
	}
	return nil
}

func violation(t *testing.T, userID int64, action string) []byte {
	t.Helper()
	raw, err := json.Marshal(service.ContentModerationLog{
		UserID: &userID, Flagged: true, Action: action, Mode: "pre_block", HighestCategory: "violence", HighestScore: 0.9,
		InputExcerpt: "the user's actual words", MatchedKeyword: "secret-keyword", Error: "upstream said no",
		ViolationCount: 99, AutoBanned: true,
	})
	require.NoError(t, err)
	return raw
}

// 违规次数在主节点跨节点累计、到阈值封号（单机同一段代码）；主节点只记不含输入内容的最小记录（带节点）；
// 只认这台节点最近准入过的用户。
func TestModerationViolationsOnTheMaster(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, config.RunModeSimple, apiKeyAccount(1, "one"))
	logs := &moderationLogs{}
	users := &banUsers{users: map[int64]*service.User{3: {ID: 3, Email: "u3@example.com", Status: service.StatusActive, Role: service.RoleUser}}}
	w.sel.deps.Users = users
	w.sel.deps.Moderation = service.NewContentModerationService(memSettings{values: map[string]string{
		service.SettingKeyRiskControlEnabled:      "true",
		service.SettingKeyContentModerationConfig: `{"auto_ban_enabled":true,"ban_threshold":2,"violation_window_hours":24}`,
	}}, logs, nil, nil, users, nil, nil, nil)

	_, err := w.sel.ModerationViolation(ctx, testNode, &relayv1.ModerationViolationRequest{Log: violation(t, 3, "block")})
	require.Equal(t, codes.PermissionDenied, status.Code(err), "the node never admitted this user")
	require.Empty(t, logs.logs)

	_, err = w.sel.Admit(ctx, testNode, &relayv1.AdmitRequest{Credential: &relayv1.AdmitRequest_ApiKey{ApiKey: "sk-a"}, ClientIp: "5.6.7.8", Method: "POST", Path: "/v1/responses"})
	require.NoError(t, err)
	_, err = w.sel.ModerationViolation(ctx, testNode+1, &relayv1.ModerationViolationRequest{Log: violation(t, 3, "block")})
	require.Equal(t, codes.PermissionDenied, status.Code(err), "another node cannot report for users it did not admit")

	first, err := w.sel.ModerationViolation(ctx, testNode, &relayv1.ModerationViolationRequest{Log: violation(t, 3, "block")})
	require.NoError(t, err)
	require.Equal(t, int32(1), first.GetViolationCount(), "the master counts; the node's own numbers are ignored")
	require.False(t, first.GetAutoBanned())
	require.Len(t, logs.logs, 1)
	row := logs.logs[0]
	require.Equal(t, testNode, *row.NodeID)
	require.Empty(t, row.InputExcerpt, "input content stays on the node")
	require.Empty(t, row.MatchedKeyword)
	require.Empty(t, row.Error)
	require.Equal(t, "u3@example.com", row.UserEmail, "the recipient comes from the master's user record")
	require.Equal(t, "violence", row.HighestCategory)

	second, err := w.sel.ModerationViolation(ctx, testNode, &relayv1.ModerationViolationRequest{Log: violation(t, 3, "block")})
	require.NoError(t, err)
	require.Equal(t, int32(2), second.GetViolationCount())
	require.True(t, second.GetAutoBanned())
	require.True(t, second.GetAutoBanJustApplied())
	require.Equal(t, []int64{3}, users.banned)

	sent, err := w.sel.ModerationNotify(ctx, testNode, &relayv1.ModerationNotifyRequest{Log: violation(t, 3, "block"), AutoBanJustApplied: true})
	require.NoError(t, err)
	require.False(t, sent.GetEmailSent(), "no mail service on this master")

	_, err = w.sel.ModerationViolation(ctx, testNode, &relayv1.ModerationViolationRequest{Log: []byte("{")})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
