// Package safehttp provides outbound HTTP clients for URLs selected by remote
// servers. It prevents public endpoints from pivoting requests into local or
// private networks while preserving explicitly configured local development.
package safehttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// RedirectMode controls whether a client may follow redirects to a new origin.
type RedirectMode uint8

const (
	// PublicRedirects permits redirects whose destinations satisfy the policy.
	PublicRedirects RedirectMode = iota
	// SameOriginRedirects additionally requires every redirect to retain the
	// original request's scheme, host, and effective port.
	SameOriginRedirects
)

type destinationClass uint8

const (
	destinationPublic destinationClass = iota
	destinationLoopback
	destinationPrivate
	destinationLinkLocal
	destinationSpecial
)

type resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

// Policy binds remotely selected restricted destinations to an explicitly
// configured URL. Public destinations remain available in every policy.
type Policy struct {
	allowLoopback     bool
	restrictedHost    string
	restrictedAddress map[netip.Addr]struct{}
	resolver          resolver
	dial              dialContextFunc
}

// NewPolicy constructs a destination policy from an operator-selected URL.
// The selected hostname may resolve into a private network, but that permission
// does not extend to server-discovered hostnames. Explicit localhost opts into
// loopback; another restricted IP opts into that exact address only.
func NewPolicy(trustedURL string) (*Policy, error) {
	u, err := parseHTTPURL(trustedURL)
	if err != nil {
		return nil, err
	}
	policy := &Policy{restrictedAddress: make(map[netip.Addr]struct{})}
	host := canonicalHostname(u.Hostname())
	if isLocalhostName(host) {
		policy.allowLoopback = true
	} else if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		class := classifyAddress(addr)
		if class == destinationSpecial {
			return nil, fmt.Errorf("trusted URL uses unsupported address %s", addr)
		}
		switch class {
		case destinationLoopback:
			policy.allowLoopback = true
		case destinationPrivate, destinationLinkLocal:
			policy.restrictedAddress[addr] = struct{}{}
		}
	} else {
		policy.restrictedHost = host
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	policy.resolver = net.DefaultResolver
	policy.dial = dialer.DialContext
	return policy, nil
}

// Client returns an HTTP client whose redirects and connections are checked by
// the policy. Environment proxies are intentionally disabled because a proxy
// would resolve the final destination outside this process's validated dial.
func (p *Policy) Client(timeout time.Duration, redirects RedirectMode) *http.Client {
	client := &http.Client{Transport: p.Transport(), Timeout: timeout}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if redirects == SameOriginRedirects && len(via) > 0 && !sameHTTPOrigin(via[0].URL, req.URL) {
			return fmt.Errorf("refusing cross-origin HTTP redirect")
		}
		return nil
	}
	return client
}

// Transport returns a proxy-free transport whose dialer validates and pins the
// destination address selected for each new connection.
func (p *Policy) Transport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = p.dialContext
	return transport
}

// ValidateURL checks a URL before handing it to an external user agent such as
// a browser. HTTP clients must still use Client so validation and dialing share
// the same resolved address.
func (p *Policy) ValidateURL(ctx context.Context, rawURL string) error {
	u, err := parseHTTPURL(rawURL)
	if err != nil {
		return err
	}
	_, err = p.resolveAllowed(ctx, u.Hostname())
	return err
}

func (p *Policy) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("parsing outbound address %q: %w", address, err)
	}
	addresses, err := p.resolveAllowed(ctx, host)
	if err != nil {
		return nil, err
	}
	var dialErr error
	for _, addr := range addresses {
		target := net.JoinHostPort(addr.String(), port)
		conn, err := p.dial(ctx, network, target)
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	return nil, fmt.Errorf("dialing validated outbound destination: %w", dialErr)
}

func (p *Policy) resolveAllowed(ctx context.Context, host string) ([]netip.Addr, error) {
	host = canonicalHostname(host)
	var addresses []netip.Addr
	if isLocalhostName(host) {
		resolved, err := p.resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("resolving outbound destination %q: %w", host, err)
		}
		addresses = resolved
	} else if addr, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{addr}
	} else {
		resolved, err := p.resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("resolving outbound destination %q: %w", host, err)
		}
		addresses = resolved
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("outbound destination %q resolved to no addresses", host)
	}

	validated := make([]netip.Addr, 0, len(addresses))
	var answerClass destinationClass
	for _, original := range addresses {
		addr := original.Unmap()
		class := classifyAddress(addr)
		if len(validated) > 0 && class != answerClass {
			return nil, fmt.Errorf("refusing outbound destination %q with mixed network-class answers", host)
		}
		allowed := class == destinationPublic
		if class == destinationLoopback {
			allowed = p.allowLoopback
		} else if class == destinationPrivate || class == destinationLinkLocal {
			_, addressAllowed := p.restrictedAddress[addr]
			allowed = addressAllowed || host == p.restrictedHost
		}
		if !allowed {
			return nil, fmt.Errorf("refusing outbound destination %q at restricted address %s", host, addr)
		}
		answerClass = class
		validated = append(validated, addr)
	}
	return validated, nil
}

func parseHTTPURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parsing outbound URL: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return nil, fmt.Errorf("outbound URL must use http or https")
	}
	if strings.TrimSpace(u.Hostname()) == "" {
		return nil, fmt.Errorf("outbound URL must include a host")
	}
	return u, nil
}

func classifyAddress(addr netip.Addr) destinationClass {
	addr = addr.Unmap()
	switch {
	case addr.IsLoopback():
		return destinationLoopback
	case addr.IsPrivate() || carrierGradeNAT.Contains(addr):
		return destinationPrivate
	case addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast():
		return destinationLinkLocal
	case isSpecialUseAddress(addr) || !addr.IsGlobalUnicast() || addr.IsUnspecified() || addr.IsMulticast():
		return destinationSpecial
	default:
		return destinationPublic
	}
}

var carrierGradeNAT = netip.MustParsePrefix("100.64.0.0/10")

var specialUsePrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.31.196.0/24"),
	netip.MustParsePrefix("192.52.193.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.175.48.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:20::/28"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
	netip.MustParsePrefix("fec0::/10"),
}

func isSpecialUseAddress(addr netip.Addr) bool {
	for _, prefix := range specialUsePrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func sameHTTPOrigin(left, right *url.URL) bool {
	if left == nil || right == nil || !strings.EqualFold(left.Scheme, right.Scheme) {
		return false
	}
	if canonicalHostname(left.Hostname()) != canonicalHostname(right.Hostname()) {
		return false
	}
	return effectivePort(left) == effectivePort(right)
}

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	if strings.EqualFold(u.Scheme, "http") {
		return "80"
	}
	return ""
}

func canonicalHostname(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

func isLocalhostName(host string) bool {
	return canonicalHostname(host) == "localhost"
}
