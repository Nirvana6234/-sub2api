package connection_health

import (
	"context"
	"errors"
	"testing"

	"transithub/backend/internal/modules/my_sites"
	"transithub/backend/internal/modules/upstream"
)

type fakeSiteLookup struct {
	site *upstream.Site
	err  error
}

func (f fakeSiteLookup) GetSite(context.Context, string) (*upstream.Site, error) {
	return f.site, f.err
}

type fakeSessionProvider struct {
	session upstream.Session
	err     error
}

func (f fakeSessionProvider) RequireSession(context.Context, string, string) (upstream.Session, error) {
	return f.session, f.err
}

type fakePlatformActioner struct {
	err        error
	panicValue any
	calls      []struct {
		channelID string
		weight    int
		status    int
	}
	sub2APICalls []struct {
		accountID string
		status    string
	}
	sub2APIErr error
}

func (f *fakePlatformActioner) UpdateNewAPIChannelWeightStatus(_ upstream.Session, channelID string, weight, status int) error {
	if f.panicValue != nil {
		panic(f.panicValue)
	}
	f.calls = append(f.calls, struct {
		channelID string
		weight    int
		status    int
	}{channelID, weight, status})
	return f.err
}

func (f *fakePlatformActioner) UpdateSub2APIAdminAccountStatus(_ upstream.Session, accountID, status string) error {
	if f.panicValue != nil {
		panic(f.panicValue)
	}
	f.sub2APICalls = append(f.sub2APICalls, struct {
		accountID string
		status    string
	}{accountID, status})
	return f.sub2APIErr
}

func TestActions_NewAPITargetRemoteActions(t *testing.T) {
	platform := &fakePlatformActioner{}
	dispatcher := newRemoteActionDispatcher(fakeSiteLookup{}, fakeSessionProvider{}, platform)
	target := AdminProbeTarget{Platform: string(upstream.PlatformNewAPI), AccountID: "100"}

	action, err := dispatcher.DegradeTarget(context.Background(), upstream.Session{Platform: upstream.PlatformNewAPI}, target, ConnectionHealthState{})
	if err != nil || action != "newapi_channel_disabled" || len(platform.calls) != 1 || platform.calls[0].weight != 0 || platform.calls[0].status != 2 {
		t.Fatalf("unexpected NewAPI degrade: action=%q err=%v calls=%+v", action, err, platform.calls)
	}

	action, err = dispatcher.RestoreTarget(context.Background(), upstream.Session{Platform: upstream.PlatformNewAPI}, target, ConnectionHealthState{CurrentWeight: 25})
	if err != nil || action != "newapi_channel_weight_25" || len(platform.calls) != 2 || platform.calls[1].weight != 25 || platform.calls[1].status != 1 {
		t.Fatalf("unexpected NewAPI restore: action=%q err=%v calls=%+v", action, err, platform.calls)
	}
}

func TestActions_Sub2APITargetActionsAreReadOnly(t *testing.T) {
	platform := &fakePlatformActioner{sub2APIErr: errors.New("must not be called")}
	dispatcher := newRemoteActionDispatcher(fakeSiteLookup{}, fakeSessionProvider{}, platform)
	session := upstream.Session{Platform: upstream.PlatformSub2API}
	target := AdminProbeTarget{Platform: string(upstream.PlatformSub2API), AccountID: "acc-1"}

	for _, call := range []func() (string, error){
		func() (string, error) {
			return dispatcher.DegradeTarget(context.Background(), session, target, ConnectionHealthState{})
		},
		func() (string, error) {
			return dispatcher.RestoreTarget(context.Background(), session, target, ConnectionHealthState{})
		},
		func() (string, error) {
			return dispatcher.ApplyTargetState(context.Background(), session, target, nil, "inactive")
		},
	} {
		action, err := call()
		if err != nil || action != RemoteActionUnsupported {
			t.Fatalf("Sub2API target action must be read-only: action=%q err=%v", action, err)
		}
	}
	if len(platform.sub2APICalls) != 0 {
		t.Fatalf("administrator state must not be written: %+v", platform.sub2APICalls)
	}
}

func TestActions_LegacySub2APIRealConnectionPathsAreReadOnly(t *testing.T) {
	platform := &fakePlatformActioner{sub2APIErr: errors.New("must not be called")}
	sites := fakeSiteLookup{site: &upstream.Site{ID: "site-1", Platform: upstream.PlatformSub2API}}
	sessions := fakeSessionProvider{session: upstream.Session{Platform: upstream.PlatformSub2API}}
	dispatcher := newRemoteActionDispatcher(sites, sessions, platform)
	conn := my_sites.RealConnection{UpstreamSiteID: "site-1", AdminAccountID: "acc-1"}

	degrade, err := dispatcher.Degrade(context.Background(), conn, ConnectionHealthState{})
	if err != nil || degrade != RemoteActionUnsupported {
		t.Fatalf("unexpected legacy degrade: action=%q err=%v", degrade, err)
	}
	restore, err := dispatcher.Restore(context.Background(), conn, ConnectionHealthState{})
	if err != nil || restore != RemoteActionUnsupported {
		t.Fatalf("unexpected legacy restore: action=%q err=%v", restore, err)
	}
	if len(platform.sub2APICalls) != 0 {
		t.Fatalf("legacy path must not write Sub2API account state: %+v", platform.sub2APICalls)
	}
}
