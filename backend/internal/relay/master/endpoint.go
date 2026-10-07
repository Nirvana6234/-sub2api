package master

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// RelayEndpoint is the public HTTPS endpoint of a relay node. Port is optional
// in the stored value; callers should use PortOr when they need a dial port.
type RelayEndpoint struct {
	Host         string
	Port         int
	PortExplicit bool
}

// ParseRelayEndpoint accepts hostnames, IPv4/IPv6 literals, and an optional
// port. IPv6 addresses with a port must use the usual [addr]:port form.
func ParseRelayEndpoint(raw string) (RelayEndpoint, error) {
	s := strings.TrimSpace(raw)
	if s == "" || strings.ContainsAny(s, "/?#@") || strings.IndexFunc(s, func(r rune) bool { return r <= ' ' }) >= 0 {
		return RelayEndpoint{}, fmt.Errorf("relay endpoint must be a host or host:port")
	}

	var host string
	port := 0
	explicit := false
	if ip := net.ParseIP(s); ip != nil {
		host = ip.String()
	} else if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		// Permit a bracketed IPv6 literal without an explicit port too.
		host = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
		if net.ParseIP(host) == nil {
			return RelayEndpoint{}, fmt.Errorf("invalid IPv6 relay endpoint")
		}
		host = net.ParseIP(host).String()
	} else if strings.Contains(s, ":") {
		var portText string
		var err error
		host, portText, err = net.SplitHostPort(s)
		if err != nil {
			return RelayEndpoint{}, fmt.Errorf("relay endpoint port is invalid: %w", err)
		}
		port, err = strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return RelayEndpoint{}, fmt.Errorf("relay endpoint port must be between 1 and 65535")
		}
		explicit = true
	} else {
		host = s
	}

	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" || net.ParseIP(host) == nil && strings.Contains(host, ":") {
		return RelayEndpoint{}, fmt.Errorf("relay endpoint host is invalid")
	}
	if port == 0 {
		port = 443
	}
	return RelayEndpoint{Host: host, Port: port, PortExplicit: explicit}, nil
}

// NormalizeRelayEndpoint returns the canonical value persisted in relay_nodes.
func NormalizeRelayEndpoint(raw string) (string, error) {
	e, err := ParseRelayEndpoint(raw)
	if err != nil {
		return "", err
	}
	return e.String(), nil
}

// String preserves whether the administrator explicitly supplied a port.
func (e RelayEndpoint) String() string {
	if e.PortExplicit {
		return net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
	}
	return e.Host
}

// URLHost returns the endpoint host in a form suitable for an HTTPS URL.
func (e RelayEndpoint) URLHost() string {
	if e.PortExplicit {
		return net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
	}
	if strings.Contains(e.Host, ":") {
		return "[" + e.Host + "]"
	}
	return e.Host
}

func (e RelayEndpoint) PortOr(defaultPort int) int {
	if e.PortExplicit {
		return e.Port
	}
	return defaultPort
}
