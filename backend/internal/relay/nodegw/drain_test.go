package nodegw

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDrainWatcherForcesCloseOnceAfterTheMaximumWait(t *testing.T) {
	var w drainWatcher
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	max := 30 * time.Minute

	require.False(t, w.observe(false, now, max))
	require.False(t, w.observe(true, now, max), "the clock starts when draining starts")
	require.False(t, w.observe(true, now.Add(29*time.Minute), max))
	require.True(t, w.observe(true, now.Add(30*time.Minute), max))
	require.False(t, w.observe(true, now.Add(31*time.Minute), max), "fires once")

	// 取消排空：计时清零，再排空要重新等。
	require.False(t, w.observe(false, now.Add(32*time.Minute), max))
	require.False(t, w.observe(true, now.Add(33*time.Minute), max))
	require.False(t, w.observe(true, now.Add(60*time.Minute), max))
	require.True(t, w.observe(true, now.Add(63*time.Minute), max))
}
