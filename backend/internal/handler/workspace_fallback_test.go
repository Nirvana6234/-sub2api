package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestMaxAccountSwitchesForWorkspaceFallback(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.WorkspaceLocalFallbackRoute, true)

	require.Equal(t, workspaceFallbackMaxAccountSwitches, maxAccountSwitchesForRequest(ctx, 10))
	require.Equal(t, 80, maxAccountSwitchesForRequest(ctx, 80))
	require.Equal(t, 10, maxAccountSwitchesForRequest(context.Background(), 10))
}
