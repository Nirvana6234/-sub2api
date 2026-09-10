package service

import "context"

// hasContributionRoomRoute reports whether the current request is being routed
// through a user-selected shared contribution-room account pool.
//
// The contribution-room feature (shared/contributed account pools with their
// own wallet and reward economics — ent entities ContributionRoom,
// ContributionRoomAccount, UserContributionRoomPreference, ~70 files touched
// upstream on local/main across migrations 176-199) is not part of this
// upstream-rebuild branch yet. Until it is ported, this always reports false,
// so callers keep their normal (non-contribution-room) sticky-session behavior.
func (s *OpenAIGatewayService) hasContributionRoomRoute(ctx context.Context) bool {
	return false
}
