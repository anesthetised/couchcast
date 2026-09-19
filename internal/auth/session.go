package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/repository"
)

// CookieName carries the session token. Prefixed so it cannot be set by a
// less secure sibling origin.
const CookieName = "couchcast_session"

// touchInterval bounds how often a sliding session is extended in the
// database; every request within the window reads only.
const touchInterval = 5 * time.Minute

// ErrBanned is returned by Resolve when the session belongs to a banned
// account. The session is revoked as a side effect.
var ErrBanned = errors.New("auth: account banned")

// SessionStore is the persistence the manager needs.
type SessionStore interface {
	CreateSession(ctx context.Context, tokenHash []byte, userID uuid.UUID, expiresAt time.Time) error
	GetSessionUser(ctx context.Context, tokenHash []byte, now time.Time) (*entity.Session, *entity.User, error)
	TouchSession(ctx context.Context, tokenHash []byte, now, expiresAt time.Time) error
	DeleteSession(ctx context.Context, tokenHash []byte) error
	DeleteUserSessions(ctx context.Context, userID uuid.UUID) (int64, error)
}

// Sessions issues, resolves and revokes cookie sessions.
type Sessions struct {
	store  SessionStore
	ttl    time.Duration
	secure bool
	now    func() time.Time
}

// NewSessions creates a manager. secure controls the cookie's Secure flag
// and must be true behind HTTPS.
func NewSessions(store SessionStore, ttl time.Duration, secure bool) *Sessions {
	return &Sessions{store: store, ttl: ttl, secure: secure, now: time.Now}
}

// Issue creates a session for the user and sets the cookie. Any session the
// request already carried is replaced (token rotation on login).
func (s *Sessions) Issue(ctx context.Context, w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	if old, ok := tokenFromRequest(r); ok {
		_ = s.store.DeleteSession(ctx, hashToken(old))
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("auth: generate token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	expires := s.now().Add(s.ttl)
	if err := s.store.CreateSession(ctx, hashToken(token), userID, expires); err != nil {
		return fmt.Errorf("auth: store session: %w", err)
	}

	http.SetCookie(w, s.cookie(token, expires))

	return nil
}

// Resolve returns the user behind the request's session, or nil when the
// request is anonymous. Sliding expiry is applied lazily.
func (s *Sessions) Resolve(ctx context.Context, r *http.Request) (*entity.User, error) {
	token, ok := tokenFromRequest(r)
	if !ok {
		return nil, nil
	}

	now := s.now()
	hash := hashToken(token)

	sess, user, err := s.store.GetSessionUser(ctx, hash, now)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth: resolve session: %w", err)
	}

	if user.IsBanned() {
		_, _ = s.store.DeleteUserSessions(ctx, user.ID)
		return nil, ErrBanned
	}

	if now.Sub(sess.LastSeenAt) > touchInterval {
		_ = s.store.TouchSession(ctx, hash, now, now.Add(s.ttl))
	}

	return user, nil
}

// Revoke deletes the request's session and clears the cookie.
func (s *Sessions) Revoke(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	if token, ok := tokenFromRequest(r); ok {
		if err := s.store.DeleteSession(ctx, hashToken(token)); err != nil {
			return fmt.Errorf("auth: revoke session: %w", err)
		}
	}

	http.SetCookie(w, s.clearedCookie())

	return nil
}

// clearedCookie expires the session cookie in the browser.
func (s *Sessions) clearedCookie() *http.Cookie {
	c := s.cookie("", time.Unix(0, 0)) //nolint:gosec // inherits flags from cookie()
	c.MaxAge = -1
	return c
}

// RevokeAll logs a user out of every device (ban, password change).
func (s *Sessions) RevokeAll(ctx context.Context, userID uuid.UUID) error {
	_, err := s.store.DeleteUserSessions(ctx, userID)
	return err
}

// cookie builds the session cookie. Secure follows configuration so that
// plain-HTTP development works; production must set SECURE_COOKIES=true.
func (s *Sessions) cookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // Secure is configuration-driven, see above
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	}
}

func tokenFromRequest(r *http.Request) (string, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}

// hashToken derives the storage key from the cookie value so that a leaked
// database does not yield usable sessions.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
