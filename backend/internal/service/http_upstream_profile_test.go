package service

import (
	"context"
	"testing"
)

func TestWithHTTPUpstreamProfile_DefaultKeepsContext(t *testing.T) {
	ctx := context.Background()
	got := WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileDefault)
	if got != ctx {
		t.Fatal("default profile should not wrap context")
	}
}

func TestWithHTTPUpstreamProfile_OpenAI(t *testing.T) {
	ctx := WithHTTPUpstreamProfile(context.TODO(), HTTPUpstreamProfileOpenAI)
	if profile := HTTPUpstreamProfileFromContext(ctx); profile != HTTPUpstreamProfileOpenAI {
		t.Fatalf("expected profile %q, got %q", HTTPUpstreamProfileOpenAI, profile)
	}
}

func TestWithHTTPUpstreamProfile_LongStream(t *testing.T) {
	ctx := WithHTTPUpstreamProfile(context.TODO(), HTTPUpstreamProfileLongStream)
	if profile := HTTPUpstreamProfileFromContext(ctx); profile != HTTPUpstreamProfileLongStream {
		t.Fatalf("expected profile %q, got %q", HTTPUpstreamProfileLongStream, profile)
	}
}

func TestWithHTTPUpstreamRedirectsDisabled(t *testing.T) {
	//nolint:staticcheck // Exercises the defensive nil-context fallback.
	ctx := WithHTTPUpstreamRedirectsDisabled(nil)
	if !HTTPUpstreamRedirectsDisabled(ctx) {
		t.Fatal("expected redirects to be disabled")
	}
	if HTTPUpstreamRedirectsDisabled(context.Background()) {
		t.Fatal("redirects should remain enabled by default")
	}
}

func TestWithHTTPUpstreamPublicHostsOnly(t *testing.T) {
	//nolint:staticcheck // Exercises the defensive nil-context fallback.
	ctx := WithHTTPUpstreamPublicHostsOnly(nil)
	if !HTTPUpstreamPublicHostsOnly(ctx) {
		t.Fatal("expected public-hosts-only marker to be set")
	}
	if HTTPUpstreamPublicHostsOnly(context.Background()) {
		t.Fatal("marker must be absent by default")
	}
	if HTTPUpstreamRedirectsDisabled(ctx) {
		t.Fatal("public-hosts-only must not disable redirects")
	}
}

func TestWithHTTPUpstreamPublicProxyOnly(t *testing.T) {
	ctx := WithHTTPUpstreamPublicProxyOnly(nil)
	if !HTTPUpstreamPublicProxyOnly(ctx) {
		t.Fatal("expected public-proxy-only marker to be set")
	}
	if HTTPUpstreamPublicProxyOnly(context.Background()) {
		t.Fatal("marker must be absent by default")
	}
	if HTTPUpstreamPublicHostsOnly(ctx) {
		t.Fatal("public-proxy-only must not imply public upstream hosts")
	}
}

func TestWithHTTPUpstreamPublicHostsOnlyForAccount(t *testing.T) {
	ownerID := int64(42)
	contributor := &Account{
		Extra: map[string]any{
			AccountContributionSourceKey: AccountContributionSourceValue,
			AccountContributorUserIDKey:  float64(ownerID),
		},
		Proxy: &Proxy{OwnerUserID: &ownerID},
	}
	ctx := WithHTTPUpstreamPublicHostsOnlyForAccount(context.Background(), contributor)
	if !HTTPUpstreamPublicHostsOnly(ctx) {
		t.Fatal("contributed account must require a public upstream destination")
	}
	if !HTTPUpstreamPublicProxyOnly(ctx) {
		t.Fatal("user-owned proxy must require a public proxy endpoint")
	}

	proxyOnly := &Account{Proxy: &Proxy{OwnerUserID: &ownerID}}
	ctx = WithHTTPUpstreamPublicHostsOnlyForAccount(context.Background(), proxyOnly)
	if HTTPUpstreamPublicHostsOnly(ctx) {
		t.Fatal("admin account must not inherit public upstream destination policy")
	}
	if !HTTPUpstreamPublicProxyOnly(ctx) {
		t.Fatal("user-owned proxy must require a public proxy endpoint")
	}

	adminProxy := &Account{Proxy: &Proxy{}}
	ctx = WithHTTPUpstreamPublicHostsOnlyForAccount(context.Background(), adminProxy)
	if HTTPUpstreamPublicHostsOnly(ctx) || HTTPUpstreamPublicProxyOnly(ctx) {
		t.Fatal("administrator-managed proxy must retain existing policy")
	}
}
