package service

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordingAccountActions struct {
	mu       sync.Mutex
	applies  int
	notifies []struct{ autoBan, cyber bool }
	ban      bool
	sent     bool
}

func (r *recordingAccountActions) Apply(_ context.Context, _ *ContentModerationConfig, log *ContentModerationLog) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applies++
	log.ViolationCount = 7
	log.AutoBanned = r.ban
	return r.ban
}

func (r *recordingAccountActions) Notify(_ context.Context, _ *ContentModerationConfig, _ *ContentModerationLog, autoBan, cyber bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notifies = append(r.notifies, struct{ autoBan, cyber bool }{autoBan, cyber})
	return r.sent
}

// 命中后的账号动作都经 accountActions（从节点换成经主节点执行的实现）：记录里的违规次数、封号、
// 已发邮件取自它，顺序与原来一致（先判封号再写记录）。
func TestModerationHitGoesThroughAccountActions(t *testing.T) {
	repo := &contentModerationTestRepo{}
	svc := NewContentModerationService(&contentModerationTestSettingRepo{values: map[string]string{SettingKeyRiskControlEnabled: "true"}},
		repo, nil, nil, nil, nil, nil, nil)
	actions := &recordingAccountActions{ban: true, sent: true}
	svc.SetAccountActions(actions)
	uid := int64(9)
	cfg := &ContentModerationConfig{}

	svc.persistContentModerationLog(context.Background(), cfg, &ContentModerationLog{UserID: &uid, Flagged: true, Action: "block"}, "h", false, true)
	logs := repo.snapshotLogs()
	require.Len(t, logs, 1)
	require.Equal(t, 7, logs[0].ViolationCount)
	require.True(t, logs[0].AutoBanned)
	require.True(t, logs[0].EmailSent)
	require.Equal(t, 1, actions.applies)
	require.Equal(t, []struct{ autoBan, cyber bool }{{true, false}}, actions.notifies)

	// 不做账号动作的记录（命中过的输入直接拦截）不调。
	svc.persistContentModerationLog(context.Background(), cfg, &ContentModerationLog{UserID: &uid, Flagged: true, Action: "hash_block"}, "h", false, false)
	require.Equal(t, 1, actions.applies)
	require.Len(t, actions.notifies, 1)
}

func TestCyberHitGoesThroughAccountActions(t *testing.T) {
	repo := &cyberOrderingTestRepo{}
	svc := NewContentModerationService(&contentModerationTestSettingRepo{values: map[string]string{SettingKeyRiskControlEnabled: "true"}},
		repo, nil, nil, nil, nil, nil, nil)
	actions := &recordingAccountActions{ban: true, sent: true}
	svc.SetAccountActions(actions)
	svc.RecordCyberPolicyEvent(context.Background(), CyberPolicyRecordInput{UserID: 1, Model: "gpt-5", Endpoint: "/v1/responses", UpstreamMessage: "x"})
	require.Equal(t, 1, actions.applies)
	require.Equal(t, []struct{ autoBan, cyber bool }{{true, true}}, actions.notifies)
	require.Equal(t, []string{"create", "update_email_sent"}, repo.snapshot(), "the record is written before the email, then marked sent")

	// 配置排除 cyber 计数：不判封号，照常通知。
	repo2 := &cyberOrderingTestRepo{}
	svc2 := NewContentModerationService(&contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: `{"cyber_policy_exclude_from_ban_count":true}`,
	}}, repo2, nil, nil, nil, nil, nil, nil)
	actions2 := &recordingAccountActions{}
	svc2.SetAccountActions(actions2)
	svc2.RecordCyberPolicyEvent(context.Background(), CyberPolicyRecordInput{UserID: 1, Model: "gpt-5", Endpoint: "/v1/responses", UpstreamMessage: "x"})
	require.Equal(t, 0, actions2.applies)
	require.Equal(t, []struct{ autoBan, cyber bool }{{false, true}}, actions2.notifies)
	require.Equal(t, []string{"create"}, repo2.snapshot())
}
