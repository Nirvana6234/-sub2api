package master

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 批次序号：记下每台节点见过的最大序号；重发、倒退不改它。
func TestBillingTracksBatchSequences(t *testing.T) {
	b := NewBilling(nil)
	b.checkSequence(1, 1)
	b.checkSequence(1, 2)
	b.checkSequence(1, 5) // 跳号：报警
	b.checkSequence(1, 3) // 重发旧批次
	b.checkSequence(2, 7)
	require.Equal(t, uint64(5), b.lastSeqs[1])
	require.Equal(t, uint64(7), b.lastSeqs[2])
}
