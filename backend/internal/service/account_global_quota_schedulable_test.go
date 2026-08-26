package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountIsSchedulable_GlobalQuotaAppliesToOAuthButNotCredentialShadow(t *testing.T) {
	now := time.Now()
	extra := map[string]any{
		"quota_daily_limit": 10.0,
		"quota_daily_used":  10.0,
		"quota_daily_start": now.Add(-time.Hour).Format(time.RFC3339),
	}

	oauth := &Account{
		Status: StatusActive, Schedulable: true, Type: AccountTypeOAuth, Extra: extra,
	}
	require.False(t, oauth.IsSchedulable(), "a direct OAuth account must stop in every group when its account total reaches the limit")

	parentID := int64(42)
	shadow := &Account{
		Status: StatusActive, Schedulable: true, Type: AccountTypeOAuth, ParentAccountID: &parentID, Extra: extra,
	}
	require.True(t, shadow.IsSchedulable(), "credential shadows do not own a separate account quota")
}
