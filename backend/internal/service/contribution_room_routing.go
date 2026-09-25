package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

const ContributionVerificationStatusVerified = "verified"

// ContributionRoomRoute is the small, credential-free routing snapshot needed
// by the gateways. Account data remains in AccountRepository; this repository
// only decides whether a user selected a room and which verified accounts are
// eligible to enter that room's candidate set.
type ContributionRoomRoute struct {
	Rooms              []ContributionRoomRouteRoom
	AllowPoolFallback  bool
	FallbackGroupID    *int64
	ExplicitlySelected bool
}

// ContributionRoomRouteRoom keeps a room's account IDs tied to its own rate.
// A user can select several rooms, and their multipliers must never bleed into
// one another during later scheduling or asynchronous billing.
type ContributionRoomRouteRoom struct {
	RoomID                 int64
	ConsumerRateMultiplier float64
	AccountIDs             []int64
	AccountConcurrencies   map[int64]int
}

func (r *ContributionRoomRoute) HasRooms() bool {
	return r != nil && len(r.Rooms) > 0
}

// IsExplicitSelection distinguishes "the user never selected a room" from
// "the selected rooms are temporarily unavailable". The latter must remain
// isolated from normal account groups and may only use the explicitly enabled
// public-pool fallback.
func (r *ContributionRoomRoute) IsExplicitSelection() bool {
	return r != nil && (r.ExplicitlySelected || len(r.Rooms) > 0)
}

// ContributionRoomRoutingRepository is intentionally read-only for gateway
// code. Room administration belongs to the contribution-management surface.
type ContributionRoomRoutingRepository interface {
	ResolveRouteForAPIKey(ctx context.Context, userID, apiKeyID int64) (*ContributionRoomRoute, error)
}

func hasContributionRoomRoute(ctx context.Context, repo ContributionRoomRoutingRepository) bool {
	if repo == nil {
		return false
	}
	userID := contributorUserIDFromContext(ctx)
	if userID <= 0 {
		return false
	}
	apiKeyID := contributorAPIKeyIDFromContext(ctx)
	if apiKeyID <= 0 {
		return false
	}
	route, err := repo.ResolveRouteForAPIKey(ctx, userID, apiKeyID)
	// On a lookup error, do not let a stale sticky binding bypass the user's
	// room choice. The normal candidate lookup will surface the actual error.
	return err != nil || route.IsExplicitSelection()
}

func contributorAPIKeyIDFromContext(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	apiKeyID, _ := ctx.Value(ctxkey.APIKeyID).(int64)
	return apiKeyID
}

// filterContributionAccountsForCaller removes other users' self-use
// contributions from a group's default candidate list. Such accounts stay bound
// to the contributor's groups so the contributor can reach them, which also
// puts them in every other member's group listing.
func filterContributionAccountsForCaller(ctx context.Context, accounts []Account) []Account {
	return filterContributionAccountsForUser(contributorUserIDFromContext(ctx), accounts)
}

// filterContributionAccountsForUser is filterContributionAccountsForCaller for
// work that runs without the request context, such as batch image jobs.
func filterContributionAccountsForUser(userID int64, accounts []Account) []Account {
	var filtered []Account
	for i := range accounts {
		if accounts[i].IsContributionAvailableTo(userID) {
			if filtered != nil {
				filtered = append(filtered, accounts[i])
			}
			continue
		}
		if filtered == nil {
			filtered = make([]Account, i, len(accounts))
			copy(filtered, accounts[:i])
		}
	}
	if filtered == nil {
		return accounts
	}
	return filtered
}

// contributionAccountBlockedForCaller guards selection paths that load an
// account by ID (sticky sessions, previous_response_id bindings, fresh
// rechecks) instead of taking it from the filtered candidate list. A caller who
// explicitly selected a contribution room may legitimately reach another
// user's contribution; the reloaded copy has lost its room marker, so the room
// route itself is the authorization.
func contributionAccountBlockedForCaller(ctx context.Context, account *Account, repo ContributionRoomRoutingRepository) bool {
	if account.IsContributionAvailableTo(contributorUserIDFromContext(ctx)) {
		return false
	}
	return !hasContributionRoomRoute(ctx, repo)
}

func (s *GatewayService) hasContributionRoomRoute(ctx context.Context) bool {
	return s != nil && hasContributionRoomRoute(ctx, s.contributionRoomRepo)
}

func (s *OpenAIGatewayService) hasContributionRoomRoute(ctx context.Context) bool {
	return s != nil && hasContributionRoomRoute(ctx, s.contributionRoomRepo)
}

func cloneContributionRouteAccount(account Account, source string, roomID int64, multiplier float64) Account {
	account.ContributionRouteSource = source
	account.ContributionRoomID = roomID
	if source == ContributionRouteSourceRoom {
		value := multiplier
		account.ContributionRateMultiplierOverride = &value
	} else {
		account.ContributionRateMultiplierOverride = nil
		account.ContributionConcurrencyOverride = nil
	}
	return account
}

func applyContributionRoomConcurrency(account *Account, shareConcurrency int) {
	if account == nil || shareConcurrency <= 0 {
		return
	}
	value := shareConcurrency
	account.ContributionConcurrencyOverride = &value
	if account.Concurrency <= 0 || shareConcurrency < account.Concurrency {
		account.Concurrency = shareConcurrency
	}
}

func preserveContributionRouteMetadata(from, to *Account) *Account {
	if from == nil || to == nil || from.ContributionRouteSource == ContributionRouteSourceNone {
		return to
	}
	to.ContributionRouteSource = from.ContributionRouteSource
	to.ContributionRoomID = from.ContributionRoomID
	if from.ContributionRateMultiplierOverride != nil {
		value := *from.ContributionRateMultiplierOverride
		to.ContributionRateMultiplierOverride = &value
	} else {
		to.ContributionRateMultiplierOverride = nil
	}
	if from.ContributionConcurrencyOverride != nil {
		value := *from.ContributionConcurrencyOverride
		to.ContributionConcurrencyOverride = &value
		if to.Concurrency <= 0 || value < to.Concurrency {
			to.Concurrency = value
		}
	} else {
		to.ContributionConcurrencyOverride = nil
	}
	return to
}

// ContributionRoomWiring is a Wire marker type: its sole purpose is to force
// the DI graph to construct it (and therefore run its provider) after
// GatewayService/OpenAIGatewayService exist, so the optional contribution-room
// routing repository gets injected via the setter methods instead of being
// left nil. See SetContributionRoomRoutingRepository's doc comment for why
// this is a setter rather than a constructor parameter.
type ContributionRoomWiring struct{}

// ProvideContributionRoomWiring wires the routing repository into both
// gateway services after they are constructed. Add ContributionRoomWiring as
// a dependency of any provider guaranteed to run during startup (e.g.
// provideCleanup in cmd/server/wire.go) so Wire actually calls this.
func ProvideContributionRoomWiring(gs *GatewayService, ogs *OpenAIGatewayService, repo ContributionRoomRoutingRepository) ContributionRoomWiring {
	cached := newCachedContributionRoomRoutes(repo, contributionRoomRouteCacheTTL)
	gs.SetContributionRoomRoutingRepository(cached)
	ogs.SetContributionRoomRoutingRepository(cached)
	return ContributionRoomWiring{}
}
