package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// PlaygroundAPIKeyProvisioner isolates playground bootstrap from upstream
// authentication. Implementations must be idempotent and fail open at signup.
type PlaygroundAPIKeyProvisioner interface {
	EnsurePlaygroundAPIKeys(ctx context.Context, userID int64) error
}

func (s *AuthService) provisionPlaygroundAPIKeys(ctx context.Context, userID int64) {
	if s == nil || s.playgroundAPIKeys == nil || userID <= 0 {
		return
	}
	if err := s.playgroundAPIKeys.EnsurePlaygroundAPIKeys(ctx, userID); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to provision playground API keys: user_id=%d err=%v", userID, err)
	}
}
