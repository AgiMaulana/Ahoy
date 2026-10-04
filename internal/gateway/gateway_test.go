package gateway

import (
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
)

func TestIsConnectionError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"transport closed", transport.ErrTransportClosed, true},
		{"session terminated", transport.ErrSessionTerminated, true},
		{"tool failure", errors.New("tool failed"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := isConnectionError(tc.err); got != tc.want {
			t.Errorf("%s: isConnectionError = %v, want %v", tc.name, got, tc.want)
		}
	}
}
