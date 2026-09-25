package upstream

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// NewInternalAdminHTTPClient keeps saved public site identities intact while
// routing only explicitly configured Sub2API administration/authentication APIs
// over a trusted internal connection. Other origins and API traffic are unchanged.
func NewInternalAdminHTTPClient(client *http.Client, origins []string, target string) (*http.Client, error) {
	if len(origins) == 0 && strings.TrimSpace(target) == "" {
		return client, nil
	}
	if len(origins) == 0 || strings.TrimSpace(target) == "" {
		return nil, fmt.Errorf("internal admin routing requires both origins and target URL")
	}
	internalURL, err := parseAdminOrigin(target)
	if err != nil {
		return nil, fmt.Errorf("invalid internal admin target: %w", err)
	}
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		u, err := parseAdminOrigin(origin)
		if err != nil {
			return nil, fmt.Errorf("invalid internal admin public origin: %w", err)
		}
		allowed[adminOriginKey(u)] = struct{}{}
	}
	if client == nil {
		client = &http.Client{}
	}
	publicTransport := client.Transport
	if publicTransport == nil {
		publicTransport = http.DefaultTransport
	}
	// Do not send internal admin credentials through HTTP(S)_PROXY.
	internalTransport := http.DefaultTransport.(*http.Transport).Clone()
	internalTransport.Proxy = nil
	router := &internalAdminTransport{public: publicTransport, internal: internalTransport, target: internalURL, origins: allowed}
	clone := *client
	clone.Transport = router
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if router.matches(req.URL) {
			return fmt.Errorf("redirects into internal admin APIs are not allowed")
		}
		for _, previous := range via {
			if router.matches(previous.URL) {
				return fmt.Errorf("internal admin API redirects are not allowed")
			}
		}
		if client.CheckRedirect != nil {
			return client.CheckRedirect(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	return &clone, nil
}

func parseAdminOrigin(value string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("expected an HTTP(S) origin without credentials, path, query or fragment")
	}
	return u, nil
}

func adminOriginKey(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return u.Scheme + "://" + strings.ToLower(u.Hostname()) + ":" + port
}

type internalAdminTransport struct {
	public, internal http.RoundTripper
	target           *url.URL
	origins          map[string]struct{}
}

func (t *internalAdminTransport) matches(u *url.URL) bool {
	if u.User != nil || u.Opaque != "" {
		return false
	}
	if _, ok := t.origins[adminOriginKey(u)]; !ok {
		return false
	}
	for _, prefix := range []string{"/api/v1/admin", "/api/v1/auth"} {
		if u.Path == prefix || strings.HasPrefix(u.Path, prefix+"/") {
			return true
		}
	}
	return false
}

func (t *internalAdminTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.matches(req.URL) {
		return t.public.RoundTrip(req)
	}
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	clone.Host = t.target.Host
	return t.internal.RoundTrip(clone)
}
