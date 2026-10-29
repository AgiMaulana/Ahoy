package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AgiMaulana/Ahoy/internal/oauthstore"
)

type cbResult struct {
	code string
	err  error
}

// runCallback starts waitForCallback on a random port and returns the URL to
// hit plus a function that yields its result.
func runCallback(t *testing.T, state string) (string, func() cbResult) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	resCh := make(chan cbResult, 1)
	go func() {
		code, err := waitForCallback(context.Background(), ln, state)
		resCh <- cbResult{code: code, err: err}
	}()
	return "http://" + ln.Addr().String() + "/callback", func() cbResult { return <-resCh }
}

func TestWaitForCallbackSuccess(t *testing.T) {
	url, get := runCallback(t, "abc")
	resp, err := http.Get(url + "?code=XYZ&state=abc")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if r := get(); r.err != nil || r.code != "XYZ" {
		t.Fatalf("result = %+v, want code XYZ", r)
	}
}

func TestWaitForCallbackBadState(t *testing.T) {
	url, get := runCallback(t, "abc")
	resp, err := http.Get(url + "?code=XYZ&state=WRONG")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if r := get(); r.err == nil {
		t.Fatal("want a state-mismatch error")
	}
}

func TestWaitForCallbackErrorParam(t *testing.T) {
	url, get := runCallback(t, "abc")
	resp, err := http.Get(url + "?error=access_denied&error_description=nope&state=abc")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if r := get(); r.err == nil || !strings.Contains(r.err.Error(), "access_denied") {
		t.Fatalf("result = %+v, want access_denied error", r)
	}
}

func TestWaitForCallbackMissingCode(t *testing.T) {
	url, get := runCallback(t, "abc")
	resp, err := http.Get(url + "?state=abc")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if r := get(); r.err == nil {
		t.Fatal("want a missing-code error")
	}
}

func TestWaitForCallbackCancelled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if _, err := waitForCallback(ctx, ln, "s"); err == nil {
		t.Fatal("want a timeout error")
	}
}

func TestLoginOAuthFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI OAuth test in short mode")
	}
	root := moduleRoot(t)
	binDir := t.TempDir()
	cliName, browserName := "ahoy", "xdg-open"
	switch runtime.GOOS {
	case "darwin":
		browserName = "open"
	case "windows":
		cliName, browserName = "ahoy.exe", "rundll32.exe"
	}
	cli := buildBinary(t, root, binDir, "./cmd/ahoy", cliName)
	browserSource := filepath.Join(binDir, "browser.go")
	if err := os.WriteFile(browserSource, []byte(`package main

import (
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(os.Args[len(os.Args)-1])
	if err != nil {
		os.Exit(1)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		os.Exit(1)
	}
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	buildBinary(t, root, binDir, browserSource, browserName)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, preregistered := range []bool{true, false} {
		name := "dynamic"
		if preregistered {
			name = "preregistered"
		}
		t.Run(name, func(t *testing.T) {
			const clientID = "test-client-id"
			const clientSecret = "test-client-secret"
			var registrations, authorizations, exchanges, rootDiscoveries atomic.Int32
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/.well-known/oauth-protected-resource":
					rootDiscoveries.Add(1)
					json.NewEncoder(w).Encode(map[string]any{
						"resource":              srv.URL,
						"authorization_servers": []string{srv.URL},
					})
				case "/.well-known/oauth-authorization-server":
					metadata := map[string]any{
						"issuer":                 srv.URL,
						"authorization_endpoint": srv.URL + "/oauth/v2_user/authorize",
						"token_endpoint":         srv.URL + "/api/oauth.v2.user.access",
					}
					if !preregistered {
						metadata["registration_endpoint"] = srv.URL + "/register"
					}
					json.NewEncoder(w).Encode(metadata)
				case "/register":
					registrations.Add(1)
					if preregistered {
						t.Error("pre-registered login attempted dynamic registration")
						http.NotFound(w, r)
						return
					}
					w.WriteHeader(http.StatusCreated)
					json.NewEncoder(w).Encode(map[string]string{
						"client_id": clientID, "client_secret": clientSecret,
					})
				case "/oauth/v2_user/authorize":
					authorizations.Add(1)
					query := r.URL.Query()
					callback, err := url.Parse(query.Get("redirect_uri"))
					if err != nil || callback.Hostname() != "127.0.0.1" || callback.Path != "/callback" {
						t.Error("authorization did not provide a localhost callback")
						http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
						return
					}
					values := url.Values{"state": {query.Get("state")}}
					if query.Get("client_id") == "denied-client" {
						values.Set("error", "access_denied")
					} else {
						values.Set("code", "code123")
					}
					callback.RawQuery = values.Encode()
					http.Redirect(w, r, callback.String(), http.StatusFound)
				case "/api/oauth.v2.user.access":
					exchanges.Add(1)
					if err := r.ParseForm(); err != nil {
						t.Errorf("parse token request: %v", err)
						http.Error(w, "invalid request", http.StatusBadRequest)
						return
					}
					if r.Form.Get("client_id") == "rejected-client" {
						w.WriteHeader(http.StatusUnauthorized)
						json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
						return
					}
					if r.Method != http.MethodPost || r.Form.Get("grant_type") != "authorization_code" ||
						r.Form.Get("client_id") != clientID || r.Form.Get("client_secret") != clientSecret ||
						r.Form.Get("code") != "code123" || r.Form.Get("code_verifier") == "" ||
						r.Form.Get("resource") != srv.URL {
						t.Error("token exchange omitted the registered credentials, code, PKCE verifier, or root resource")
						http.Error(w, "invalid exchange", http.StatusBadRequest)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{
						"access_token": "test-access-token", "token_type": "Bearer",
						"refresh_token": "test-refresh-token", "expires_in": 3600,
					})
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			home := t.TempDir()
			t.Setenv("AHOY_HOME", home)
			cfg := fmt.Sprintf(`{"servers":{"stub":{"transport":"http","url":%q,"auth":"oauth"}}}`, srv.URL+"/mcp")
			if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(cfg), 0o600); err != nil {
				t.Fatal(err)
			}
			store := oauthstore.NewAt(filepath.Join(home, "tokens", "stub.json"))
			runLogin := func(flags ...string) ([]byte, error) {
				t.Helper()
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				port := ln.Addr().(*net.TCPAddr).Port
				if err := ln.Close(); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				args := []string{"login", "stub", "-port", strconv.Itoa(port), "-timeout", "10s"}
				return exec.CommandContext(ctx, cli, append(args, flags...)...).CombinedOutput()
			}
			assertSaved := func() {
				t.Helper()
				if id, secret := store.ClientCredentials(); id != clientID || secret != clientSecret {
					t.Fatal("registered client credentials were not persisted")
				}
				token, err := store.GetToken(context.Background())
				if err != nil {
					t.Fatalf("read saved token: %v", err)
				}
				if token.AccessToken != "test-access-token" || token.RefreshToken != "test-refresh-token" {
					t.Fatal("access and refresh tokens were not persisted")
				}
			}

			var flags []string
			if preregistered {
				flags = []string{"-client-id", clientID, "-client-secret", clientSecret}
			}
			for attempt := range 2 {
				if output, err := runLogin(flags...); err != nil {
					t.Fatalf("login %d: %v\n%s", attempt+1, err, output)
				}
				assertSaved()
				flags = nil
			}
			if authorizations.Load() != 2 || exchanges.Load() != 2 || rootDiscoveries.Load() != 2 {
				t.Fatal("both logins must discover root metadata and complete an authorization code exchange")
			}
			wantRegistrations := int32(1)
			if preregistered {
				wantRegistrations = 0
			}
			if got := registrations.Load(); got != wantRegistrations {
				t.Fatalf("registration requests = %d, want %d", got, wantRegistrations)
			}

			before, err := os.ReadFile(store.Path())
			if err != nil {
				t.Fatal(err)
			}
			for _, failure := range []struct {
				clientID       string
				authorizations int32
				exchanges      int32
			}{
				{"denied-client", 3, 2},
				{"rejected-client", 4, 3},
			} {
				if output, err := runLogin("-client-id", failure.clientID, "-client-secret", "replacement-secret"); err == nil {
					t.Fatalf("login with %s succeeded unexpectedly:\n%s", failure.clientID, output)
				}
				if authorizations.Load() != failure.authorizations || exchanges.Load() != failure.exchanges {
					t.Fatalf("login with %s did not reach the expected failure", failure.clientID)
				}
				after, err := os.ReadFile(store.Path())
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatalf("login with %s replaced working OAuth state", failure.clientID)
				}
			}
		})
	}
}
