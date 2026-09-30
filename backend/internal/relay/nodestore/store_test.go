package nodestore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type rec struct {
	N int `json:"n"`
}

func scanAll(t *testing.T, s *Store, kind string, from, to time.Time) []int {
	t.Helper()
	var out []int
	require.NoError(t, s.Scan(kind, from, to, func(line []byte) bool {
		var r rec
		require.NoError(t, json.Unmarshal(line, &r))
		out = append(out, r.N)
		return true
	}))
	return out
}

func TestStoreAppendScanAndRetention(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	s, err := Open(dir, Options{Now: func() time.Time { return now }})
	require.NoError(t, err)
	for i := 1; i <= 3; i++ {
		require.NoError(t, s.Append("moderation-hit", rec{N: i}))
	}
	now = now.Add(24 * time.Hour)
	require.NoError(t, s.Append("moderation-hit", rec{N: 4}))
	require.Equal(t, []int{4, 3, 2, 1}, scanAll(t, s, "moderation-hit", time.Time{}, time.Time{}), "newest first")
	require.Equal(t, []int{4}, scanAll(t, s, "moderation-hit", now, time.Time{}))
	require.Error(t, s.Append("../escape", rec{}))

	// 重启后还在。
	require.NoError(t, s.Close())
	s, err = Open(dir, Options{Now: func() time.Time { return now }})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	require.Equal(t, []int{4, 3, 2, 1}, scanAll(t, s, "moderation-hit", time.Time{}, time.Time{}))

	// 按保留期整天删除。
	deleted, err := s.DeleteBefore("moderation-hit", now)
	require.NoError(t, err)
	require.Equal(t, int64(3), deleted)
	require.Equal(t, []int{4}, scanAll(t, s, "moderation-hit", time.Time{}, time.Time{}))
	require.NoError(t, s.Append("moderation-hit", rec{N: 5}), "today's segment can still be written after a cleanup")
}

func TestStoreSizeLimitDropsTheOldest(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	s, err := Open(dir, Options{MaxBytes: 40, Now: func() time.Time { return now }})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	for day := 0; day < 5; day++ {
		require.NoError(t, s.Append("logs", rec{N: day}))
		require.NoError(t, s.Append("logs", rec{N: day}))
		now = now.Add(24 * time.Hour)
	}
	got := scanAll(t, s, "logs", time.Time{}, time.Time{})
	require.NotEmpty(t, got)
	require.Equal(t, 4, got[0], "the newest day is kept")
	require.NotContains(t, got, 0, "the oldest days were dropped")
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	require.LessOrEqual(t, total, int64(40))
}
