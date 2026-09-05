package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExpectedLocalControlTokenHashFromEnvironment(t *testing.T) {
	t.Setenv("LOCAL_CONTROL_TOKEN", "environment-local-control-token")
	t.Setenv("DATA_DIR", "")

	actual, ok := expectedLocalControlTokenHash()

	require.True(t, ok)
	require.Equal(t, sha256.Sum256([]byte("environment-local-control-token")), actual)
}

func TestExpectedLocalControlTokenHashFromDataDirectory(t *testing.T) {
	t.Setenv("LOCAL_CONTROL_TOKEN", "")
	t.Setenv("DATA_DIR", t.TempDir())
	expected := sha256.Sum256([]byte("persisted-local-control-token"))
	require.NoError(t, os.WriteFile(
		filepath.Join(os.Getenv("DATA_DIR"), localControlTokenHashFile),
		[]byte(hex.EncodeToString(expected[:])),
		0o600))

	actual, ok := expectedLocalControlTokenHash()

	require.True(t, ok)
	require.Equal(t, expected, actual)
}

func TestExpectedLocalControlTokenHashRejectsInvalidFile(t *testing.T) {
	t.Setenv("LOCAL_CONTROL_TOKEN", "")
	t.Setenv("DATA_DIR", t.TempDir())
	require.NoError(t, os.WriteFile(
		filepath.Join(os.Getenv("DATA_DIR"), localControlTokenHashFile),
		[]byte("not-a-sha256-value"),
		0o600))

	_, ok := expectedLocalControlTokenHash()

	require.False(t, ok)
}
