//go:build pgtest

// 真库回归：请求在途期间用户删除了 API Key，响应结束后的异步扣费仍必须成功。
//
// 线上事故（2026-10-05）：用户脚本「建 Key（带 100 额度）→ 发请求 → 约 2 秒后删 Key」，请求结束时
// Key 已软删除，Key 额度更新的 SQL 带 deleted_at IS NULL，0 行即返回 API_KEY_NOT_FOUND，
// 同一事务里的余额扣费随之回滚，用户白用。
//
// 运行（需要一个库名含 test 的空 Postgres，每次会清空重建）：
//
//	BILLING_PG_DSN="postgres://postgres@127.0.0.1:54399/billing_deleted_key_test?sslmode=disable" \
//	  go test -tags pgtest ./internal/repository/ -run TestUsageBillingDeletedKeyPG -count=1
package repository_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

var (
	billingDB  *sql.DB
	billingSeq int64
)

func TestMain(m *testing.M) {
	dsn := os.Getenv("BILLING_PG_DSN")
	if dsn == "" {
		fmt.Println("BILLING_PG_DSN not set; skipping")
		os.Exit(0)
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		panic(err)
	}
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		panic(err)
	}
	if !strings.Contains(name, "test") {
		panic(fmt.Sprintf("refusing to reset database %q: name must contain \"test\"", name))
	}
	if _, err := db.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		panic(err)
	}
	if err := repository.ApplyMigrations(context.Background(), db); err != nil {
		panic(fmt.Errorf("apply migrations: %w", err))
	}
	billingDB = db
	os.Exit(m.Run())
}

type billingFixture struct {
	userID int64
	keyID  int64
}

func newBillingFixture(t *testing.T, balance, quota float64) billingFixture {
	t.Helper()
	n := atomic.AddInt64(&billingSeq, 1)
	var f billingFixture
	require.NoError(t, billingDB.QueryRow(`
		INSERT INTO users (email, password_hash, role, status, balance, concurrency)
		VALUES ($1, 'x', 'user', 'active', $2, 5) RETURNING id`,
		fmt.Sprintf("bill-%d-%d@example.test", time.Now().UnixNano(), n), balance).Scan(&f.userID))
	require.NoError(t, billingDB.QueryRow(`
		INSERT INTO api_keys (user_id, key, name, status, quota)
		VALUES ($1, $2, 'rc-test', 'active', $3) RETURNING id`,
		f.userID, fmt.Sprintf("sk-test-%d-%d", time.Now().UnixNano(), n), quota).Scan(&f.keyID))
	return f
}

func (f billingFixture) softDeleteKey(t *testing.T) {
	t.Helper()
	// 和 apiKeyRepository.Delete 一致：墓碑 key + deleted_at
	_, err := billingDB.Exec(`UPDATE api_keys SET key = $1, deleted_at = NOW() WHERE id = $2`,
		fmt.Sprintf("__deleted__%d__%d", f.keyID, time.Now().UnixNano()), f.keyID)
	require.NoError(t, err)
}

func (f billingFixture) balance(t *testing.T) float64 {
	t.Helper()
	var b float64
	require.NoError(t, billingDB.QueryRow(`SELECT balance FROM users WHERE id = $1`, f.userID).Scan(&b))
	return b
}

func (f billingFixture) command(requestID string, cost float64) *service.UsageBillingCommand {
	return &service.UsageBillingCommand{
		RequestID:       requestID,
		APIKeyID:        f.keyID,
		UserID:          f.userID,
		Model:           "gpt-5.5",
		BalanceCost:     cost,
		APIKeyQuotaCost: cost, // 用户的 Key 都设了额度，这条 SQL 就是出事的地方
	}
}

func newBillingRepo() service.UsageBillingRepository {
	return repository.NewUsageBillingRepository(nil, billingDB)
}

func TestUsageBillingDeletedKeyPG_BalanceIsStillChargedWhenKeyWasDeletedMidRequest(t *testing.T) {
	f := newBillingFixture(t, 10, 100)
	f.softDeleteKey(t) // 请求在途期间用户把 Key 删了

	res, err := newBillingRepo().Apply(context.Background(), f.command("req-deleted-1", 0.4))
	require.NoError(t, err, "Key 已删不能让扣费事务失败")
	require.NotNil(t, res)
	require.True(t, res.Applied)
	require.InDelta(t, 9.6, f.balance(t), 1e-6, "余额必须照常扣掉")
}

func TestUsageBillingDeletedKeyPG_BalanceIsStillChargedWhenKeyRowIsGone(t *testing.T) {
	f := newBillingFixture(t, 10, 100)
	_, err := billingDB.Exec(`DELETE FROM api_keys WHERE id = $1`, f.keyID) // 极端：行被物理清理
	require.NoError(t, err)

	_, err = newBillingRepo().Apply(context.Background(), f.command("req-gone-1", 0.25))
	require.NoError(t, err)
	require.InDelta(t, 9.75, f.balance(t), 1e-6)
}

func TestUsageBillingDeletedKeyPG_RateLimitCountersDoNotBlockBillingEither(t *testing.T) {
	f := newBillingFixture(t, 10, 0)
	f.softDeleteKey(t)

	cmd := f.command("req-rate-1", 0.5)
	cmd.APIKeyQuotaCost = 0
	cmd.APIKeyRateLimitCost = 0.5
	_, err := newBillingRepo().Apply(context.Background(), cmd)
	require.NoError(t, err)
	require.InDelta(t, 9.5, f.balance(t), 1e-6)
}

func TestUsageBillingDeletedKeyPG_ReplayStillChargesOnce(t *testing.T) {
	f := newBillingFixture(t, 10, 100)
	f.softDeleteKey(t)
	repo := newBillingRepo()

	_, err := repo.Apply(context.Background(), f.command("req-replay-1", 1))
	require.NoError(t, err)
	res, err := repo.Apply(context.Background(), f.command("req-replay-1", 1)) // 重试
	require.NoError(t, err)
	require.False(t, res.Applied, "同一请求重放不能二次扣费")
	require.InDelta(t, 9, f.balance(t), 1e-6)
}

func TestUsageBillingDeletedKeyPG_LiveKeyStillCountsQuota(t *testing.T) {
	f := newBillingFixture(t, 10, 100)

	_, err := newBillingRepo().Apply(context.Background(), f.command("req-live-1", 0.3))
	require.NoError(t, err)
	require.InDelta(t, 9.7, f.balance(t), 1e-6)

	var used float64
	require.NoError(t, billingDB.QueryRow(`SELECT quota_used FROM api_keys WHERE id = $1`, f.keyID).Scan(&used))
	require.InDelta(t, 0.3, used, 1e-6, "正常的 Key 额度统计不能被改坏")
}
