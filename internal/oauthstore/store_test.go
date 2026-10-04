package oauthstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
)

func TestGetTokenMissing(t *testing.T) {
	s := NewAt(filepath.Join(t.TempDir(), "s.json"))
	if _, err := s.GetToken(context.Background()); !errors.Is(err, transport.ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

func TestTokenRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	s := NewAt(path)
	tok := &transport.Token{AccessToken: "at", RefreshToken: "rt", TokenType: "Bearer"}
	if err := s.SaveToken(context.Background(), tok); err != nil {
		t.Fatal(err)
	}

	// A fresh store over the same path must read the saved token back.
	got, err := NewAt(path).GetToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "at" || got.RefreshToken != "rt" {
		t.Fatalf("got %+v", got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 600", perm)
	}
}

func TestClientCredentialsSurviveTokenSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	s := NewAt(path)
	if id, _ := s.ClientCredentials(); id != "" {
		t.Fatalf("id = %q, want empty", id)
	}
	if err := s.SaveClientCredentials("cid", "secret"); err != nil {
		t.Fatal(err)
	}
	// Saving a token afterwards must not clobber the stored credentials.
	if err := s.SaveToken(context.Background(), &transport.Token{AccessToken: "at"}); err != nil {
		t.Fatal(err)
	}

	id, secret := NewAt(path).ClientCredentials()
	if id != "cid" || secret != "secret" {
		t.Fatalf("creds = %q/%q, want cid/secret", id, secret)
	}
	got, err := NewAt(path).GetToken(context.Background())
	if err != nil || got.AccessToken != "at" {
		t.Fatalf("token = %+v, err = %v", got, err)
	}
}

func TestCancelledContext(t *testing.T) {
	s := NewAt(filepath.Join(t.TempDir(), "s.json"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.GetToken(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("GetToken err = %v, want context.Canceled", err)
	}
	if err := s.SaveToken(ctx, &transport.Token{}); !errors.Is(err, context.Canceled) {
		t.Errorf("SaveToken err = %v, want context.Canceled", err)
	}
}

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"github":  "github",
		"../evil": ".._evil",
		"a b/c":   "a_b_c",
		"":        "server",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
