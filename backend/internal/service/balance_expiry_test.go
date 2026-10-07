package service

import (
	"math"
	"testing"
)

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func TestReconcileBalanceLots(t *testing.T) {
	t.Run("nothing spent changes nothing", func(t *testing.T) {
		p, r := reconcileBalanceLots(30, 10, []float64{20})
		approx(t, "permanent", p, 10)
		approx(t, "lot", r[0], 20)
	})

	t.Run("spending eats the earliest lot first and spares the permanent part", func(t *testing.T) {
		// 永久 10，两批各 20，花了 25：先到期的批次扣完 20，第二批扣 5，永久部分不动。
		p, r := reconcileBalanceLots(25, 10, []float64{20, 20})
		approx(t, "permanent", p, 10)
		approx(t, "lot1", r[0], 0)
		approx(t, "lot2", r[1], 15)
	})

	t.Run("permanent part is only touched after every lot is used up", func(t *testing.T) {
		p, r := reconcileBalanceLots(6, 10, []float64{20})
		approx(t, "permanent", p, 6)
		approx(t, "lot", r[0], 0)
	})

	t.Run("unexplained increase is permanent", func(t *testing.T) {
		// 管理员加款等没有批次认领的增量归永久部分，不能被后面的到期清零带走。
		p, r := reconcileBalanceLots(60, 10, []float64{20})
		approx(t, "permanent", p, 40)
		approx(t, "lot", r[0], 20)
	})

	t.Run("overdraft clears everything", func(t *testing.T) {
		p, r := reconcileBalanceLots(-3, 10, []float64{20, 5})
		approx(t, "permanent", p, 0)
		approx(t, "lot1", r[0], 0)
		approx(t, "lot2", r[1], 0)
	})

	t.Run("no lots keeps permanent equal to balance", func(t *testing.T) {
		p, r := reconcileBalanceLots(7, 10, nil)
		approx(t, "permanent", p, 7)
		if len(r) != 0 {
			t.Fatalf("remaining = %v, want empty", r)
		}
	})

	t.Run("input slice is not modified", func(t *testing.T) {
		in := []float64{20}
		reconcileBalanceLots(5, 0, in)
		approx(t, "input", in[0], 20)
	})
}
