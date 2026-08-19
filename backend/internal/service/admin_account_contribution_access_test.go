package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestEnsureAdminAccountManagementAccessRejectsContributedAccount(t *testing.T) {
	account := &Account{Extra: map[string]any{
		AccountContributionSourceKey: AccountContributionSourceValue,
		AccountContributorUserIDKey:  float64(42),
	}}

	err := ensureAdminAccountManagementAccess(context.Background(), account)
	require.Error(t, err)
	require.Equal(t, http.StatusNotFound, infraerrors.Code(err))
}

func TestEnsureAdminAccountManagementAccessAllowsContributionWorkflow(t *testing.T) {
	account := &Account{Extra: map[string]any{
		AccountContributionSourceKey: AccountContributionSourceValue,
		AccountContributorUserIDKey:  float64(42),
	}}
	ctx := context.WithValue(context.Background(), ctxkey.AllowContributionAccountManagement, true)

	require.NoError(t, ensureAdminAccountManagementAccess(ctx, account))
}

func TestEnsureAdminAccountManagementAccessAllowsAdminPoolAccount(t *testing.T) {
	require.NoError(t, ensureAdminAccountManagementAccess(context.Background(), &Account{ID: 7}))
}
