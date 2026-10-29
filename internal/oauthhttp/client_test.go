package oauthhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestGetServerMetadata(t *testing.T) {
	const authPath = "/.well-known/oauth-authorization-server"
	cases := []struct {
		name             string
		basePath         string
		scoped           bool
		explicitPath     string
		rootResourcePath string
		wantIssuerPath   string
		wantError        bool
		wantRequests     []string
	}{
		{
			name:         "root-only metadata",
			basePath:     "/mcp",
			wantRequests: []string{protectedResourcePath + "/mcp", protectedResourcePath, authPath},
		},
		{
			name:           "path metadata takes precedence",
			basePath:       "/mcp",
			scoped:         true,
			wantIssuerPath: "/scoped",
			wantRequests:   []string{protectedResourcePath + "/mcp", authPath + "/scoped"},
		},
		{
			name:         "root resource",
			wantRequests: []string{protectedResourcePath, authPath},
		},
		{
			name:         "advertised matching root resource",
			explicitPath: protectedResourcePath,
			wantRequests: []string{protectedResourcePath, authPath},
		},
		{
			name:           "advertised matching scoped resource",
			basePath:       "/mcp",
			scoped:         true,
			explicitPath:   protectedResourcePath + "/mcp",
			wantIssuerPath: "/scoped",
			wantRequests:   []string{protectedResourcePath + "/mcp", authPath + "/scoped"},
		},
		{
			name:             "advertised fallback keeps matching resource",
			basePath:         "/mcp",
			explicitPath:     protectedResourcePath + "/mcp",
			rootResourcePath: "/mcp",
			wantRequests:     []string{protectedResourcePath + "/mcp", protectedResourcePath, authPath},
		},
		{
			name:         "advertised fallback rejects mismatched resource",
			basePath:     "/mcp",
			explicitPath: protectedResourcePath + "/mcp",
			wantError:    true,
			wantRequests: []string{protectedResourcePath + "/mcp", protectedResourcePath},
		},
		{
			name:         "advertised root rejects mismatched resource",
			basePath:     "/mcp",
			explicitPath: protectedResourcePath,
			wantError:    true,
			wantRequests: []string{protectedResourcePath},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan string, 32)
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r.URL.Path
				if r.Method != http.MethodGet {
					t.Errorf("method = %s, want GET", r.Method)
				}
				if r.Header.Get("Accept") != "application/json" || r.Header.Get("MCP-Protocol-Version") != mcp.LATEST_PROTOCOL_VERSION {
					t.Errorf("discovery headers missing: %v", r.Header)
				}
				switch r.URL.Path {
				case protectedResourcePath + "/mcp":
					if !tc.scoped {
						http.NotFound(w, r)
						return
					}
					writeJSON(t, w, map[string]any{
						"resource":              server.URL + "/mcp",
						"authorization_servers": []string{server.URL + "/scoped"},
					})
				case protectedResourcePath:
					writeJSON(t, w, map[string]any{
						"resource":              server.URL + tc.rootResourcePath,
						"authorization_servers": []string{server.URL},
					})
				case authPath, authPath + "/scoped":
					issuer := server.URL + strings.TrimPrefix(r.URL.Path, authPath)
					writeJSON(t, w, map[string]any{
						"issuer":                 issuer,
						"authorization_endpoint": issuer + "/oauth/authorize",
						"token_endpoint":         issuer + "/oauth/token",
					})
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			cfg := transport.OAuthConfig{HTTPClient: NewClient()}
			if tc.explicitPath != "" {
				cfg.ProtectedResourceMetadataURL = server.URL + tc.explicitPath
			}
			handler := transport.NewOAuthHandler(cfg)
			handler.SetBaseURL(server.URL + tc.basePath)
			metadata, err := handler.GetServerMetadata(t.Context())
			if tc.wantError {
				if err == nil {
					t.Fatal("accepted metadata for a different protected resource")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				issuer := server.URL + tc.wantIssuerPath
				if metadata.Issuer != issuer || metadata.AuthorizationEndpoint != issuer+"/oauth/authorize" || metadata.TokenEndpoint != issuer+"/oauth/token" {
					t.Errorf("metadata = %+v, want endpoints from %s", metadata, issuer)
				}
				if metadata.RegistrationEndpoint != "" {
					t.Errorf("registration endpoint = %q, want absent", metadata.RegistrationEndpoint)
				}
				if err := handler.RegisterClient(t.Context(), "test"); err == nil {
					t.Error("registered a client without an advertised registration endpoint")
				}
			}
			assertRequests(t, requests, tc.wantRequests)
		})
	}
}

func TestExpiredTokenRefreshUsesRootMetadata(t *testing.T) {
	requests := make(chan string, 32)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.URL.Path
		switch r.URL.Path {
		case protectedResourcePath + "/mcp":
			http.NotFound(w, r)
		case protectedResourcePath:
			writeJSON(t, w, map[string]any{
				"resource":              server.URL,
				"authorization_servers": []string{server.URL},
			})
		case "/.well-known/oauth-authorization-server":
			writeJSON(t, w, map[string]any{
				"issuer":                 server.URL,
				"authorization_endpoint": server.URL + "/oauth/v2_user/authorize",
				"token_endpoint":         server.URL + "/api/oauth.v2.user.access",
			})
		case "/api/oauth.v2.user.access":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
				http.Error(w, "invalid form", http.StatusBadRequest)
				return
			}
			for key, want := range map[string]string{
				"grant_type":    "refresh_token",
				"refresh_token": "old-refresh",
				"client_id":     "registered-client",
				"client_secret": "registered-secret",
				"resource":      server.URL,
			} {
				if got := r.PostForm.Get(key); got != want {
					t.Errorf("form %s = %q, want %q", key, got, want)
				}
			}
			writeJSON(t, w, map[string]any{
				"access_token":  "new-access",
				"refresh_token": "new-refresh",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := transport.NewMemoryTokenStore()
	if err := store.SaveToken(t.Context(), &transport.Token{
		AccessToken:  "expired-access",
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	handler := transport.NewOAuthHandler(transport.OAuthConfig{
		ClientID:     "registered-client",
		ClientSecret: "registered-secret",
		TokenStore:   store,
		HTTPClient:   NewClient(),
	})
	handler.SetBaseURL(server.URL + "/mcp")
	header, err := handler.GetAuthorizationHeader(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if header != "Bearer new-access" {
		t.Errorf("authorization = %q", header)
	}
	token, err := store.GetToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "new-access" || token.RefreshToken != "new-refresh" || token.IsExpired() {
		t.Errorf("stored token = %+v", token)
	}
	assertRequests(t, requests, []string{
		"GET " + protectedResourcePath + "/mcp",
		"GET " + protectedResourcePath,
		"GET /.well-known/oauth-authorization-server",
		"POST /api/oauth.v2.user.access",
	})
}

func TestFallbackScope(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		path       string
		status     int
		rootStatus int
		fallback   bool
	}{
		{"missing path", "GET", protectedResourcePath + "/mcp", 404, 200, true},
		{"nested path", "GET", protectedResourcePath + "/api/mcp", 404, 200, true},
		{"successful path", "GET", protectedResourcePath + "/mcp", 200, 200, false},
		{"unauthorized path", "GET", protectedResourcePath + "/mcp", 401, 200, false},
		{"forbidden path", "GET", protectedResourcePath + "/mcp", 403, 200, false},
		{"rate-limited path", "GET", protectedResourcePath + "/mcp", 429, 200, false},
		{"failed path", "GET", protectedResourcePath + "/mcp", 500, 200, false},
		{"missing root", "GET", protectedResourcePath, 404, 404, false},
		{"empty suffix", "GET", protectedResourcePath + "/", 404, 200, false},
		{"similar prefix", "GET", protectedResourcePath + "-other/mcp", 404, 200, false},
		{"encoded separator", "GET", protectedResourcePath + "%2Fmcp", 404, 200, false},
		{"authorization metadata", "GET", "/.well-known/oauth-authorization-server/mcp", 404, 200, false},
		{"token request", "POST", "/token", 404, 200, false},
		{"registration request", "POST", "/register", 404, 200, false},
		{"other GET", "GET", "/mcp", 404, 200, false},
		{"POST metadata", "POST", protectedResourcePath + "/mcp", 404, 200, false},
		{"HEAD metadata", "HEAD", protectedResourcePath + "/mcp", 404, 200, false},
		{"fallback also missing", "GET", protectedResourcePath + "/mcp", 404, 404, true},
		{"fallback fails", "GET", protectedResourcePath + "/mcp", 404, 503, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan string, 32)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r.URL.EscapedPath()
				status := tc.status
				if r.URL.Path == protectedResourcePath {
					status = tc.rootStatus
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			req, err := http.NewRequestWithContext(t.Context(), tc.method, server.URL+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			client := NewClient()
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			wantStatus := tc.status
			wantRequests := []string{tc.path}
			if tc.fallback {
				wantStatus = tc.rootStatus
				wantRequests = append(wantRequests, protectedResourcePath)
			}
			if resp.StatusCode != wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, wantStatus)
			}
			assertRequests(t, requests, wantRequests)
		})
	}
}

func TestFallbackPreservesRequestAndClosesBody(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(t.Context(), contextKey{}, "discovery")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com:8443"+protectedResourcePath+"/m%63p?tenant=one#part", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer discovery-token")
	req.Header.Set("MCP-Protocol-Version", mcp.LATEST_PROTOCOL_VERSION)
	originalURL := req.URL.String()
	missingBody := &trackedBody{Reader: strings.NewReader("missing")}
	rootBody := &trackedBody{Reader: strings.NewReader("root")}
	calls := 0
	clientTransport := &rootMetadataTransport{base: roundTripFunc(func(got *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: http.StatusNotFound, Body: missingBody}, nil
		}
		if calls != 2 {
			t.Fatalf("unexpected fallback attempt %d", calls)
		}
		if !missingBody.closed {
			t.Error("replaced body not closed before fallback")
		}
		if got.URL.String() != "https://example.com:8443"+protectedResourcePath {
			t.Errorf("fallback URL = %s", got.URL)
		}
		if got.Context() != ctx || got.Context().Value(contextKey{}) != "discovery" || got.Host != req.Host {
			t.Error("fallback changed request context or host")
		}
		for key, want := range req.Header {
			if !slices.Equal(got.Header.Values(key), want) {
				t.Errorf("fallback header %s = %v, want %v", key, got.Header.Values(key), want)
			}
		}
		got.Header.Set("X-Fallback", "root")
		return &http.Response{StatusCode: http.StatusOK, Body: rootBody}, nil
	})}
	resp, err := clientTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if calls != 2 || resp.Body != rootBody || rootBody.closed {
		t.Errorf("fallback calls = %d, response body = %v", calls, resp.Body)
	}
	if req.URL.String() != originalURL || req.Header.Get("X-Fallback") != "" {
		t.Error("fallback mutated original request")
	}
}

func TestTransportErrorDoesNotFallback(t *testing.T) {
	wantErr := errors.New("connection failed")
	calls := 0
	clientTransport := &rootMetadataTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, wantErr
	})}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.com"+protectedResourcePath+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := clientTransport.RoundTrip(req)
	if resp != nil || !errors.Is(err, wantErr) || calls != 1 {
		t.Fatalf("RoundTrip = (%v, %v), calls = %d", resp, err, calls)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error {
	b.closed = true
	return nil
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}

func assertRequests(t *testing.T, requests <-chan string, want []string) {
	t.Helper()
	var got []string
	for {
		select {
		case request := <-requests:
			got = append(got, request)
		default:
			if !slices.Equal(got, want) {
				t.Errorf("requests = %v, want %v", got, want)
			}
			return
		}
	}
}
