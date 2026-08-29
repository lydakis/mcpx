package oauthclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// LoopbackReceiver owns a temporary loopback callback listener for an
// interactive OAuth login.
type LoopbackReceiver struct {
	listener net.Listener
	server   *http.Server
	result   chan *auth.AuthorizationResult
	errs     chan error
	once     sync.Once
	output   io.Writer
}

// NewLoopbackReceiver binds an unpredictable local port and starts the OAuth
// callback server. It never accepts non-loopback connections.
func NewLoopbackReceiver(output io.Writer) (*LoopbackReceiver, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("opening OAuth callback listener: %w", err)
	}
	receiver := &LoopbackReceiver{
		listener: listener,
		result:   make(chan *auth.AuthorizationResult, 1),
		errs:     make(chan error, 1),
		output:   output,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", receiver.handleCallback)
	receiver.server = &http.Server{Handler: mux}
	go func() {
		if err := receiver.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			receiver.errs <- err
		}
	}()
	return receiver, nil
}

// RedirectURL is the exact loopback URI registered with the authorization server.
func (r *LoopbackReceiver) RedirectURL() string {
	return "http://" + r.listener.Addr().String() + "/callback"
}

// Fetch prints the authorization URL and waits for the redirect. Requiring the
// user to open it prevents an untrusted authorization server from driving the
// system browser through redirects into a local or private network.
func (r *LoopbackReceiver) Fetch(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
	if args == nil || args.URL == "" {
		return nil, fmt.Errorf("authorization URL is missing")
	}
	output := r.output
	if output == nil {
		output = io.Discard
	}
	if _, err := fmt.Fprintf(output, "Open this URL in your browser to authorize mcpx:\n%s\n", args.URL); err != nil {
		return nil, fmt.Errorf("printing authorization URL: %w", err)
	}
	select {
	case result := <-r.result:
		return result, nil
	case err := <-r.errs:
		return nil, fmt.Errorf("OAuth callback server: %w", err)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *LoopbackReceiver) handleCallback(w http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	if oauthErr := query.Get("error"); oauthErr != "" {
		http.Error(w, "Authorization failed. You can close this window.", http.StatusBadRequest)
		r.once.Do(func() {
			r.errs <- fmt.Errorf("authorization server returned %s", oauthErr)
		})
		return
	}
	result := &auth.AuthorizationResult{
		Code:  query.Get("code"),
		State: query.Get("state"),
		Iss:   query.Get("iss"),
	}
	if result.Code == "" {
		http.Error(w, "Missing authorization code. You can close this window.", http.StatusBadRequest)
		r.once.Do(func() { r.errs <- fmt.Errorf("OAuth callback did not include a code") })
		return
	}
	fmt.Fprint(w, "Authentication successful. You can close this window.")
	r.once.Do(func() { r.result <- result })
}

// Close stops the callback server.
func (r *LoopbackReceiver) Close() error {
	if r == nil || r.server == nil {
		return nil
	}
	return r.server.Close()
}
