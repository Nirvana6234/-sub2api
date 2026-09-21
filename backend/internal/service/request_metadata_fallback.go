package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// IsWorkspaceLocalFallbackRoute reports whether the request came through the
// desktop workspace's fixed local relay route.
func IsWorkspaceLocalFallbackRoute(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(ctxkey.WorkspaceLocalFallbackRoute).(bool)
	return v
}
