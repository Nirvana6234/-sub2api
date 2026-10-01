//go:build pgtest

// 充值余额有效期的真库测试。需要一个空的 Postgres，通过环境变量传入连接串：
//
//	BALANCE_EXPIRY_PG_DSN="postgres://postgres@127.0.0.1:54399/balance_expiry_test?sslmode=disable" \
//	  go test -tags pgtest ./internal/service/ -run TestBalanceExpiryPG -count=1
//
// 迁移由项目自己的 ApplyMigrations 建好；测试用的是真实的 user / redeem / setting 仓储，
// 到期用「把批次的 expires_at 改到过去」来模拟时间流逝。
package service_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type pgEnv struct {
	db     *sql.DB
	client *dbent.Client
	users  service.UserRepository
	be     *service.BalanceExpiryService
	redeem *service.RedeemService
}

var (
	pgShared *pgEnv
	seq      int64

	// 迁移刚跑完时的开关默认值：在任何用例改动设置之前读出来，避免用例执行顺序影响断言。
	seededEnabled, seededDays string
)

func TestMain(m *testing.M) {
	dsn := os.Getenv("BALANCE_EXPIRY_PG_DSN")
	if dsn == "" {
		fmt.Println("BALANCE_EXPIRY_PG_DSN not set; skipping")
		os.Exit(0)
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		panic(err)
	}
	// 每次从空库开始，保证「迁移后的默认值」之类的断言不受上一轮残留影响。
	// 只允许在库名带 test 的库上执行，避免误清真实数据。
	var dbName string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&dbName); err != nil {
		panic(err)
	}
	if !strings.Contains(dbName, "test") {
		panic(fmt.Sprintf("refusing to reset database %q: name must contain \"test\"", dbName))
	}
	if _, err := db.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		panic(err)
	}
	if err := repository.ApplyMigrations(context.Background(), db); err != nil {
		panic(fmt.Errorf("apply migrations: %w", err))
	}
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='balance_expiry_enabled'`).Scan(&seededEnabled); err != nil {
		panic(err)
	}
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='balance_expiry_days'`).Scan(&seededDays); err != nil {
		panic(err)
	}
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	users := repository.NewUserRepository(client, db)
	be := service.NewBalanceExpiryService(client, repository.NewSettingRepository(client), nil, nil)
	redeem := service.NewRedeemService(repository.NewRedeemCodeRepository(client), users, nil, nil, nil, client, nil, nil)
	redeem.SetBalanceExpiry(be)
	pgShared = &pgEnv{db: db, client: client, users: users, be: be, redeem: redeem}
	os.Exit(m.Run())
}

func (e *pgEnv) newUser(t *testing.T, balance float64) int64 {
	t.Helper()
	var id int64
	err := e.db.QueryRow(`INSERT INTO users (email, password_hash, role, status, balance, concurrency)
		VALUES ($1, 'x', 'user', 'active', $2, 5) RETURNING id`,
		fmt.Sprintf("be-%d-%d@example.test", time.Now().UnixNano(), atomic.AddInt64(&seq, 1)), balance).Scan(&id)
	require.NoError(t, err)
	return id
}

func (e *pgEnv) balance(t *testing.T, id int64) float64 {
	t.Helper()
	var b float64
	require.NoError(t, e.db.QueryRow(`SELECT balance FROM users WHERE id = $1`, id).Scan(&b))
	return b
}

// recharge 走真实的兑换流程：建一个余额码并兑换，和支付订单入账走的是同一条路。
func (e *pgEnv) recharge(t *testing.T, userID int64, amount float64) string {
	t.Helper()
	code := fmt.Sprintf("BE-%d-%d", time.Now().UnixNano(), atomic.AddInt64(&seq, 1))
	ctx := context.Background()
	require.NoError(t, e.redeem.CreateCode(ctx, &service.RedeemCode{Code: code, Type: service.RedeemTypeBalance, Value: amount, Status: service.StatusUnused}))
	_, err := e.redeem.RedeemForAdminFulfillment(ctx, userID, code)
	require.NoError(t, err)
	return code
}

func (e *pgEnv) makeDue(t *testing.T, code string) {
	t.Helper()
	_, err := e.db.Exec(`UPDATE balance_expiry_lots SET expires_at = NOW() - INTERVAL '1 minute' WHERE source_ref = $1`, code)
	require.NoError(t, err)
}

func (e *pgEnv) setEnabled(t *testing.T, on bool, days int) {
	t.Helper()
	require.NoError(t, e.be.SetConfig(context.Background(), service.BalanceExpiryConfig{Enabled: on, Days: days}))
}

func near(t *testing.T, name string, got, want float64) {
	t.Helper()
	require.InDelta(t, want, got, 1e-6, name)
}

func TestBalanceExpiryPG_Defaults(t *testing.T) {
	e := pgShared
	require.Equal(t, "false", seededEnabled, "迁移后默认关闭")
	require.Equal(t, "30", seededDays)

	require.Error(t, e.be.SetConfig(context.Background(), service.BalanceExpiryConfig{Enabled: true, Days: 0}))
	require.Error(t, e.be.SetConfig(context.Background(), service.BalanceExpiryConfig{Enabled: true, Days: 4000}))
}

func TestBalanceExpiryPG_DisabledMeansPermanent(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, false, 30)
	uid := e.newUser(t, 3)
	e.recharge(t, uid, 20)
	near(t, "balance", e.balance(t, uid), 23)

	var n int
	require.NoError(t, e.db.QueryRow(`SELECT COUNT(*) FROM balance_expiry_lots WHERE user_id=$1`, uid).Scan(&n))
	require.Equal(t, 0, n, "开关关闭时不记批次")

	view, err := e.be.GetUserView(ctx, uid)
	require.NoError(t, err)
	require.False(t, view.Enabled)
	near(t, "permanent", view.PermanentBalance, 23)
	near(t, "expiring", view.ExpiringBalance, 0)
}

func TestBalanceExpiryPG_Lifecycle(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)
	uid := e.newUser(t, 10) // 旧余额：没有批次，永久

	before := time.Now()
	code := e.recharge(t, uid, 20)
	near(t, "balance after recharge", e.balance(t, uid), 30)

	view, err := e.be.GetUserView(ctx, uid)
	require.NoError(t, err)
	require.True(t, view.Enabled)
	require.Equal(t, 30, view.Days)
	near(t, "permanent", view.PermanentBalance, 10)
	near(t, "expiring", view.ExpiringBalance, 20)
	require.Len(t, view.Lots, 1)
	require.WithinDuration(t, before.AddDate(0, 0, 30), view.Lots[0].ExpiresAt, time.Minute, "从充值当刻起 30 天")
	require.WithinDuration(t, before, view.Lots[0].CreditedAt, time.Minute)
	require.NotNil(t, view.NextExpiresAt)

	// 花 5：先花限时余额，永久部分不动
	require.NoError(t, e.users.DeductBalance(ctx, uid, 5))
	view, err = e.be.GetUserView(ctx, uid)
	require.NoError(t, err)
	near(t, "permanent", view.PermanentBalance, 10)
	near(t, "expiring", view.ExpiringBalance, 15)

	// 还没到期时清零不动这个用户
	_, _, err = e.be.SweepExpired(ctx)
	require.NoError(t, err)
	near(t, "balance untouched before due", e.balance(t, uid), 25)

	// 到期：只清掉限时余额里没用完的 15，旧余额 10 保留
	e.makeDue(t, code)
	lots, amount, err := e.be.SweepExpired(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, lots, 1)
	require.GreaterOrEqual(t, amount, 15.0-1e-6)
	near(t, "balance after expiry", e.balance(t, uid), 10)

	view, err = e.be.GetUserView(ctx, uid)
	require.NoError(t, err)
	near(t, "permanent", view.PermanentBalance, 10)
	near(t, "expiring", view.ExpiringBalance, 0)
	require.Empty(t, view.Lots)
	require.Len(t, view.Expired, 1)
	near(t, "expired amount", view.Expired[0].ExpiredAmount, 15)

	// 清零是一次性的
	_, _, err = e.be.SweepExpired(ctx)
	require.NoError(t, err)
	near(t, "balance stable", e.balance(t, uid), 10)

	// 订单上要能查到这笔充值的到期信息（含已到期清零的数额）
	marks, err := e.be.LotsBySourceRefs(ctx, []string{code, "no-such-code"})
	require.NoError(t, err)
	require.Len(t, marks, 1)
	require.Equal(t, "expired", marks[code].Status)
	near(t, "mark expired", marks[code].ExpiredAmount, 15)
}

func TestBalanceExpiryPG_SpendingPastTheLotEatsPermanentLast(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)
	uid := e.newUser(t, 10)
	code := e.recharge(t, uid, 20)
	require.NoError(t, e.users.DeductBalance(ctx, uid, 25)) // 限时 20 用完，再动永久 5
	near(t, "balance", e.balance(t, uid), 5)

	e.makeDue(t, code)
	_, _, err := e.be.SweepExpired(ctx)
	require.NoError(t, err)
	near(t, "nothing to clear", e.balance(t, uid), 5)

	var status string
	require.NoError(t, e.db.QueryRow(`SELECT status FROM balance_expiry_lots WHERE source_ref=$1`, code).Scan(&status))
	require.Equal(t, "depleted", status)
}

func TestBalanceExpiryPG_CreditAfterSpendingIsNotWipedByExpiry(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)
	uid := e.newUser(t, 0)
	code := e.recharge(t, uid, 20)
	require.NoError(t, e.users.DeductBalance(ctx, uid, 8)) // 限时剩 12

	// 管理员加款 30：不是充值，永久。若不先对账，净变化会让批次看起来还有 20，到期时多清掉 8。
	_, err := e.users.AdjustBalance(ctx, uid, 30)
	require.NoError(t, err)
	near(t, "balance", e.balance(t, uid), 42)

	e.makeDue(t, code)
	_, _, err = e.be.SweepExpired(ctx)
	require.NoError(t, err)
	near(t, "gift survives, only the unused 12 is cleared", e.balance(t, uid), 30)
}

func TestBalanceExpiryPG_UpdateBalanceCreditPathAlsoSyncs(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)
	uid := e.newUser(t, 0)
	code := e.recharge(t, uid, 20)
	require.NoError(t, e.users.DeductBalance(ctx, uid, 8))
	require.NoError(t, e.users.UpdateBalance(ctx, uid, 30)) // 例如优惠码赠送

	e.makeDue(t, code)
	_, _, err := e.be.SweepExpired(ctx)
	require.NoError(t, err)
	near(t, "balance", e.balance(t, uid), 30)
}

func TestBalanceExpiryPG_EarliestLotIsSpentFirst(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)
	uid := e.newUser(t, 0)
	c1 := e.recharge(t, uid, 10)
	e.recharge(t, uid, 10)
	require.NoError(t, e.users.DeductBalance(ctx, uid, 4)) // 先到期的那批剩 6

	view, err := e.be.GetUserView(ctx, uid)
	require.NoError(t, err)
	require.Len(t, view.Lots, 2)
	near(t, "lot1", view.Lots[0].Remaining, 6)
	near(t, "lot2", view.Lots[1].Remaining, 10)

	e.makeDue(t, c1) // 第一批先到期
	_, _, err = e.be.SweepExpired(ctx)
	require.NoError(t, err)
	near(t, "only lot1's unused 6 cleared", e.balance(t, uid), 10)

	view, err = e.be.GetUserView(ctx, uid)
	require.NoError(t, err)
	require.Len(t, view.Lots, 1)
	near(t, "lot2 intact", view.Lots[0].Remaining, 10)
}

func TestBalanceExpiryPG_OverdraftThenRecharge(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)
	uid := e.newUser(t, 0)
	e.recharge(t, uid, 10)
	require.NoError(t, e.users.DeductBalance(ctx, uid, 15)) // 允许透支
	near(t, "overdraft", e.balance(t, uid), -5)

	e.recharge(t, uid, 10) // 先还 5 的账，实际可用 5
	near(t, "balance", e.balance(t, uid), 5)
	view, err := e.be.GetUserView(ctx, uid)
	require.NoError(t, err)
	near(t, "expiring is what is actually left", view.ExpiringBalance, 5)
	near(t, "permanent", view.PermanentBalance, 0)
}

func TestBalanceExpiryPG_RecordRechargeIsIdempotent(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)
	uid := e.newUser(t, 0)
	code := e.recharge(t, uid, 10)
	exp, err := e.be.RecordRecharge(ctx, uid, 10, code) // 同一单据重放
	require.NoError(t, err)
	require.Nil(t, exp)
	var n int
	require.NoError(t, e.db.QueryRow(`SELECT COUNT(*) FROM balance_expiry_lots WHERE source_ref=$1`, code).Scan(&n))
	require.Equal(t, 1, n)
}

func TestBalanceExpiryPG_TurningOffPausesClearing(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)
	uid := e.newUser(t, 0)
	code := e.recharge(t, uid, 10)
	e.makeDue(t, code)

	e.setEnabled(t, false, 30)
	_, _, err := e.be.SweepExpired(ctx)
	require.NoError(t, err)
	near(t, "paused", e.balance(t, uid), 10)

	e.setEnabled(t, true, 30) // 重新打开后下一轮清零
	_, _, err = e.be.SweepExpired(ctx)
	require.NoError(t, err)
	near(t, "cleared after re-enable", e.balance(t, uid), 0)
}

func TestBalanceExpiryPG_ChangingDaysOnlyAffectsLaterRecharges(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)
	uid := e.newUser(t, 0)
	e.recharge(t, uid, 10)
	e.setEnabled(t, true, 7)
	e.recharge(t, uid, 10)

	view, err := e.be.GetUserView(ctx, uid)
	require.NoError(t, err)
	require.Len(t, view.Lots, 2)
	require.Equal(t, 7, view.Days)
	// 7 天的那批更早到期，排在前面，两批相差约 23 天
	gap := view.Lots[1].ExpiresAt.Sub(view.Lots[0].ExpiresAt).Hours() / 24
	require.InDelta(t, 23, gap, 0.1)
}

func TestBalanceExpiryPG_NegativeRedeemCodeJustConsumes(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)
	uid := e.newUser(t, 5)
	e.recharge(t, uid, 10)
	// 扣款类兑换码（负数）不产生批次，只是消耗
	code := fmt.Sprintf("BE-NEG-%d", time.Now().UnixNano())
	require.NoError(t, e.redeem.CreateCode(ctx, &service.RedeemCode{Code: code, Type: service.RedeemTypeBalance, Value: -3, Status: service.StatusUnused}))
	_, err := e.redeem.RedeemForAdminFulfillment(ctx, uid, code)
	require.NoError(t, err)
	near(t, "balance", e.balance(t, uid), 12)
	view, err := e.be.GetUserView(ctx, uid)
	require.NoError(t, err)
	near(t, "expiring", view.ExpiringBalance, 7)
	near(t, "permanent", view.PermanentBalance, 5)
}

func TestBalanceExpiryPG_UserWithoutLots(t *testing.T) {
	e := pgShared
	uid := e.newUser(t, 12.34)
	view, err := e.be.GetUserView(context.Background(), uid)
	require.NoError(t, err)
	near(t, "permanent == balance", view.PermanentBalance, 12.34)
	require.Empty(t, view.Lots)
	require.Nil(t, view.NextExpiresAt)

	_, err = e.be.GetUserView(context.Background(), 987654321)
	require.ErrorIs(t, err, service.ErrUserNotFound)
}

// 回归：非充值的加款之后用户继续消费，「加 5、花 4」不能被净成「加 1」。
// 否则到期时把花掉的 4 当成没花，多清了用户的钱（端到端测试里发现的）。
func TestBalanceExpiryPG_SpendingAfterAFreeCreditIsStillChargedToTheLot(t *testing.T) {
	credits := map[string]func(e *pgEnv, ctx context.Context, uid int64) error{
		"UpdateBalance": func(e *pgEnv, ctx context.Context, uid int64) error { return e.users.UpdateBalance(ctx, uid, 5) },
		"AdjustBalance": func(e *pgEnv, ctx context.Context, uid int64) error {
			_, err := e.users.AdjustBalance(ctx, uid, 5)
			return err
		},
		"SetBalance": func(e *pgEnv, ctx context.Context, uid int64) error {
			// 把余额从 12 调到 17
			_, err := e.users.SetBalance(ctx, uid, 17)
			return err
		},
	}
	for name, credit := range credits {
		t.Run(name, func(t *testing.T) {
			e := pgShared
			ctx := context.Background()
			e.setEnabled(t, true, 30)
			uid := e.newUser(t, 0)
			code := e.recharge(t, uid, 12)
			require.NoError(t, credit(e, ctx, uid)) // 永久 5
			near(t, "balance after credit", e.balance(t, uid), 17)
			require.NoError(t, e.users.DeductBalance(ctx, uid, 4)) // 先花限时余额：剩 8

			view, err := e.be.GetUserView(ctx, uid)
			require.NoError(t, err)
			near(t, "expiring", view.ExpiringBalance, 8)
			near(t, "permanent", view.PermanentBalance, 5)

			e.makeDue(t, code)
			_, _, err = e.be.SweepExpired(ctx)
			require.NoError(t, err)
			near(t, "only the unused 8 is cleared, the free 5 stays", e.balance(t, uid), 5)
		})
	}
}

func TestBalanceExpiryPG_AdminRechargeThenSpendThenExpire(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)
	uid := e.newUser(t, 5) // 旧永久余额 5
	c1 := e.recharge(t, uid, 12)
	require.NoError(t, e.users.DeductBalance(ctx, uid, 4)) // 限时剩 8
	_, err := e.users.AdjustBalance(ctx, uid, 6)           // 管理员加 6，永久
	require.NoError(t, err)
	require.NoError(t, e.users.DeductBalance(ctx, uid, 3)) // 继续花：仍先花限时，剩 5
	view, err := e.be.GetUserView(ctx, uid)
	require.NoError(t, err)
	near(t, "expiring", view.ExpiringBalance, 5)
	near(t, "permanent", view.PermanentBalance, 11)

	e.makeDue(t, c1)
	_, _, err = e.be.SweepExpired(ctx)
	require.NoError(t, err)
	near(t, "balance", e.balance(t, uid), 11)
}
