package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const workspaceFallbackMaxAccountSwitches = 64

func maxAccountSwitchesForRequest(ctx context.Context, configured int) int {
	if service.IsWorkspaceLocalFallbackRoute(ctx) && configured < workspaceFallbackMaxAccountSwitches {
		return workspaceFallbackMaxAccountSwitches
	}
	return configured
}
