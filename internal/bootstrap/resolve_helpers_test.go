package bootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
)

func TestHTTPStatusErrorErrorStringHandlesNilReceiver(t *testing.T) {
	var nilErr *httpStatusError
	if got := nilErr.Error(); got != "" {
		t.Fatalf("(*httpStatusError)(nil).Error() = %q, want empty", got)
	}

	err := &httpStatusError{statusCode: http.StatusForbidden}
	if got := err.Error(); got != "unexpected HTTP status 403" {
		t.Fatalf("httpStatusError.Error() = %q, want %q", got, "unexpected HTTP status 403")
	}
}

func TestResolveErrorMethodsAndSourceAccessWrapper(t *testing.T) {
	var nilErr *resolveError
	if got := nilErr.Error(); got != "" {
		t.Fatalf("(*resolveError)(nil).Error() = %q, want empty", got)
	}
	if unwrapped := nilErr.Unwrap(); unwrapped != nil {
		t.Fatalf("(*resolveError)(nil).Unwrap() = %v, want nil", unwrapped)
	}

	inner := errors.New("boom")
	typed := &resolveError{kind: resolveErrorSourceAccess, err: inner}
	if got := typed.Error(); got != "boom" {
		t.Fatalf("resolveError.Error() = %q, want %q", got, "boom")
	}
	if unwrapped := typed.Unwrap(); !errors.Is(unwrapped, inner) {
		t.Fatalf("resolveError.Unwrap() = %v, want wrapped boom error", unwrapped)
	}

	if got := wrapResolveSourceAccessError(nil); got != nil {
		t.Fatalf("wrapResolveSourceAccessError(nil) = %v, want nil", got)
	}
	if got := wrapResolveSourceAccessError(inner); !IsSourceAccessError(got) {
		t.Fatalf("wrapResolveSourceAccessError(inner) is source access = false, want true")
	}
}

func TestFetchSourceURLSuccessAndHTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			_, _ = w.Write([]byte(`{"mcpServers":{"demo":{"command":"echo"}}}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("  bad request body  "))
	}))
	defer srv.Close()

	okBody, err := fetchSourceURL(context.Background(), srv.URL+"/ok")
	if err != nil {
		t.Fatalf("fetchSourceURL(ok) error = %v, want nil", err)
	}
	if got := string(okBody); !strings.Contains(got, `"mcpServers"`) {
		t.Fatalf("fetchSourceURL(ok) body = %q, want manifest content", got)
	}

	_, err = fetchSourceURL(context.Background(), srv.URL+"/bad")
	if err == nil {
		t.Fatal("fetchSourceURL(status>=400) error = nil, want non-nil")
	}
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("fetchSourceURL(status>=400) error = %v, want *httpStatusError", err)
	}
	if statusErr.statusCode != http.StatusBadRequest {
		t.Fatalf("statusCode = %d, want %d", statusErr.statusCode, http.StatusBadRequest)
	}
	if statusErr.body != "bad request body" {
		t.Fatalf("status body = %q, want %q", statusErr.body, "bad request body")
	}
}

func TestSortedNamesReturnsSortedOrder(t *testing.T) {
	got := sortedNames(map[string]config.ServerConfig{
		"zeta":  {},
		"alpha": {},
		"beta":  {},
	})
	want := []string{"alpha", "beta", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortedNames() = %#v, want %#v", got, want)
	}
}
