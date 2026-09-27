package keycodec

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/accountcodec"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 每个字段都要归类：sent（发给从节点）或 kept（留在主节点）。新增字段时这里失败，提醒决定它归哪一类。
var fieldClasses = map[reflect.Type]struct{ sent, kept []string }{
	reflect.TypeOf(service.APIKey{}): {
		sent: []string{"ID", "UserID", "Name", "GroupID", "AutoGroup", "Status", "User", "Group"},
		kept: []string{
			// 原文由从节点自己填回；鉴权、额度、限额在主节点复查。
			"Key", "AutoGroupStrategy", "AutoGroupIDs", "IPWhitelist", "IPBlacklist", "CompiledIPWhitelist", "CompiledIPBlacklist",
			"LastUsedAt", "LastUsedIP", "CreatedAt", "UpdatedAt", "AutoGroupCurrentGroup", "AutoGroupCurrentModel",
			"AutoGroupCurrentSelectedAt", "CurrentConcurrency", "Quota", "QuotaUsed", "ExpiresAt",
			"RateLimit5h", "RateLimit1d", "RateLimit7d", "Usage5h", "Usage1d", "Usage7d",
			"Window5hStart", "Window1dStart", "Window7dStart", "RelayNodeID", "RelayNodeChangedAt",
		},
	},
	reflect.TypeOf(service.User{}): {
		sent: []string{"ID", "Role", "Concurrency", "Status"},
		kept: []string{
			"Email", "Username", "Notes", "AvatarURL", "AvatarSource", "AvatarMIME", "AvatarByteSize", "AvatarSHA256",
			"PasswordHash", "Balance", "FrozenBalance", "RelayReservedBalance", "AllowedGroups", "RestrictPublicGroups",
			"TokenVersion", "TokenVersionResolved", "SignupSource", "LastLoginAt", "LastActiveAt", "LastUsedAt",
			"CreatedAt", "UpdatedAt", "DeletedAt", "GroupRates", "TotpSecretEncrypted", "TotpEnabled", "TotpEnabledAt",
			"BalanceNotifyEnabled", "BalanceNotifyThresholdType", "BalanceNotifyThreshold", "BalanceNotifyExtraEmails",
			"TotalRecharged", "APIKeys", "AccountManagementEnabled", "ContributionRoomsEnabled", "RPMLimit", "Subscriptions",
			"UserGroupRPMOverride", "UserGroupRPMOverrideGroupID",
		},
	},
	reflect.TypeOf(service.UserSubscription{}): {
		sent: []string{"ID", "UserID", "GroupID", "StartsAt", "ExpiresAt", "Status"},
		kept: []string{
			"DailyWindowStart", "WeeklyWindowStart", "MonthlyWindowStart", "DailyUsageUSD", "WeeklyUsageUSD",
			"MonthlyUsageUSD", "AssignedBy", "AssignedAt", "Notes", "CreatedAt", "UpdatedAt", "DeletedAt",
			"User", "Group", "AssignedByUser",
		},
	},
	reflect.TypeOf(service.Group{}): {
		kept: []string{"AccountGroups", "AccountCount", "ActiveAccountCount", "RateLimitedAccountCount"},
	},
}

func TestEveryFieldIsClassified(t *testing.T) {
	for typ, cls := range fieldClasses {
		known := map[string]bool{}
		for _, n := range append(append([]string{}, cls.sent...), cls.kept...) {
			require.False(t, known[n], "%s.%s classified twice", typ.Name(), n)
			known[n] = true
		}
		var missing []string
		for i := 0; i < typ.NumField(); i++ {
			name := typ.Field(i).Name
			if typ == reflect.TypeOf(service.Group{}) {
				// 分组除了 kept 以外全部发送：只要求不像密钥。
				require.False(t, accountcodec.LooksSecret(name), "group field %s looks secret", name)
				continue
			}
			if !known[name] {
				missing = append(missing, name)
			}
			delete(known, name)
		}
		if typ == reflect.TypeOf(service.Group{}) {
			for _, n := range cls.kept {
				_, ok := typ.FieldByName(n)
				require.True(t, ok, "group has no field %s", n)
			}
			continue
		}
		sort.Strings(missing)
		require.Empty(t, missing, "%s fields must be classified in fieldClasses", typ.Name())
		require.Empty(t, known, "%s: classified fields that no longer exist", typ.Name())
	}
}

func TestAPIKeyRoundTripKeepsOnlySentFields(t *testing.T) {
	gid := int64(9)
	now := time.Unix(1_700_000_000, 0).UTC()
	in := &service.APIKey{
		ID: 3, UserID: 5, Key: "sk-secret", Name: "k", GroupID: &gid, Status: service.StatusActive,
		IPWhitelist: []string{"1.2.3.4"}, Quota: 10, ExpiresAt: &now,
		User: &service.User{ID: 5, Email: "a@b.c", PasswordHash: "hash", Balance: 42, Role: "user", Concurrency: 7, Status: service.StatusActive},
		Group: &service.Group{
			ID: 9, Name: "g", Platform: service.PlatformOpenAI, RateMultiplier: 1.5, Hydrated: true,
			MaxReasoningEffort: "high", AllowImageGeneration: true, AccountCount: 12,
			AccountGroups: []service.AccountGroup{{AccountID: 1, GroupID: 9}},
		},
	}
	data, err := EncodeAPIKey(in)
	require.NoError(t, err)
	require.NotContains(t, string(data), "sk-secret")
	require.NotContains(t, string(data), "a@b.c")
	require.NotContains(t, string(data), "hash")

	out, err := DecodeAPIKey(data, "sk-raw")
	require.NoError(t, err)
	require.Equal(t, "sk-raw", out.Key)
	require.Equal(t, int64(3), out.ID)
	require.Equal(t, &gid, out.GroupID)
	require.Nil(t, out.IPWhitelist)
	require.Zero(t, out.Quota)
	require.Nil(t, out.ExpiresAt)
	require.Equal(t, &service.User{ID: 5, Role: "user", Concurrency: 7, Status: service.StatusActive}, out.User)

	wantGroup := *in.Group
	wantGroup.AccountGroups, wantGroup.AccountCount = nil, 0
	require.Equal(t, &wantGroup, out.Group)
}

func TestSubscriptionRoundTrip(t *testing.T) {
	data, err := EncodeSubscription(nil)
	require.NoError(t, err)
	got, err := DecodeSubscription(data)
	require.NoError(t, err)
	require.Nil(t, got)

	now := time.Unix(1_700_000_000, 0).UTC()
	data, err = EncodeSubscription(&service.UserSubscription{ID: 1, UserID: 2, GroupID: 3, StartsAt: now, ExpiresAt: now.Add(time.Hour), Status: "active", DailyUsageUSD: 4, Notes: "n"})
	require.NoError(t, err)
	got, err = DecodeSubscription(data)
	require.NoError(t, err)
	require.Equal(t, &service.UserSubscription{ID: 1, UserID: 2, GroupID: 3, StartsAt: now, ExpiresAt: now.Add(time.Hour), Status: "active"}, got)
}
