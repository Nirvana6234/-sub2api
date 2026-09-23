package service

import "context"

// HTTPUpstreamProfile marks HTTP upstream requests that need provider-specific
// transport policy.
type HTTPUpstreamProfile string

const (
	HTTPUpstreamProfileDefault    HTTPUpstreamProfile = ""
	HTTPUpstreamProfileOpenAI     HTTPUpstreamProfile = "openai"
	HTTPUpstreamProfileGrok       HTTPUpstreamProfile = "grok"
	HTTPUpstreamProfileLongStream HTTPUpstreamProfile = "long_stream"
)

type httpUpstreamProfileContextKey struct{}
type httpUpstreamDisableRedirectsContextKey struct{}
type httpUpstreamPublicHostsOnlyContextKey struct{}
type httpUpstreamPublicProxyOnlyContextKey struct{}

// WithHTTPUpstreamProfile injects an upstream transport profile into ctx.
func WithHTTPUpstreamProfile(ctx context.Context, profile HTTPUpstreamProfile) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if profile == HTTPUpstreamProfileDefault {
		return ctx
	}
	return context.WithValue(ctx, httpUpstreamProfileContextKey{}, profile)
}

// HTTPUpstreamProfileFromContext resolves the upstream transport profile from ctx.
func HTTPUpstreamProfileFromContext(ctx context.Context) HTTPUpstreamProfile {
	if ctx == nil {
		return HTTPUpstreamProfileDefault
	}
	profile, ok := ctx.Value(httpUpstreamProfileContextKey{}).(HTTPUpstreamProfile)
	if !ok {
		return HTTPUpstreamProfileDefault
	}
	switch profile {
	case HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileGrok, HTTPUpstreamProfileLongStream:
		return profile
	default:
		return HTTPUpstreamProfileDefault
	}
}

// WithHTTPUpstreamRedirectsDisabled prevents credential-bearing probes from
// following redirects through the shared upstream client.
func WithHTTPUpstreamRedirectsDisabled(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, httpUpstreamDisableRedirectsContextKey{}, true)
}

func HTTPUpstreamRedirectsDisabled(ctx context.Context) bool {
	return ctx != nil && ctx.Value(httpUpstreamDisableRedirectsContextKey{}) == true
}

// WithHTTPUpstreamPublicHostsOnly marks a request whose destination, and every
// redirect hop after it, must resolve to a public address. The shared upstream
// client enforces it regardless of the security.url_allowlist configuration;
// use it for fetches whose URL comes from an untrusted upstream response.
func WithHTTPUpstreamPublicHostsOnly(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, httpUpstreamPublicHostsOnlyContextKey{}, true)
}

func HTTPUpstreamPublicHostsOnly(ctx context.Context) bool {
	return ctx != nil && ctx.Value(httpUpstreamPublicHostsOnlyContextKey{}) == true
}

// WithHTTPUpstreamPublicProxyOnly marks requests that must use a proxy whose
// endpoint resolves to a public address. This is separate from the upstream
// destination policy because an operator-managed proxy may intentionally be
// private while a user-owned proxy must not become an internal pivot.
func WithHTTPUpstreamPublicProxyOnly(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, httpUpstreamPublicProxyOnlyContextKey{}, true)
}

func HTTPUpstreamPublicProxyOnly(ctx context.Context) bool {
	return ctx != nil && ctx.Value(httpUpstreamPublicProxyOnlyContextKey{}) == true
}

// requiresHTTPUpstreamPublicPolicy reports whether an account's outbound
// request must use the shared HTTPUpstream public-address policy. Both
// contributor accounts and user-owned proxies are untrusted request pivots.
func requiresHTTPUpstreamPublicPolicy(account *Account) bool {
	if account == nil {
		return false
	}
	if account.ContributorUserID() > 0 {
		return true
	}
	return account.Proxy != nil && account.Proxy.OwnerUserID != nil && *account.Proxy.OwnerUserID > 0
}

// WithHTTPUpstreamPublicHostsOnlyForAccount applies the public-destination
// policy to user-contributed accounts. Administrator-managed accounts may
// intentionally use private endpoints and proxies and retain the existing
// behavior.
func WithHTTPUpstreamPublicHostsOnlyForAccount(ctx context.Context, account *Account) context.Context {
	if account == nil {
		return ctx
	}
	if account.ContributorUserID() > 0 {
		ctx = WithHTTPUpstreamPublicHostsOnly(ctx)
	}
	if account.Proxy != nil && account.Proxy.OwnerUserID != nil && *account.Proxy.OwnerUserID > 0 {
		ctx = WithHTTPUpstreamPublicProxyOnly(ctx)
	}
	return ctx
}
