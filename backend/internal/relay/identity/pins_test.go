package identity_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/relay/identity"
	"github.com/stretchr/testify/require"
)

func fp(c string) string { return strings.Repeat(c, 64) }

func TestRootPinsReplaceStoredListButKeepConfigured(t *testing.T) {
	dir := t.TempDir()
	_, err := identity.LoadRootPins(dir, []string{"not-a-fingerprint"})
	require.Error(t, err, "at least one valid configured fingerprint is required")

	pins, err := identity.LoadRootPins(dir, []string{strings.ToUpper(fp("a"))})
	require.NoError(t, err)
	require.Equal(t, []string{fp("a")}, pins.Pinned())

	changed, err := pins.Update([]string{fp("a"), fp("b")})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, []string{fp("a"), fp("b")}, pins.Pinned())
	changed, err = pins.Update([]string{fp("b"), fp("a")})
	require.NoError(t, err)
	require.False(t, changed, "same set, no rewrite")

	// 旧根停用：存下来的列表整份替换；本机配置的仍然信任。
	_, err = pins.Update([]string{fp("b"), fp("c")})
	require.NoError(t, err)
	require.Equal(t, []string{fp("a"), fp("b"), fp("c")}, pins.Pinned())
	reloaded, err := identity.LoadRootPins(dir, []string{fp("d")})
	require.NoError(t, err)
	require.Equal(t, []string{fp("b"), fp("c"), fp("d")}, reloaded.Pinned(), "the stored list survives a restart")

	// 空列表、无效值、过长的列表不接收。
	for _, bad := range [][]string{nil, {"zz"}, {fp("e"), fp("f"), fp("0"), fp("1"), fp("2"), fp("3"), fp("4"), fp("5"), fp("6")}} {
		changed, err = pins.Update(bad)
		require.NoError(t, err)
		require.False(t, changed)
	}
	require.Equal(t, []string{fp("a"), fp("b"), fp("c")}, pins.Pinned())
}

func TestCorruptRootPinsFileFallsBackToConfigured(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "root_pins.json"), []byte("{not json"), 0o600))
	pins, err := identity.LoadRootPins(dir, []string{fp("a")})
	require.NoError(t, err)
	require.Equal(t, []string{fp("a")}, pins.Pinned())
}
