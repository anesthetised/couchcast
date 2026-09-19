package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/anesthetised/toolkit/di"

	"github.com/anesthetised/couchcast/internal/entity"
)

// UserFrom returns the authenticated user stored in the context, or nil.
func UserFrom(ctx context.Context) *entity.User {
	u, _ := di.Get[*entity.User](ctx)
	return u
}

// WithUser stores the user in the context. Exposed for tests and for the
// WebSocket upgrade path, which resolves the session itself.
func WithUser(ctx context.Context, u *entity.User) context.Context {
	return di.Put(ctx, u)
}

// Middleware resolves the session cookie on every request and attaches the
// user to the context. Anonymous requests pass through; banned accounts are
// rejected outright.
func (s *Sessions) Middleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, err := s.Resolve(r.Context(), r)
			switch {
			case errors.Is(err, ErrBanned):
				http.SetCookie(w, s.clearedCookie())
				writeError(w, http.StatusForbidden, "account is banned")
				return
			case err != nil:
				logger.ErrorContext(r.Context(), "resolve session", "error", err)
				writeError(w, http.StatusInternalServerError, "internal error")
				return
			}

			if user != nil {
				r = r.WithContext(WithUser(r.Context(), user))
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireUser rejects anonymous requests with 401.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserFrom(r.Context()) == nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin rejects requests from anyone but site administrators.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFrom(r.Context())
		switch {
		case u == nil:
			writeError(w, http.StatusUnauthorized, "authentication required")
		case !u.IsAdmin():
			writeError(w, http.StatusForbidden, "administrator role required")
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// CheckOrigin blocks cross-site state-changing requests. Cookies are
// SameSite=Lax, which already stops cross-site POSTs in modern browsers;
// this is the defence in depth for older ones. Requests without browser
// headers (curl, tests) pass: they carry no ambient credentials.
func CheckOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) || sameOrigin(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusForbidden, "cross-origin request rejected")
	})
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin compares the Origin header (falling back to Referer and the
// Fetch Metadata headers) against the request host.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		if site != "same-origin" && site != "none" {
			return false
		}
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	return strings.EqualFold(u.Host, r.Host)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
