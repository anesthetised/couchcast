package apihttp

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSecurityHeaders(t *testing.T) {
	static := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>couchcast</title>")}}
	env := newTestEnv(t, static)

	// The shell, a client route, the API (including its errors) and the
	// health check all carry the same headers.
	for _, path := range []string{"/", "/r/movie-night", "/api/v1/auth/me", "/api/v1/nope", "/healthz"} {
		rec := env.do(http.MethodGet, path, nil)
		h := rec.Header()
		csp := map[string]string{}
		for _, d := range strings.Split(h.Get("Content-Security-Policy"), "; ") {
			name, value, _ := strings.Cut(d, " ")
			csp[name] = value
		}
		for name, want := range map[string]string{
			// Only the bundle's own scripts: no inline, no eval.
			"script-src":      "'self'",
			"default-src":     "'self'",
			"connect-src":     "'self'",
			"media-src":       "'self' blob:",
			"frame-ancestors": "'none'",
			"object-src":      "'none'",
			"base-uri":        "'none'",
		} {
			if csp[name] != want {
				t.Errorf("%s: CSP %s = %q, want %q", path, name, csp[name], want)
			}
		}
		for k, v := range map[string]string{
			"X-Frame-Options":            "DENY",
			"X-Content-Type-Options":     "nosniff",
			"Referrer-Policy":            "strict-origin-when-cross-origin",
			"Cross-Origin-Opener-Policy": "same-origin",
		} {
			if got := h.Get(k); got != v {
				t.Errorf("%s: %s = %q, want %q", path, k, got, v)
			}
		}
		if pp := h.Get("Permissions-Policy"); !strings.Contains(pp, "camera=()") || strings.Contains(pp, "fullscreen") || strings.Contains(pp, "autoplay") {
			t.Errorf("%s: Permissions-Policy = %q", path, pp)
		}
		// Plain HTTP: browsers ignore HSTS there, so it is not sent.
		if got := h.Get("Strict-Transport-Security"); got != "" {
			t.Errorf("%s: HSTS over http = %q", path, got)
		}
	}

	// Behind a TLS-terminating proxy the site pins HTTPS.
	rec := env.doWith(http.MethodGet, "/", nil, map[string]string{"X-Forwarded-Proto": "https"})
	if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=31536000" {
		t.Errorf("HSTS over https = %q", got)
	}
}
