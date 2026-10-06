package nodegw

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/relay/nodestore"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newLogStore(t *testing.T) *nodestore.Store {
	t.Helper()
	store, err := nodestore.Open(t.TempDir(), nodestore.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func decodeRecords(t *testing.T, res *relayv1.LogQueryResult) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, raw := range res.GetRecords() {
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		out = append(out, m)
	}
	return out
}

// 程序日志写进本机存储：警告以上和审计（访问日志默认不写），脱敏，按字段建顶层索引；汇总条数计入心跳的统计。
func TestLogSinkStoresWarningsAndRedactsSecrets(t *testing.T) {
	store := newLogStore(t)
	stats := NewStats()
	sink := NewLogSink(store, stats)
	now := time.Now()
	sink.WriteLogEvent(&logger.LogEvent{Time: now, Level: "info", Component: "service.gateway", Message: "routine"})
	sink.WriteLogEvent(&logger.LogEvent{Time: now, Level: "info", Component: "http.access", Message: "GET /v1/models"})
	sink.WriteLogEvent(&logger.LogEvent{Time: now, Level: "warn", Component: "service.gateway", Message: "upstream slow Authorization: Bearer sk-secret-token-value",
		Fields: map[string]any{"request_id": "req-1", "user_id": int64(7), "api_key_id": 3, "account_id": float64(9), "platform": "openai", "model": "gpt-5",
			"api_key": "sk-should-not-appear", "extra": "kept"}})
	sink.WriteLogEvent(&logger.LogEvent{Time: now, Level: "info", Component: "audit.login", Message: "audit event"})
	sink.Close()

	res := NewLogQueryExecutor(store, nil).Execute(context.Background(), &relayv1.LogQuery{Kind: "app"})
	got := decodeRecords(t, res)
	require.Len(t, got, 2, "only the warning and the audit event are stored")
	require.Equal(t, "audit event", got[0]["message"], "newest first")
	warn := got[1]
	require.Equal(t, "warn", warn["level"])
	require.Equal(t, "service.gateway", warn["component"])
	require.Equal(t, "req-1", warn["request_id"])
	require.EqualValues(t, 7, warn["user_id"])
	require.EqualValues(t, 3, warn["api_key_id"])
	require.EqualValues(t, 9, warn["account_id"])
	require.NotContains(t, string(res.GetRecords()[1]), "sk-should-not-appear")
	require.NotContains(t, string(res.GetRecords()[1]), "sk-secret-token-value")
	require.Equal(t, "kept", warn["fields"].(map[string]any)["extra"])

	snap := stats.Snapshot(context.Background(), "")
	_ = snap
	require.EqualValues(t, 0, sink.Dropped())

	// 访问日志打开后也写。
	sink2 := NewLogSink(store, stats)
	sink2.SetPersistAccessLogs(true)
	sink2.WriteLogEvent(&logger.LogEvent{Time: now.Add(time.Second), Level: "info", Component: "http.access", Message: "GET /v1/models"})
	sink2.Close()
	res = NewLogQueryExecutor(store, nil).Execute(context.Background(), &relayv1.LogQuery{Kind: "app", Keyword: "GET /v1/models"})
	require.Len(t, res.GetRecords(), 1)
}

// 请求错误日志（OpsService.RecordError 的落点）写进本机存储：顶层字段可过滤，整条记录脱敏。
func TestNodeOpsRepositoryStoresErrorLogs(t *testing.T) {
	store := newLogStore(t)
	stats := NewStats()
	repo := NewNodeOpsRepository(store, stats)
	user, key, account := int64(7), int64(3), int64(9)
	created := time.Now()
	_, err := repo.InsertErrorLog(context.Background(), &service.OpsInsertErrorLogInput{
		RequestID: "req-9", UserID: &user, APIKeyID: &key, AccountID: &account, Platform: "anthropic", Model: "claude-sonnet-4-5",
		ErrorPhase: "upstream", ErrorType: "upstream_error", Severity: "P1", StatusCode: 502, ErrorMessage: "bad gateway Bearer sk-leak-token-abc",
		ErrorBody: `{"error":"x"}`, CreatedAt: created,
	})
	require.NoError(t, err)
	_, err = repo.BatchInsertErrorLogs(context.Background(), []*service.OpsInsertErrorLogInput{
		{RequestID: "req-10", ErrorPhase: "auth", ErrorType: "invalid_api_key", Severity: "P3", StatusCode: 401, CreatedAt: created.Add(time.Second)},
	})
	require.NoError(t, err)

	exec := NewLogQueryExecutor(store, nil)
	res := exec.Execute(context.Background(), &relayv1.LogQuery{Kind: "error", RequestId: "req-9"})
	got := decodeRecords(t, res)
	require.Len(t, got, 1)
	require.Equal(t, "upstream", got[0]["component"])
	require.Equal(t, "P1", got[0]["level"])
	require.EqualValues(t, 502, got[0]["status_code"])
	require.NotContains(t, string(res.GetRecords()[0]), "sk-leak-token-abc")
	require.NotNil(t, got[0]["entry"])

	res = exec.Execute(context.Background(), &relayv1.LogQuery{Kind: "error", UserId: 7, Platform: "ANTHROPIC", Model: "claude-sonnet-4-5", AccountId: 9, ApiKeyId: 3})
	require.Len(t, res.GetRecords(), 1)
	res = exec.Execute(context.Background(), &relayv1.LogQuery{Kind: "error"})
	require.Len(t, res.GetRecords(), 2)

	snap := stats.Snapshot(context.Background(), "")
	require.NotNil(t, snap)
}

// 查询有条数、大小、翻页游标、时间范围和关键字的限制（设计 12.3）；不认识的种类回错误。
func TestLogQueryExecutorLimitsAndFilters(t *testing.T) {
	store := newLogStore(t)
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 300; i++ {
		require.NoError(t, store.Append(logKindApp, logRecord{TS: base.Add(time.Duration(i) * time.Second).UnixMilli(), Level: "warn", Component: "c",
			Message: "line " + strings.Repeat("x", 10) + " #" + time.Duration(i).String()}))
	}
	exec := NewLogQueryExecutor(store, nil)

	res := exec.Execute(context.Background(), &relayv1.LogQuery{Kind: "app", Limit: 1000})
	require.Len(t, res.GetRecords(), maxLogQueryRecords, "capped at 200 whatever the caller asks")
	require.True(t, res.GetTruncated())
	first := decodeRecords(t, res)[0]
	require.EqualValues(t, base.Add(299*time.Second).UnixMilli(), first["ts"], "newest first")

	// 翻页：用上一页最后一条的时间做游标。
	last := decodeRecords(t, res)[len(res.GetRecords())-1]
	res = exec.Execute(context.Background(), &relayv1.LogQuery{Kind: "app", Limit: 200, BeforeUnixMs: int64(last["ts"].(float64))})
	require.Len(t, res.GetRecords(), 100)
	require.False(t, res.GetTruncated())

	// 大小上限：每条约 60 字节，限 200 字节只能放几条。
	res = exec.Execute(context.Background(), &relayv1.LogQuery{Kind: "app", MaxBytes: 200})
	require.NotEmpty(t, res.GetRecords())
	require.Less(t, len(res.GetRecords()), 5)
	require.True(t, res.GetTruncated())

	// 时间范围和关键字。
	res = exec.Execute(context.Background(), &relayv1.LogQuery{Kind: "app", FromUnixMs: base.Add(10 * time.Second).UnixMilli(), ToUnixMs: base.Add(12 * time.Second).UnixMilli()})
	require.Len(t, res.GetRecords(), 3)
	res = exec.Execute(context.Background(), &relayv1.LogQuery{Kind: "app", Keyword: "NO-SUCH-TEXT"})
	require.Empty(t, res.GetRecords())
	require.Empty(t, res.GetError())

	res = exec.Execute(context.Background(), &relayv1.LogQuery{Kind: "secrets"})
	require.NotEmpty(t, res.GetError(), "only the known kinds can be read")
	res = exec.Execute(context.Background(), &relayv1.LogQuery{Kind: "moderation"})
	require.NotEmpty(t, res.GetError(), "no moderation store wired")
}
