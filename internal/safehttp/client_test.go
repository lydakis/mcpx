package safehttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type staticResolver struct {
	addresses []netip.Addr
	calls     atomic.Int32
}

func (r *staticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.calls.Add(1)
	return append([]netip.Addr(nil), r.addresses...), nil
}

func TestPublicPolicyRejectsRestrictedAndMixedAnswers(t *testing.T) {
	for _, addresses := range [][]netip.Addr{
		{netip.MustParseAddr("127.0.0.1")},
		{netip.MustParseAddr("10.0.0.1")},
		{netip.MustParseAddr("169.254.1.1")},
		{netip.MustParseAddr("::ffff:127.0.0.1")},
		{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("127.0.0.1")},
	} {
		policy, err := NewPolicy("https://example.com/mcp")
		if err != nil {
			t.Fatal(err)
		}
		policy.resolver = &staticResolver{addresses: addresses}
		if err := policy.ValidateURL(context.Background(), "https://controlled.example/metadata"); err == nil {
			t.Fatalf("ValidateURL() accepted restricted addresses %v", addresses)
		}
	}
}

func TestPublicPolicyRejectsReservedAddresses(t *testing.T) {
	for _, raw := range []string{
		"198.18.0.1",
		"240.0.0.1",
		"192.0.2.1",
		"198.51.100.1",
		"203.0.113.1",
		"fec0::1",
		"2001:db8::1",
	} {
		t.Run(raw, func(t *testing.T) {
			policy, err := NewPolicy("https://example.com/mcp")
			if err != nil {
				t.Fatal(err)
			}
			var dialed atomic.Bool
			policy.resolver = &staticResolver{addresses: []netip.Addr{netip.MustParseAddr(raw)}}
			policy.dial = func(context.Context, string, string) (net.Conn, error) {
				dialed.Store(true)
				return nil, nil
			}
			if _, err := policy.dialContext(context.Background(), "tcp", "controlled.example:443"); err == nil {
				t.Fatal("dialContext() accepted a reserved destination")
			}
			if dialed.Load() {
				t.Fatal("dialContext() attempted a connection to a reserved destination")
			}
		})
	}
}

func TestPrivatePolicyOnlyAllowsSelectedAddress(t *testing.T) {
	policy, err := NewPolicy("https://10.0.0.1/mcp")
	if err != nil {
		t.Fatal(err)
	}
	policy.resolver = &staticResolver{addresses: []netip.Addr{netip.MustParseAddr("192.168.1.20")}}
	if err := policy.ValidateURL(context.Background(), "https://authorization.example/metadata"); err == nil {
		t.Fatal("ValidateURL() allowed a different private destination")
	}
}

func TestConfiguredHostnameMayResolvePrivateWithoutAuthorizingOtherHosts(t *testing.T) {
	policy, err := NewPolicy("https://mcp.internal/mcp")
	if err != nil {
		t.Fatal(err)
	}
	policy.resolver = &staticResolver{addresses: []netip.Addr{netip.MustParseAddr("10.0.0.42")}}
	if err := policy.ValidateURL(context.Background(), "https://mcp.internal/mcp"); err != nil {
		t.Fatalf("ValidateURL(configured private hostname) error = %v", err)
	}
	if err := policy.ValidateURL(context.Background(), "https://metadata.internal/oauth"); err == nil {
		t.Fatal("ValidateURL() allowed a different private hostname")
	}
}

func TestLocalhostPolicySupportsIPv6Loopback(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})}
	go server.Serve(listener) //nolint:errcheck
	defer server.Close()      //nolint:errcheck

	port := listener.Addr().(*net.TCPAddr).Port
	trusted := (&url.URL{Scheme: "http", Host: net.JoinHostPort("localhost", fmt.Sprint(port)), Path: "/mcp"}).String()
	policy, err := NewPolicy(trusted)
	if err != nil {
		t.Fatal(err)
	}
	client := policy.Client(time.Second, SameOriginRedirects)
	resp, err := client.Get(trusted)
	if err != nil {
		t.Fatalf("Get(localhost IPv6) error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestPolicyDialsTheValidatedAddressWithoutASecondLookup(t *testing.T) {
	policy, err := NewPolicy("https://example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	resolver := &staticResolver{addresses: []netip.Addr{netip.MustParseAddr("93.184.216.34")}}
	policy.resolver = resolver
	var dialed string
	policy.dial = func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = address
		client, server := net.Pipe()
		_ = server.Close()
		return client, nil
	}
	conn, err := policy.dialContext(context.Background(), "tcp", "controlled.example:443")
	if err != nil {
		t.Fatalf("dialContext() error = %v", err)
	}
	_ = conn.Close()
	if dialed != "93.184.216.34:443" {
		t.Fatalf("dialed address = %q, want validated IP", dialed)
	}
	if resolver.calls.Load() != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls.Load())
	}
}

func TestSameOriginClientRejectsCrossOriginRedirect(t *testing.T) {
	var destinationHits atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		destinationHits.Add(1)
	}))
	defer destination.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	policy, err := NewPolicy(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = policy.Client(0, SameOriginRedirects).Post(source.URL, "application/json", strings.NewReader(`{}`))
	if err == nil || !strings.Contains(err.Error(), "cross-origin") {
		t.Fatalf("Post() error = %v, want cross-origin rejection", err)
	}
	if destinationHits.Load() != 0 {
		t.Fatalf("destination hits = %d, want 0", destinationHits.Load())
	}
}

func TestSameOriginClientAllowsPathRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			w.Header().Set("Location", "/final")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	policy, err := NewPolicy(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := policy.Client(0, SameOriginRedirects).Post(server.URL+"/start", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestLocalPolicyPreservesLoopbackOAuthDevelopment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	policy, err := NewPolicy("http://localhost/mcp")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := policy.Client(0, PublicRedirects).Get(server.URL)
	if err != nil {
		t.Fatalf("Get(loopback) error = %v", err)
	}
	_ = resp.Body.Close()
}
