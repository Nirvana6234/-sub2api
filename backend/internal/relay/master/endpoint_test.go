package master

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRelayEndpoint(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		canonical string
		host      string
		port      int
		explicit  bool
		urlHost   string
	}{
		{name: "hostname", raw: " Relay1.Example.com. ", canonical: "relay1.example.com", host: "relay1.example.com", port: 443, urlHost: "relay1.example.com"},
		{name: "hostname port", raw: "relay1.example.com:8443", canonical: "relay1.example.com:8443", host: "relay1.example.com", port: 8443, explicit: true, urlHost: "relay1.example.com:8443"},
		{name: "ipv4", raw: "192.0.2.10", canonical: "192.0.2.10", host: "192.0.2.10", port: 443, urlHost: "192.0.2.10"},
		{name: "ipv4 port", raw: "192.0.2.10:18081", canonical: "192.0.2.10:18081", host: "192.0.2.10", port: 18081, explicit: true, urlHost: "192.0.2.10:18081"},
		{name: "ipv6", raw: "2001:db8::10", canonical: "2001:db8::10", host: "2001:db8::10", port: 443, urlHost: "[2001:db8::10]"},
		{name: "ipv6 port", raw: "[2001:db8::10]:8443", canonical: "[2001:db8::10]:8443", host: "2001:db8::10", port: 8443, explicit: true, urlHost: "[2001:db8::10]:8443"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint, err := ParseRelayEndpoint(tt.raw)
			require.NoError(t, err)
			require.Equal(t, tt.canonical, endpoint.String())
			require.Equal(t, tt.host, endpoint.Host)
			require.Equal(t, tt.port, endpoint.Port)
			require.Equal(t, tt.explicit, endpoint.PortExplicit)
			require.Equal(t, tt.urlHost, endpoint.URLHost())
		})
	}
}

func TestParseRelayEndpointRejectsURLsAndInvalidPorts(t *testing.T) {
	for _, raw := range []string{"https://relay.example.com", "relay.example.com/path", "relay.example.com:0", "relay.example.com:65536", "relay.example.com:nope"} {
		_, err := ParseRelayEndpoint(raw)
		require.Error(t, err, raw)
	}
}
