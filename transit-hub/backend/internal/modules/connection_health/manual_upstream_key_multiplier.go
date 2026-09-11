package connection_health

import (
	"context"
	"math"
)

const maxManualUpstreamKeyMultiplier = 1000000

func (s *Service) SetManualUpstreamKeyMultiplier(ctx context.Context, userID string, targetID string, input ManualUpstreamKeyMultiplierInput) (ManualUpstreamKeyMultiplier, error) {
	if input.Multiplier == nil || math.IsNaN(*input.Multiplier) || math.IsInf(*input.Multiplier, 0) || *input.Multiplier < 0 || *input.Multiplier > maxManualUpstreamKeyMultiplier {
		return ManualUpstreamKeyMultiplier{}, requestError(ErrorRequest)
	}
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return ManualUpstreamKeyMultiplier{}, err
	}
	if _, _, _, _, err := s.resolveManualTarget(ctx, userID, targetID); err != nil {
		return ManualUpstreamKeyMultiplier{}, err
	}
	value := ManualUpstreamKeyMultiplier{
		UserID: userID, AdminAccountID: adminAccountID, TargetID: targetID, Multiplier: *input.Multiplier,
	}
	if err := s.repo.UpsertManualUpstreamKeyMultiplier(ctx, value); err != nil {
		return ManualUpstreamKeyMultiplier{}, err
	}
	return value, nil
}

func (s *Service) ClearManualUpstreamKeyMultiplier(ctx context.Context, userID string, targetID string) error {
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return err
	}
	if _, _, _, _, err := s.resolveManualTarget(ctx, userID, targetID); err != nil {
		return err
	}
	return s.repo.DeleteManualUpstreamKeyMultiplier(ctx, userID, adminAccountID, targetID)
}
