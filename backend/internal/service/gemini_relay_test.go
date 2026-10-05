//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type geminiCooldownRecorder struct{ ids []int64 }

func (r *geminiCooldownRecorder) ReportGeminiCooldown(id int64) { r.ids = append(r.ids, id) }

type geminiRateLimitRepo struct {
	AccountRepository
	limited map[int64]time.Time
}

func (r *geminiRateLimitRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.limited[id] = resetAt
	return nil
}

func geminiCodeAssistAccount() *Account {
	return &Account{ID: 7, Platform: PlatformGemini, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"oauth_type": "code_assist", "project_id": "p", "access_token": "t"}}
}

// 从节点：Code Assist 账号 429 且上游没给重置时间时，档位冷却交给主节点（上报，不在本机写限流）；
// 上游给了重置时间或不是这类账号时仍在本机算（再作为账号级限流事件上报）。
func TestGeminiCooldownIsReportedToTheMasterOnRelayNodes(t *testing.T) {
	repo := &geminiRateLimitRepo{limited: map[int64]time.Time{}}
	reporter := &geminiCooldownRecorder{}
	s := &GeminiMessagesCompatService{accountRepo: repo}
	s.SetCooldownReporter(reporter)

	s.handleGeminiUpstreamError(context.Background(), geminiCodeAssistAccount(), http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"message":"quota"}}`))
	require.Equal(t, []int64{7}, reporter.ids)
	require.Empty(t, repo.limited, "the cooldown is computed and written by the master")

	// 上游给了重置时间：按它写（本机的仓储在从节点上是事件）。
	s.handleGeminiUpstreamError(context.Background(), geminiCodeAssistAccount(), http.StatusTooManyRequests, http.Header{},
		[]byte(`{"error":{"message":"Please retry in 30s."}}`))
	require.Len(t, reporter.ids, 1)
	require.Contains(t, repo.limited, int64(7))
}

// 单机：没有上报出口，按档位冷却在本机写。
func TestGeminiCooldownIsWrittenLocallyOnASingleServer(t *testing.T) {
	repo := &geminiRateLimitRepo{limited: map[int64]time.Time{}}
	s := &GeminiMessagesCompatService{accountRepo: repo}
	s.handleGeminiUpstreamError(context.Background(), geminiCodeAssistAccount(), http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"message":"quota"}}`))
	require.Contains(t, repo.limited, int64(7))
	require.True(t, repo.limited[7].After(time.Now()))
}
