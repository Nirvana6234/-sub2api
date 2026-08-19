package repository

import (
	"context"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbverification "github.com/Wei-Shaw/sub2api/ent/contributionaccountverification"
	dbroomaccount "github.com/Wei-Shaw/sub2api/ent/contributionroomaccount"
	dbpreference "github.com/Wei-Shaw/sub2api/ent/usercontributionroompreference"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// contributionRoomRoutingRepository keeps the gateway's contribution lookup
// credential-free. It returns only room policy and verified account IDs.
type contributionRoomRoutingRepository struct {
	client *dbent.Client
}

func NewContributionRoomRoutingRepository(client *dbent.Client) service.ContributionRoomRoutingRepository {
	return &contributionRoomRoutingRepository{client: client}
}

func (r *contributionRoomRoutingRepository) ResolveRouteForAPIKey(ctx context.Context, userID, apiKeyID int64) (*service.ContributionRoomRoute, error) {
	if r == nil || r.client == nil || userID <= 0 || apiKeyID <= 0 {
		return nil, nil
	}

	prefs, err := r.client.UserContributionRoomPreference.Query().
		Where(
			dbpreference.UserIDEQ(userID),
			dbpreference.APIKeyIDEQ(apiKeyID),
		).
		WithRoom().
		Order(dbent.Asc(dbpreference.FieldRoomID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	if len(prefs) == 0 {
		return nil, nil
	}
	routeRooms := make([]service.ContributionRoomRouteRoom, 0, len(prefs))
	allowPoolFallback := false
	var fallbackGroupID *int64
	for _, pref := range prefs {
		allowPoolFallback = allowPoolFallback || pref.AllowPoolFallback
		if pref.AllowPoolFallback && pref.FallbackGroupID != nil && fallbackGroupID == nil {
			value := *pref.FallbackGroupID
			fallbackGroupID = &value
		}
		room := pref.Edges.Room
		// A contributor's own accounts are always available through their private
		// route and must never be selected back through their own shared room.
		if room == nil || room.OwnerUserID == userID || room.Status != "active" || room.Visibility != "public" {
			continue
		}
		assignments, queryErr := r.client.ContributionRoomAccount.Query().
			Where(
				dbroomaccount.RoomIDEQ(room.ID),
				dbroomaccount.EnabledEQ(true),
				dbroomaccount.VerifiedAtNotNil(),
				dbroomaccount.HasAccountWith(
					dbaccount.StatusEQ(service.StatusActive),
					dbaccount.SchedulableEQ(true),
				),
			).
			All(ctx)
		if queryErr != nil {
			return nil, queryErr
		}
		candidateIDs := make([]int64, 0, len(assignments))
		accountConcurrencies := make(map[int64]int, len(assignments))
		for _, assignment := range assignments {
			if assignment.ShareBudgetUsd <= assignment.ShareUsedUsd {
				continue
			}
			candidateIDs = append(candidateIDs, assignment.AccountID)
			accountConcurrencies[assignment.AccountID] = assignment.ShareConcurrency
		}
		verifiedIDs := make(map[int64]struct{}, len(candidateIDs))
		if len(candidateIDs) > 0 {
			verifications, verificationErr := r.client.ContributionAccountVerification.Query().
				Where(
					dbverification.AccountIDIn(candidateIDs...),
					dbverification.StatusEQ(service.ContributionVerificationStatusVerified),
				).
				All(ctx)
			if verificationErr != nil {
				return nil, verificationErr
			}
			for _, verification := range verifications {
				if !contributionVerificationModelFamilyMatchesPlatform(verification.ModelFamily, verification.Platform) {
					continue
				}
				verifiedIDs[verification.AccountID] = struct{}{}
			}
		}
		accountIDs := make([]int64, 0, len(verifiedIDs))
		verifiedConcurrencies := make(map[int64]int, len(verifiedIDs))
		for _, accountID := range candidateIDs {
			if _, verified := verifiedIDs[accountID]; verified {
				accountIDs = append(accountIDs, accountID)
				verifiedConcurrencies[accountID] = accountConcurrencies[accountID]
			}
		}
		multiplier := room.ConsumerRateMultiplier
		if multiplier < 0 {
			multiplier = 1.0
		}
		routeRooms = append(routeRooms, service.ContributionRoomRouteRoom{
			RoomID: room.ID, ConsumerRateMultiplier: multiplier, AccountIDs: accountIDs, AccountConcurrencies: verifiedConcurrencies,
		})
	}
	return &service.ContributionRoomRoute{
		Rooms:              routeRooms,
		AllowPoolFallback:  allowPoolFallback,
		FallbackGroupID:    fallbackGroupID,
		ExplicitlySelected: true,
	}, nil
}

func contributionVerificationModelFamilyMatchesPlatform(modelFamily, platform string) bool {
	var expected string
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case service.PlatformOpenAI:
		expected = "gpt"
	case service.PlatformAnthropic:
		expected = "claude"
	case service.PlatformGemini:
		expected = "gemini"
	case service.PlatformGrok:
		expected = "grok"
	default:
		return false
	}
	return strings.EqualFold(strings.TrimSpace(modelFamily), expected)
}
