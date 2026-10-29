// Package oauthhttp adds root protected-resource discovery for OAuth clients.
package oauthhttp

import (
	"net/http"
	"strings"
	"time"
)

const protectedResourcePath = "/.well-known/oauth-protected-resource"

// NewClient tries root metadata when path-specific protected-resource metadata is absent.
func NewClient() *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &rootMetadataTransport{base: http.DefaultTransport},
	}
}

type rootMetadataTransport struct {
	base http.RoundTripper
}

func (t *rootMetadataTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || req.Method != http.MethodGet || resp.StatusCode != http.StatusNotFound {
		return resp, err
	}
	path := req.URL.EscapedPath()
	if !strings.HasPrefix(path, protectedResourcePath+"/") || len(path) <= len(protectedResourcePath)+1 {
		return resp, nil
	}

	// mcp-go v1.1.1 skips root PRM discovery after a path-specific 404.
	// Try it once before the SDK falls back to authorization-server discovery.
	resp.Body.Close()
	root := req.Clone(req.Context())
	root.URL.Path = protectedResourcePath
	root.URL.RawPath = ""
	root.URL.RawQuery = ""
	root.URL.ForceQuery = false
	root.URL.Fragment = ""
	root.URL.RawFragment = ""
	return t.base.RoundTrip(root)
}
