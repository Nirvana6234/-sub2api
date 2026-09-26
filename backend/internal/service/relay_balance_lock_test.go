//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type reclaimerStub struct {
	calls     int
	locked    float64
	releaseBy time.Time
	err       error
	onReclaim func()
}

func (r *reclaimerStub) ReclaimBalance(context.Context, int64) (float64, time.Time, error) {
	r.calls++
	if r.onReclaim != nil {
		r.onReclaim()
	}
	return r.locked, r.releaseBy, r.err
}

func TestWithRelayBalanceReclaim(t *testing.T) {
	t.Cleanup(func() { SetRelayBalanceReclaimer(nil) })
	ctx := context.Background()
	lockedOnce := func() func() error {
		n := 0
		return func() error {
			n++
			if n == 1 {
				return ErrBalanceLockedOnRelay
			}
			return nil
		}
	}

	// 没开主从分流（没挂收回）时直接 409。
	err := withRelayBalanceReclaim(ctx, 1, func() error { return ErrBalanceLockedOnRelay })
	require.Equal(t, http.StatusConflict, infraerrors.Code(err))

	// 收回后够了：第二次成功。
	stub := &reclaimerStub{}
	SetRelayBalanceReclaimer(stub)
	require.NoError(t, withRelayBalanceReclaim(ctx, 1, lockedOnce()))
	require.Equal(t, 1, stub.calls)

	// 收回后仍锁在离线节点上：409，带锁着多少、最晚什么时候放回。
	by := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	stub = &reclaimerStub{locked: 3.5, releaseBy: by}
	SetRelayBalanceReclaimer(stub)
	err = withRelayBalanceReclaim(ctx, 1, func() error { return ErrBalanceLockedOnRelay })
	require.Equal(t, http.StatusConflict, infraerrors.Code(err))
	require.Equal(t, "RELAY_BALANCE_LOCKED", infraerrors.Reason(err))
	require.True(t, errors.Is(err, ErrBalanceLockedOnRelay))
	_, st := infraerrors.ToHTTP(err)
	require.Equal(t, "3.5", st.Metadata["locked_amount"])
	require.Equal(t, "2026-09-26T12:00:00Z", st.Metadata["release_by"])

	// 其他错误原样返回，不收回。
	stub = &reclaimerStub{}
	SetRelayBalanceReclaimer(stub)
	boom := errors.New("boom")
	require.ErrorIs(t, withRelayBalanceReclaim(ctx, 1, func() error { return boom }), boom)
	require.Zero(t, stub.calls)
}

// 退款：余额够、只是锁在从节点上时，先收回再算可退的部分。
func TestPrepDeductReclaimsRelayLockedBalanceFirst(t *testing.T) {
	t.Cleanup(func() { SetRelayBalanceReclaimer(nil) })
	repo := &mockUserRepo{getByIDUser: &User{Balance: 100, RelayReservedBalance: 30}}
	stub := &reclaimerStub{onReclaim: func() { repo.getByIDUser = &User{Balance: 100} }}
	SetRelayBalanceReclaimer(stub)
	svc := &PaymentService{userRepo: repo}
	plan := &RefundPlan{RefundAmount: 100}
	result := svc.prepDeduct(context.Background(), &dbent.PaymentOrder{UserID: 1, OrderType: payment.OrderTypeBalance}, plan, false)
	require.Nil(t, result)
	require.Equal(t, 1, stub.calls)
	require.Equal(t, 100.0, plan.BalanceToDeduct, "everything came back from the nodes")
}
