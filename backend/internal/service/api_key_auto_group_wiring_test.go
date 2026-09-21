package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type autoGroupWiringAccountRepo struct {
	AccountRepository
	called bool
}

func (r *autoGroupWiringAccountRepo) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]Account, error) {
	r.called = true
	return nil, nil
}

type autoGroupWiringUsageLogRepo struct {
	UsageLogRepository
	called bool
}

func (r *autoGroupWiringUsageLogRepo) ListRecentGroupFirstTokenSamples(context.Context, int64, []int64, string, time.Time, int) (map[int64][]int64, error) {
	r.called = true
	return nil, nil
}

// Regression: the automatic-group selector degrades silently when its optional
// dependencies are not injected. Without the model-availability repository it
// ranks candidates on price alone and can settle on a group whose accounts do
// not serve the requested model, which surfaces to the caller as a 404
// model_not_found. Without the metric repository the speed/balanced strategies
// collapse into price ordering. Neither failure is visible in logs, so assert
// the wiring here.
func TestProvideAPIKeyServiceWiresAutoGroupDependencies(t *testing.T) {
	accountRepo := &autoGroupWiringAccountRepo{}
	usageLogRepo := &autoGroupWiringUsageLogRepo{}

	svc := ProvideAPIKeyService(
		nil, nil, nil, nil, nil, nil,
		&config.Config{},
		accountRepo,
		usageLogRepo,
		nil, nil, nil,
	)

	require.NotNil(t, svc.autoGroupModelRepo,
		"model availability repository must be wired or automatic routing ignores model support")
	require.NotNil(t, svc.autoGroupMetricsRepo,
		"metric repository must be wired or speed/balanced strategies fall back to price only")

	// Prove the wired dependencies are the ones handed in, not an unrelated stub.
	_, err := svc.autoGroupModelRepo.ListModelAvailabilityCandidates(context.Background(), nil, nil, true)
	require.NoError(t, err)
	require.True(t, accountRepo.called)

	_, err = svc.autoGroupMetricsRepo.ListRecentGroupFirstTokenSamples(context.Background(), 1, nil, "", time.Now(), 1)
	require.NoError(t, err)
	require.True(t, usageLogRepo.called)
}
