package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"

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

// TestLoginOAuthFlow drives the exact handler sequence "ahoy login" uses
// (discovery, dynamic registration, PKCE authorization URL, code exchange)
// against a stub OAuth server, asserting the token is persisted.
func TestLoginOAuthFlow(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 srv.URL,
			"authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint":         srv.URL + "/token",
			"registration_endpoint":  srv.URL + "/register",
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"client_id": "cid"})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.Form.Get("code") != "code123" {
			http.Error(w, "unexpected code", http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer", "refresh_token": "rt",
		})
	})

	ctx := context.Background()
	store := oauthstore.NewAt(filepath.Join(t.TempDir(), "srv.json"))
	handler := transport.NewOAuthHandler(transport.OAuthConfig{
		RedirectURI: srv.URL + "/callback",
		PKCEEnabled: true,
		TokenStore:  store,
	})
	handler.SetBaseURL(srv.URL)

	if err := handler.RegisterClient(ctx, "Ahoy"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := store.SaveClientCredentials(handler.GetClientID(), handler.GetClientSecret()); err != nil {
		t.Fatal(err)
	}

	state, _ := transport.GenerateState()
	verifier, _ := transport.GenerateCodeVerifier()
	authURL, err := handler.GetAuthorizationURL(ctx, state, transport.GenerateCodeChallenge(verifier))
	if err != nil {
		t.Fatalf("authorization url: %v", err)
	}
	if !strings.Contains(authURL, "client_id=cid") || !strings.Contains(authURL, "code_challenge=") {
		t.Fatalf("authURL = %q", authURL)
	}

	if err := handler.ProcessAuthorizationResponse(ctx, "code123", state, verifier); err != nil {
		t.Fatalf("exchange: %v", err)
	}
	tok, err := store.GetToken(ctx)
	if err != nil {
		t.Fatalf("token not persisted: %v", err)
	}
	if tok.AccessToken != "at" || tok.RefreshToken != "rt" {
		t.Fatalf("token = %+v", tok)
	}
}
