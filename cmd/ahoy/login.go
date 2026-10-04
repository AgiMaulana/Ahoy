package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"

	"github.com/AgiMaulana/Ahoy/internal/config"
	"github.com/AgiMaulana/Ahoy/internal/oauthstore"
)

const loginUsage = `Authenticate an OAuth-configured HTTP server in the browser.

Usage:
  ahoy login <name> [-config path] [-port n] [-scope s]...

Flags:
  -port int             localhost callback port (default 53127)
  -scope string         OAuth scope, repeatable
  -client-id string     pre-registered client id (skips dynamic registration)
  -client-secret string client secret for a confidential client
  -timeout duration     how long to wait for the browser (default 5m)
  -config string        path to the config file (default: $AHOY_HOME/config.json, else ~/.ahoy/config.json)

The token is cached under ~/.ahoy/tokens (override with $AHOY_HOME) and reused
by "ahoy serve" on later runs.`

func login(args []string) {
	fs := flag.NewFlagSet("ahoy login", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, loginUsage) }

	var (
		configPath   = fs.String("config", "", "path to the gateway config file")
		port         = fs.Int("port", 53127, "localhost callback port")
		clientID     = fs.String("client-id", "", "pre-registered OAuth client id")
		clientSecret = fs.String("client-secret", "", "OAuth client secret")
		timeout      = fs.Duration("timeout", 5*time.Minute, "how long to wait for the browser")
	)
	var scopes stringList
	fs.Var(&scopes, "scope", "OAuth scope (repeatable)")

	positionals, _, err := parseArgs(fs, args)
	if err != nil {
		fail(err)
	}
	if len(positionals) != 1 {
		fs.Usage()
		os.Exit(2)
	}

	path, err := config.Resolve(*configPath)
	if err != nil {
		fail(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		fail(err)
	}
	key, ok := matchServer(cfg, positionals[0])
	if !ok {
		fail(fmt.Errorf("unknown server %q (configured: %s)", positionals[0], joinNames(cfg)))
	}
	sc := cfg.Servers[key]
	if sc.Transport != config.TransportHTTP {
		fail(fmt.Errorf("server %q is %s; login only applies to http servers", key, sc.Transport))
	}
	if sc.Auth != config.AuthOAuth {
		fail(fmt.Errorf("server %q is not configured for oauth (add \"auth\": \"oauth\" or -auth oauth)", key))
	}

	store, err := oauthstore.New(key)
	if err != nil {
		fail(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	oauthCfg := transport.OAuthConfig{
		ClientID:     *clientID,
		ClientSecret: *clientSecret,
		RedirectURI:  fmt.Sprintf("http://127.0.0.1:%d/callback", *port),
		Scopes:       scopes,
		PKCEEnabled:  true,
		TokenStore:   store,
	}
	// Reuse a client registered on a previous login.
	if oauthCfg.ClientID == "" {
		oauthCfg.ClientID, oauthCfg.ClientSecret = store.ClientCredentials()
	}

	handler := transport.NewOAuthHandler(oauthCfg)
	handler.SetBaseURL(sc.URL)

	if oauthCfg.ClientID == "" {
		fmt.Fprintln(os.Stderr, "registering OAuth client...")
		if err := handler.RegisterClient(ctx, "Ahoy"); err != nil {
			fail(fmt.Errorf("dynamic client registration failed: %w\n\nPass -client-id (and -client-secret) to use a pre-registered client", err))
		}
		if err := store.SaveClientCredentials(handler.GetClientID(), handler.GetClientSecret()); err != nil {
			fail(err)
		}
	}

	state, err := transport.GenerateState()
	if err != nil {
		fail(err)
	}
	verifier, err := transport.GenerateCodeVerifier()
	if err != nil {
		fail(err)
	}
	authURL, err := handler.GetAuthorizationURL(ctx, state, transport.GenerateCodeChallenge(verifier))
	if err != nil {
		fail(err)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		fail(fmt.Errorf("listen for the OAuth callback: %w", err))
	}

	fmt.Printf("Open this URL to authorize:\n\n  %s\n\n", authURL)
	if err := openBrowser(authURL); err != nil {
		fmt.Fprintf(os.Stderr, "could not open a browser automatically: %v\n", err)
	}

	code, err := waitForCallback(ctx, ln, state)
	if err != nil {
		fail(err)
	}
	if err := handler.ProcessAuthorizationResponse(ctx, code, state, verifier); err != nil {
		fail(err)
	}

	fmt.Printf("authorized %q; token saved to %s\n", key, store.Path())
}

// waitForCallback serves a single OAuth redirect on ln and returns the code.
func waitForCallback(ctx context.Context, ln net.Listener, state string) (string, error) {
	defer ln.Close()

	type result struct {
		code string
		err  error
	}
	results := make(chan result, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			http.Error(w, "Authorization failed: "+e, http.StatusBadRequest)
			results <- result{err: fmt.Errorf("authorization failed: %s %s", e, q.Get("error_description"))}
			return
		}
		if got := q.Get("state"); got != state {
			http.Error(w, "State mismatch", http.StatusBadRequest)
			results <- result{err: fmt.Errorf("state mismatch (possible CSRF); code rejected")}
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "Missing code", http.StatusBadRequest)
			results <- result{err: fmt.Errorf("callback did not include a code")}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, callbackPage)
		results <- result{code: code}
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	select {
	case <-ctx.Done():
		return "", fmt.Errorf("timed out waiting for the browser: %w", ctx.Err())
	case res := <-results:
		return res.code, res.err
	}
}

const callbackPage = `<!doctype html><title>Ahoy</title>
<h1>Authorized</h1><p>You can close this tab and return to the terminal.</p>`

// openBrowser best-effort launches the system browser at url.
func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	return exec.Command(cmd, append(args, url)...).Start()
}
