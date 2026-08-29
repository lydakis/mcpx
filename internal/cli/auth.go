package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lydakis/mcpx/internal/cache"
	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcppool"
	"github.com/lydakis/mcpx/internal/oauthclient"
)

const oauthLoginTimeout = 5 * time.Minute

var oauthStoreFn = oauthclient.NewKeyringStore

var verifyHTTPAuthorizationFn = mcppool.VerifyHTTPAuthorization

func maybeHandleAuthCommand(args []string, cfg *config.Config, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 || args[0] != "auth" {
		return false, 0
	}
	if utilityCommandDeferredToServer(cfg, "auth") {
		return false, 0
	}
	return true, runAuthCommand(args[1:], cfg, stdout, stderr)
}

func runAuthCommand(args []string, cfg *config.Config, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printAuthHelp(stdout)
		return ipc.ExitOK
	}
	if len(args) != 2 {
		fmt.Fprintln(stderr, "mcpx: auth: expected <login|logout|status> <server>")
		printAuthHelp(stderr)
		return ipc.ExitUsageErr
	}
	server := strings.TrimSpace(args[1])
	scfg, ok := cfg.Servers[server]
	if !ok {
		fmt.Fprintf(stderr, "mcpx: auth: unknown server: %s\n", server)
		return ipc.ExitUsageErr
	}
	if !scfg.IsHTTP() || !scfg.OAuth {
		fmt.Fprintf(stderr, "mcpx: auth: server %q is not an OAuth-enabled HTTP server\n", server)
		return ipc.ExitUsageErr
	}
	if scfg.HasAuthorizationHeader() {
		fmt.Fprintf(stderr, "mcpx: auth: server %q uses an explicit Authorization header; OAuth session commands are inactive\n", server)
		return ipc.ExitUsageErr
	}

	switch args[0] {
	case "login":
		return runAuthLogin(server, scfg, stdout, stderr)
	case "logout":
		key := oauthclient.CredentialKey(scfg.URL)
		store := oauthStoreFn()
		transition, err := cache.BeginCredentialTransition()
		if err != nil {
			if transition != "" {
				_ = cache.AbortCredentialTransition(transition)
			}
			fmt.Fprintf(stderr, "mcpx: auth logout: disabling response cache: %v\n", err)
			return ipc.ExitInternal
		}
		mayBePrepared, err := prepareDaemonCredentialTransition(server, transition)
		if err != nil {
			if rollbackErr := rollbackUnchangedCredentialTransition(server, transition, mayBePrepared); rollbackErr != nil {
				err = errors.Join(err, fmt.Errorf("rolling back credential transition: %w", rollbackErr))
			}
			fmt.Fprintf(stderr, "mcpx: auth logout: credentials unchanged because daemon quiescence failed: %v\n", err)
			return ipc.ExitInternal
		}
		if err := store.Delete(key); err != nil {
			if cleanupErr := reloadDaemonServer(server, transition); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("releasing credential transition: %w", cleanupErr))
			}
			fmt.Fprintf(stderr, "mcpx: auth logout: %v\n", err)
			return ipc.ExitInternal
		}
		if err := reloadDaemonServer(server, transition); err != nil {
			fmt.Fprintf(stderr, "mcpx: auth logout: credentials removed, but daemon invalidation failed: %v\n", err)
			return ipc.ExitInternal
		}
		fmt.Fprintf(stdout, "Removed OAuth session for %q\n", server)
		return ipc.ExitOK
	case "status":
		session, err := oauthStoreFn().Load(oauthclient.CredentialKey(scfg.URL))
		if errors.Is(err, oauthclient.ErrNotFound) {
			fmt.Fprintf(stdout, "%s: not authenticated\n", server)
			return ipc.ExitToolErr
		}
		if err != nil {
			fmt.Fprintf(stderr, "mcpx: auth status: %v\n", err)
			return ipc.ExitInternal
		}
		if !oauthclient.SessionScopesCompatible(session, scfg.OAuthScopes) {
			fmt.Fprintf(stdout, "%s: not authenticated (configured OAuth scopes differ; run mcpx auth logout, then mcpx auth login)\n", server)
			return ipc.ExitToolErr
		}
		if !oauthclient.SessionHasIssuerBinding(session) {
			fmt.Fprintf(stdout, "%s: not authenticated (stored OAuth session has no issuer binding; run mcpx auth logout %s, then mcpx auth login %s)\n", server, server, server)
			return ipc.ExitToolErr
		}
		if !oauthclient.SessionHasUsableToken(session) {
			fmt.Fprintf(stdout, "%s: not authenticated (stored OAuth grant is expired or incomplete; run mcpx auth login %s)\n", server, server)
			return ipc.ExitToolErr
		}
		if session.Token.Expiry.IsZero() {
			fmt.Fprintf(stdout, "%s: authenticated (expiry not provided)\n", server)
		} else {
			fmt.Fprintf(stdout, "%s: authenticated (access token expires %s)\n", server, session.Token.Expiry.UTC().Format(time.RFC3339))
		}
		return ipc.ExitOK
	default:
		fmt.Fprintf(stderr, "mcpx: auth: unknown subcommand: %s\n", args[0])
		printAuthHelp(stderr)
		return ipc.ExitUsageErr
	}
}

func runAuthLogin(server string, scfg config.ServerConfig, stdout, stderr io.Writer) int {
	receiver, err := oauthclient.NewLoopbackReceiver(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: auth login: %v\n", err)
		return ipc.ExitInternal
	}
	defer receiver.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), oauthLoginTimeout)
	defer cancel()
	store := oauthStoreFn()
	key := oauthclient.CredentialKey(scfg.URL)
	handler, err := oauthclient.NewHandler(ctx, oauthclient.HandlerOptions{
		ServerURL:         scfg.URL,
		RedirectURL:       receiver.RedirectURL(),
		Fetcher:           receiver.Fetch,
		Store:             store,
		Scopes:            scfg.OAuthScopes,
		ClientMetadataURL: scfg.OAuthClientMetadataURL,
		AllowLogin:        true,
	})
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: auth login: %v\n", err)
		return ipc.ExitInternal
	}
	transition, err := cache.BeginCredentialTransition()
	if err != nil {
		if transition != "" {
			_ = cache.AbortCredentialTransition(transition)
		}
		fmt.Fprintf(stderr, "mcpx: auth login: disabling response cache: %v\n", err)
		return ipc.ExitInternal
	}
	mayBePrepared, err := prepareDaemonCredentialTransition(server, transition)
	if err != nil {
		if rollbackErr := rollbackUnchangedCredentialTransition(server, transition, mayBePrepared); rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("rolling back credential transition: %w", rollbackErr))
		}
		fmt.Fprintf(stderr, "mcpx: auth login: credentials unchanged because daemon quiescence failed: %v\n", err)
		return ipc.ExitInternal
	}
	fmt.Fprintf(stderr, "mcpx: waiting for browser authorization for %q\n", server)
	if err := verifyHTTPAuthorizationFn(ctx, scfg, handler); err != nil {
		return finishFailedAuthLogin(server, transition, err, ipc.ExitToolErr, stderr)
	}
	session, err := store.Load(key)
	if errors.Is(err, oauthclient.ErrNotFound) {
		return finishFailedAuthLogin(server, transition, errors.New("server did not request authorization; no OAuth session was stored"), ipc.ExitToolErr, stderr)
	}
	if err != nil {
		return finishFailedAuthLogin(server, transition, fmt.Errorf("verifying stored session: %w", err), ipc.ExitInternal, stderr)
	}
	if !oauthclient.SessionHasIssuerBinding(session) {
		return finishFailedAuthLogin(server, transition, errors.New("stored OAuth session has no authorization-server issuer binding"), ipc.ExitToolErr, stderr)
	}
	if !oauthclient.SessionHasUsableToken(session) {
		return finishFailedAuthLogin(server, transition, errors.New("stored OAuth grant is expired or incomplete"), ipc.ExitToolErr, stderr)
	}
	if err := reloadDaemonServer(server, transition); err != nil {
		fmt.Fprintf(stderr, "mcpx: auth login: credentials stored, but daemon invalidation failed: %v\n", err)
		return ipc.ExitInternal
	}
	fmt.Fprintf(stdout, "Authenticated %q; credentials stored in the OS credential store\n", server)
	return ipc.ExitOK
}

func prepareDaemonCredentialTransition(server, transition string) (bool, error) {
	return sendDaemonCredentialTransition(&ipc.Request{
		Type:       "prepare_credential_transition",
		Server:     server,
		Transition: transition,
		CWD:        callerWorkingDirectory(),
	})
}

func reloadDaemonServer(server, transition string) error {
	req := &ipc.Request{
		Type:       "reload_server",
		Server:     server,
		Transition: transition,
		CWD:        callerWorkingDirectory(),
	}
	mayBeApplied, err := sendDaemonCredentialTransition(req)
	if err != nil && mayBeApplied {
		_, retryErr := sendDaemonCredentialTransition(req)
		if retryErr == nil {
			err = nil
		} else {
			err = errors.Join(err, fmt.Errorf("retrying idempotent daemon reload: %w", retryErr))
		}
	}
	if err != nil {
		return err
	}
	return nil
}

func finishFailedAuthLogin(server, transition string, cause error, exitCode int, stderr io.Writer) int {
	if cleanupErr := reloadDaemonServer(server, transition); cleanupErr != nil {
		cause = errors.Join(cause, fmt.Errorf("releasing credential transition: %w", cleanupErr))
		exitCode = ipc.ExitInternal
	}
	fmt.Fprintf(stderr, "mcpx: auth login: %v\n", cause)
	return exitCode
}

func rollbackUnchangedCredentialTransition(server, transition string, mayBePrepared bool) error {
	if mayBePrepared {
		return reloadDaemonServer(server, transition)
	}
	return cache.AbortCredentialTransition(transition)
}

func sendDaemonCredentialTransition(req *ipc.Request) (bool, error) {
	nonce, err := spawnOrConnectFn()
	if err != nil {
		return false, err
	}
	client := newDaemonClient(ipc.SocketPath(), nonce)
	resp, err := client.Send(req)
	if err != nil {
		return true, err
	}
	if resp == nil {
		return true, fmt.Errorf("empty daemon response")
	}
	if resp.ExitCode != ipc.ExitOK {
		if strings.TrimSpace(resp.Stderr) != "" {
			return false, errors.New(resp.Stderr)
		}
		return false, fmt.Errorf("daemon credential transition failed with exit code %d", resp.ExitCode)
	}
	return true, nil
}

func printAuthHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  mcpx auth login <server>")
	fmt.Fprintln(out, "  mcpx auth status <server>")
	fmt.Fprintln(out, "  mcpx auth logout <server>")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "OAuth sessions are stored in the operating system credential store.")
}
