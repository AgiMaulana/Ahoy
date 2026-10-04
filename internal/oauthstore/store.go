// Package oauthstore persists OAuth state for HTTP MCP servers: the access and
// refresh tokens, plus the client credentials from dynamic registration. State
// lives in one JSON file per server so "ahoy login" records it and "ahoy serve"
// reuses it on the next run.
package oauthstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/client/transport"

	"github.com/AgiMaulana/Ahoy/internal/appdir"
)

// Store is a file-backed transport.TokenStore for a single server.
type Store struct {
	path string
	mu   sync.Mutex
}

type fileData struct {
	ClientID     string           `json:"client_id,omitempty"`
	ClientSecret string           `json:"client_secret,omitempty"`
	Token        *transport.Token `json:"token,omitempty"`
}

// New returns the store for a server. The name is sanitized so it can never
// escape the token directory.
func New(server string) (*Store, error) {
	dir, err := DefaultDir()
	if err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(dir, sanitize(server)+".json")}, nil
}

// NewAt returns a store backed by an explicit path (used by tests).
func NewAt(path string) *Store { return &Store{path: path} }

// DefaultDir is $AHOY_HOME/tokens, or ~/.ahoy/tokens when AHOY_HOME is unset.
func DefaultDir() (string, error) {
	base, err := appdir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "tokens"), nil
}

// Path returns the file backing this store.
func (s *Store) Path() string { return s.path }

// GetToken implements transport.TokenStore.
func (s *Store) GetToken(ctx context.Context) (*transport.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.read()
	if err != nil {
		return nil, err
	}
	if d.Token == nil {
		return nil, transport.ErrNoToken
	}
	return d.Token, nil
}

// SaveToken implements transport.TokenStore.
func (s *Store) SaveToken(ctx context.Context, token *transport.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.read()
	if err != nil {
		return err
	}
	d.Token = token
	return s.write(d)
}

// ClientCredentials returns the registered client id and secret, if any.
func (s *Store) ClientCredentials() (id, secret string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.read()
	if err != nil {
		return "", ""
	}
	return d.ClientID, d.ClientSecret
}

// SaveClientCredentials records the result of dynamic client registration.
func (s *Store) SaveClientCredentials(id, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.read()
	if err != nil {
		return err
	}
	d.ClientID, d.ClientSecret = id, secret
	return s.write(d)
}

func (s *Store) read() (fileData, error) {
	var d fileData
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return d, nil
		}
		return d, fmt.Errorf("read token file %s: %w", s.path, err)
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return d, fmt.Errorf("parse token file %s: %w", s.path, err)
	}
	return d, nil
}

func (s *Store) write(d fileData) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("encode token file: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".token-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp token file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write token file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close token file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace token file %s: %w", s.path, err)
	}
	return nil
}

// sanitize keeps a server name usable as a file name: anything outside
// [A-Za-z0-9._-] becomes "_".
func sanitize(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "server"
	}
	return b.String()
}
