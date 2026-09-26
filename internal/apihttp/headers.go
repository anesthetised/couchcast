package apihttp

import (
	"net/http"
	"strings"
)

// contentSecurityPolicy allows only the bundle's own scripts. Media,
// manifests and the socket are same-origin ("self" covers ws/wss on the
// same host); Shaka plays through MediaSource blob: URLs. Thumbnails come
// from wherever the video site hosts them, hence https: for images.
// Inline styles stay allowed: components set positions and colours with
// style bindings.
var contentSecurityPolicy = strings.Join([]string{
	"default-src 'self'",
	"script-src 'self'",
	"style-src 'self' 'unsafe-inline'",
	"img-src 'self' data: blob: https:",
	"media-src 'self' blob:",
	"connect-src 'self'",
	"worker-src 'self'",
	"manifest-src 'self'",
	"font-src 'self'",
	"object-src 'none'",
	"base-uri 'none'",
	"form-action 'self'",
	"frame-ancestors 'none'",
}, "; ")

// permissionsPolicy turns off device features the app never uses, so an
// injected script could not ask for them either. Fullscreen, autoplay and
// picture-in-picture keep their defaults.
const permissionsPolicy = "camera=(), microphone=(), geolocation=(), payment=(), usb=(), browsing-topics=()"

// securityHeaders sets the browser hardening headers on every response,
// so they hold whether or not a proxy sits in front. HSTS is only sent
// over HTTPS, where browsers honour it.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", permissionsPolicy)
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if isHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// isHTTPS reports whether the client reached us over HTTPS, directly or
// through a TLS-terminating proxy.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
