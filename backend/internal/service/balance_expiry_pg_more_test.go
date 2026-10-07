//go:build pgtest

package service_test

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 与支付订单入账走同一条路：订单 PAID -> ExecuteBalanceFulfillment -> 兑换码 -> 余额 + 到期批次。
func (e *pgEnv) paidOrder(t *testing.T, userID int64, amount float64) (orderID int64, code string) {
	t.Helper()
	n := atomic.AddInt64(&seq, 1)
	code = fmt.Sprintf("PAY-%d-%d", time.Now().UnixNano(), n)
	err := e.db.QueryRow(`
		INSERT INTO payment_orders (user_id, amount, pay_amount, status, order_type, recharge_code, expires_at, out_trade_no, payment_type, paid_at)
		VALUES ($1, $2, $2, 'PAID', 'balance', $3, NOW() + INTERVAL '1 hour', $4, 'alipay', NOW())
		RETURNING id`, userID, amount, code, fmt.Sprintf("pay-%d-%d", time.Now().UnixNano(), n)).Scan(&orderID)
	require.NoError(t, err)
	return orderID, code
}

func (e *pgEnv) paymentService() *service.PaymentService {
	return service.NewPaymentService(e.client, nil, nil, e.redeem, nil, nil, e.users, nil, nil)
}

func TestBalanceExpiryPG_PaymentOrderFulfillmentMarksTheExpiryDate(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)
	uid := e.newUser(t, 4) // 旧余额
	before := time.Now()
	orderID, code := e.paidOrder(t, uid, 25)

	pay := e.paymentService()
	require.NoError(t, pay.ExecuteBalanceFulfillment(ctx, orderID))

	var status string
	require.NoError(t, e.db.QueryRow(`SELECT status FROM payment_orders WHERE id=$1`, orderID).Scan(&status))
	require.Equal(t, "COMPLETED", status)
	near(t, "balance", e.balance(t, uid), 29)

	var amount, remaining float64
	var credited, expires time.Time
	require.NoError(t, e.db.QueryRow(
		`SELECT amount, remaining, credited_at, expires_at FROM balance_expiry_lots WHERE source_ref=$1`, code).
		Scan(&amount, &remaining, &credited, &expires))
	near(t, "lot amount", amount, 25)
	near(t, "lot remaining", remaining, 25)
	require.WithinDuration(t, before, credited, time.Minute, "充值日 = 入账那一刻")
	require.WithinDuration(t, before.AddDate(0, 0, 30), expires, time.Minute, "到期 = 充值日 + 30 天")

	// 订单列表上能看到这笔充值的到期时间
	order, err := e.client.PaymentOrder.Get(ctx, orderID)
	require.NoError(t, err)
	marks := pay.BalanceExpiryMarks(ctx, nil)
	require.Empty(t, marks)
	marks = pay.BalanceExpiryMarks(ctx, []*dbent.PaymentOrder{order})
	require.Contains(t, marks, orderID)
	require.WithinDuration(t, expires, marks[orderID].ExpiresAt, time.Second)
	require.Equal(t, "active", marks[orderID].Status)

	// 幂等：同一订单再触发入账（重试、回调重放）不会重复加钱，也不会多记批次
	require.NoError(t, pay.ExecuteBalanceFulfillment(ctx, orderID))
	near(t, "balance after replay", e.balance(t, uid), 29)
	var n int
	require.NoError(t, e.db.QueryRow(`SELECT COUNT(*) FROM balance_expiry_lots WHERE source_ref=$1`, code).Scan(&n))
	require.Equal(t, 1, n)

	// 30 天后：没用完的清零，旧余额保留
	require.NoError(t, e.users.DeductBalance(ctx, uid, 5))
	e.makeDue(t, code)
	_, _, err = e.be.SweepExpired(ctx)
	require.NoError(t, err)
	near(t, "only the old 4 remains", e.balance(t, uid), 4)
	marks = pay.BalanceExpiryMarks(ctx, []*dbent.PaymentOrder{order})
	require.Equal(t, "expired", marks[orderID].Status)
	near(t, "expired amount shown on the order", marks[orderID].ExpiredAmount, 20)
}

func TestBalanceExpiryPG_PaymentFulfillmentWhileSwitchOffStaysPermanent(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, false, 30)
	uid := e.newUser(t, 0)
	orderID, code := e.paidOrder(t, uid, 40)
	require.NoError(t, e.paymentService().ExecuteBalanceFulfillment(ctx, orderID))
	near(t, "balance", e.balance(t, uid), 40)

	var n int
	require.NoError(t, e.db.QueryRow(`SELECT COUNT(*) FROM balance_expiry_lots WHERE source_ref=$1`, code).Scan(&n))
	require.Equal(t, 0, n)

	// 之后再打开开关，这笔充值也不会被回溯成会过期
	e.setEnabled(t, true, 30)
	_, _, err := e.be.SweepExpired(ctx)
	require.NoError(t, err)
	near(t, "still permanent", e.balance(t, uid), 40)
}

func TestBalanceExpiryPG_RefundDeductionIsSpentFromTheLotFirst(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)
	uid := e.newUser(t, 6)
	code := e.recharge(t, uid, 20)

	refunder, ok := e.users.(interface {
		DeductAvailableBalance(ctx context.Context, id int64, amount float64) (float64, error)
	})
	require.True(t, ok, "user repository must expose DeductAvailableBalance")
	deducted, err := refunder.DeductAvailableBalance(ctx, uid, 8)
	require.NoError(t, err)
	near(t, "deducted", deducted, 8)
	near(t, "balance", e.balance(t, uid), 18)

	e.makeDue(t, code)
	_, _, err = e.be.SweepExpired(ctx)
	require.NoError(t, err)
	near(t, "lot kept 12 -> cleared, old 6 stays", e.balance(t, uid), 6)
}

func TestBalanceExpiryPG_CentsDoNotLeaveFloatDust(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)
	uid := e.newUser(t, 0)
	code := e.recharge(t, uid, 33.33)
	for i := 0; i < 3; i++ {
		require.NoError(t, e.users.DeductBalance(ctx, uid, 11.11))
	}
	e.makeDue(t, code)
	lots, amount, err := e.be.SweepExpired(ctx)
	require.NoError(t, err)
	_ = lots
	require.Less(t, math.Abs(amount), 1e-6, "用完的批次不能被清出一粒浮点灰尘")

	var status string
	var expired float64
	require.NoError(t, e.db.QueryRow(`SELECT status, expired_amount FROM balance_expiry_lots WHERE source_ref=$1`, code).Scan(&status, &expired))
	require.Equal(t, "depleted", status)
	require.Less(t, math.Abs(expired), 1e-6)
	require.Less(t, math.Abs(e.balance(t, uid)), 1e-6)
}

// 守恒：每一块钱要么花掉了，要么被清掉了。并发扣费和多个清零任务同时跑，也不能多清、少清、重复清。
func TestBalanceExpiryPG_ConcurrentSpendingAndSweepsConserveMoney(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)
	uid := e.newUser(t, 10)
	code := e.recharge(t, uid, 20)
	e.makeDue(t, code)

	const spenders, perSpender = 8, 25 // 共 200 笔 × 0.10 = 20.00
	var wg sync.WaitGroup
	errCh := make(chan error, spenders*perSpender+16)
	for s := 0; s < spenders; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perSpender; i++ {
				if err := e.users.DeductBalance(ctx, uid, 0.1); err != nil {
					errCh <- err
				}
			}
		}()
	}
	for s := 0; s < 4; s++ { // 多实例同时清零
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 6; i++ {
				if _, _, err := e.be.SweepExpired(ctx); err != nil {
					errCh <- err
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	var expired float64
	var status string
	require.NoError(t, e.db.QueryRow(`SELECT expired_amount, status FROM balance_expiry_lots WHERE source_ref=$1`, code).Scan(&expired, &status))
	spent := float64(spenders*perSpender) * 0.1
	// 初始 10 + 20，花掉 spent，清掉 expired
	near(t, "conservation: balance = 30 - spent - expired", e.balance(t, uid), 30-spent-expired)
	require.Contains(t, []string{"expired", "depleted"}, status)
	require.GreaterOrEqual(t, expired, -1e-9)
	require.LessOrEqual(t, expired, 20+1e-9)
}

// 随机操作序列对照一个朴素模型：充值、消费、赠送加款、退款扣减、按先后顺序到期清零。
func TestBalanceExpiryPG_RandomOperationsMatchTheModel(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)
	refunder := e.users.(interface {
		DeductAvailableBalance(ctx context.Context, id int64, amount float64) (float64, error)
	})

	type lot struct {
		code      string
		remaining float64
	}
	cents := func(r *rand.Rand, lo, hi int) float64 { return float64(lo+r.Intn(hi-lo+1)) / 100 }

	for seed := int64(1); seed <= 25; seed++ {
		r := rand.New(rand.NewSource(seed))
		start := cents(r, 0, 5000)
		uid := e.newUser(t, start)
		permanent := start
		var lots []lot // 先到期的在前（创建顺序）

		balance := func() float64 {
			b := permanent
			for _, l := range lots {
				b += l.remaining
			}
			return b
		}
		spend := func(amount float64) {
			for i := range lots {
				take := math.Min(lots[i].remaining, amount)
				lots[i].remaining -= take
				amount -= take
			}
			permanent -= amount
		}
		check := func(step string) {
			t.Helper()
			near(t, fmt.Sprintf("seed %d %s: balance", seed, step), e.balance(t, uid), balance())
			view, err := e.be.GetUserView(ctx, uid)
			require.NoError(t, err)
			var expiring float64
			for _, l := range lots {
				expiring += l.remaining
			}
			near(t, fmt.Sprintf("seed %d %s: expiring", seed, step), view.ExpiringBalance, expiring)
			near(t, fmt.Sprintf("seed %d %s: permanent", seed, step), view.PermanentBalance, permanent)
		}

		for op := 0; op < 40; op++ {
			step := fmt.Sprintf("op %d", op)
			switch r.Intn(6) {
			case 0, 1: // 充值
				a := cents(r, 100, 3000)
				code := e.recharge(t, uid, a)
				lots = append(lots, lot{code: code, remaining: a})
				step += " recharge"
			case 2: // 消费
				if b := balance(); b > 0.05 {
					a := math.Min(cents(r, 1, 1500), b)
					require.NoError(t, e.users.DeductBalance(ctx, uid, a))
					spend(a)
				}
				step += " spend"
			case 3: // 退款扣减
				if b := balance(); b > 0.05 {
					a := math.Min(cents(r, 1, 800), b)
					got, err := refunder.DeductAvailableBalance(ctx, uid, a)
					require.NoError(t, err)
					near(t, "refund deducted", got, a)
					spend(a)
				}
				step += " refund"
			case 4: // 管理员加款 / 赠送：永久
				a := cents(r, 50, 1500)
				_, err := e.users.AdjustBalance(ctx, uid, a)
				require.NoError(t, err)
				permanent += a
				step += " gift"
			case 5: // 最早的一批到期
				if len(lots) > 0 {
					e.makeDue(t, lots[0].code)
					_, _, err := e.be.SweepExpired(ctx)
					require.NoError(t, err)
					lots = lots[1:] // 没用完的部分被清掉，余额里不再有它
					step += " expire"
				}
			}
			check(step)
		}
	}
}

// 多个实例同时清零同一批到期用户：每个批次只能被清一次，否则用户的钱会被多扣。
func TestBalanceExpiryPG_ManySweepersRacingOnTheSameUsersClearEachLotOnce(t *testing.T) {
	e := pgShared
	ctx := context.Background()
	e.setEnabled(t, true, 30)

	const users, sweepers = 40, 8
	type account struct {
		id   int64
		code string
	}
	accounts := make([]account, 0, users)
	for i := 0; i < users; i++ {
		uid := e.newUser(t, 0)
		code := e.recharge(t, uid, 10)
		e.makeDue(t, code)
		accounts = append(accounts, account{id: uid, code: code})
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errCh := make(chan error, sweepers)
	var clearedLots int64
	for s := 0; s < sweepers; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			lots, _, err := e.be.SweepExpired(ctx)
			if err != nil {
				errCh <- err
			}
			atomic.AddInt64(&clearedLots, int64(lots))
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	require.EqualValues(t, users, clearedLots, "每个批次恰好被清一次，不能多也不能少")
	for _, a := range accounts {
		near(t, "balance cleared exactly once", e.balance(t, a.id), 0)
		var expired float64
		require.NoError(t, e.db.QueryRow(`SELECT expired_amount FROM balance_expiry_lots WHERE source_ref=$1`, a.code).Scan(&expired))
		near(t, "expired amount", expired, 10)
	}
}
