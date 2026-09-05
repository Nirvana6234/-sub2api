//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSettingService_GetPublicSettings_PlaygroundDefaultsFalseAndInjectionMatches(t *testing.T) {
	svc := NewSettingService(&settingPublicRepoStub{values: map[string]string{}}, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.PlaygroundEnabled)

	injected, err := svc.GetPublicSettingsForInjection(context.Background())
	require.NoError(t, err)

	payload, ok := injected.(*PublicSettingsInjectionPayload)
	require.True(t, ok)
	require.False(t, payload.PlaygroundEnabled)
}

func TestSettingService_GetPublicSettings_PlaygroundEnabledExposedInInjection(t *testing.T) {
	svc := NewSettingService(&settingPublicRepoStub{values: map[string]string{
		SettingKeyPlaygroundEnabled: "true",
	}}, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.PlaygroundEnabled)

	injected, err := svc.GetPublicSettingsForInjection(context.Background())
	require.NoError(t, err)

	payload, ok := injected.(*PublicSettingsInjectionPayload)
	require.True(t, ok)
	require.True(t, payload.PlaygroundEnabled)
}

func TestSettingService_ParseSettings_PlaygroundEnabled(t *testing.T) {
	svc := NewSettingService(&settingPublicRepoStub{values: map[string]string{}}, &config.Config{})

	settings := svc.parseSettings(map[string]string{SettingKeyPlaygroundEnabled: "true"})

	require.True(t, settings.PlaygroundEnabled)
}

func TestSettingService_IsPlaygroundEnabled_FailsClosedOnReadError(t *testing.T) {
	repo := &bmRepoStub{
		getValueFn: func(ctx context.Context, key string) (string, error) {
			require.Equal(t, SettingKeyPlaygroundEnabled, key)
			return "", errors.New("db down")
		},
	}
	svc := NewSettingService(repo, &config.Config{})

	require.False(t, svc.IsPlaygroundEnabled(context.Background()))
	require.Equal(t, 1, repo.calls)
}
