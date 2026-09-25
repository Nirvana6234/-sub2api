//go:build unit

package service

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestObservedSettingRepositoryNotifiesAfterSuccessfulWrites(t *testing.T) {
	hub := NewSettingChangeHub()
	var got [][]string
	unsubscribe := hub.Subscribe(func(keys []string) {
		cp := append([]string(nil), keys...)
		sort.Strings(cp)
		got = append(got, cp)
	})
	repo := NewObservedSettingRepository(&mapSettingRepo{values: map[string]string{}}, hub)
	ctx := context.Background()

	require.NoError(t, repo.Set(ctx, "a", "1"))
	require.NoError(t, repo.SetMultiple(ctx, map[string]string{"b": "2", "c": "3"}))
	require.NoError(t, repo.Delete(ctx, "a"))
	_, err := repo.GetValue(ctx, "b")
	require.NoError(t, err)
	require.Equal(t, [][]string{{"a"}, {"b", "c"}, {"a"}}, got, "reads do not notify")

	unsubscribe()
	require.NoError(t, repo.Set(ctx, "d", "4"))
	require.Len(t, got, 3, "no notifications after unsubscribing")
}

func TestObservedSettingRepositoryDoesNotNotifyFailedWrites(t *testing.T) {
	hub := NewSettingChangeHub()
	calls := 0
	hub.Subscribe(func([]string) { calls++ })
	repo := NewObservedSettingRepository(&failingSettingRepo{mapSettingRepo{values: map[string]string{}}}, hub)
	require.Error(t, repo.Set(context.Background(), "a", "1"))
	require.Zero(t, calls)
}

func TestObservedSettingRepositoryWithoutHubIsTheInnerRepository(t *testing.T) {
	inner := &mapSettingRepo{values: map[string]string{}}
	require.Same(t, inner, NewObservedSettingRepository(inner, nil))
}

type failingSettingRepo struct{ mapSettingRepo }

func (*failingSettingRepo) Set(context.Context, string, string) error { return errors.New("db down") }
