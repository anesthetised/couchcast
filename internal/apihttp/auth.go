package apihttp

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/repository"
)

// UserStore is what the auth handlers need from the repository.
type UserStore interface {
	CreateUser(ctx context.Context, username, passwordHash string) (*entity.User, error)
	GetUserByUsername(ctx context.Context, username string) (*entity.User, error)
}

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,32}$`)

const (
	minPasswordLen = 8
	maxPasswordLen = 128
)

// dummyHash is verified against when the username does not exist so that
// login latency does not reveal whether an account exists.
var dummyHash = func() string {
	h, err := auth.HashPassword("dummy-password-for-timing")
	if err != nil {
		panic(err)
	}
	return h
}()

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type userResponse struct {
	ID        uuid.UUID   `json:"id"`
	Username  string      `json:"username"`
	Role      entity.Role `json:"role"`
	CreatedAt time.Time   `json:"createdAt"`
}

func toUserResponse(u *entity.User) userResponse {
	return userResponse{ID: u.ID, Username: u.Username, Role: u.Role, CreatedAt: u.CreatedAt}
}

// normalizeCredentials trims the username (case is kept as typed; lookups
// ignore it) and validates both fields. It returns a user-facing message
// on failure.
func normalizeCredentials(c *credentials) string {
	c.Username = strings.TrimSpace(c.Username)
	if !usernameRe.MatchString(c.Username) {
		return "username must be 3-32 characters: letters, digits and underscore"
	}
	if n := len(c.Password); n < minPasswordLen || n > maxPasswordLen {
		return "password must be between 8 and 128 characters"
	}
	return ""
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decodeJSON(w, r, &c) {
		return
	}
	if msg := normalizeCredentials(&c); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	hash, err := auth.HashPassword(c.Password)
	if err != nil {
		s.internalError(w, r, "hash password", err)
		return
	}

	user, err := s.deps.Users.CreateUser(r.Context(), c.Username, hash)
	switch {
	case errors.Is(err, repository.ErrConflict):
		writeError(w, http.StatusConflict, "username is taken")
		return
	case err != nil:
		s.internalError(w, r, "create user", err)
		return
	}

	if err := s.deps.Sessions.Issue(r.Context(), w, r, user.ID); err != nil {
		s.internalError(w, r, "issue session", err)
		return
	}

	writeJSON(w, http.StatusCreated, toUserResponse(user))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decodeJSON(w, r, &c) {
		return
	}
	c.Username = strings.TrimSpace(c.Username)

	// Per-account limit on top of the per-IP one applied by the router.
	if s.deps.LoginLimiter != nil && !s.deps.LoginLimiter.Allow("user:"+strings.ToLower(c.Username)) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}

	user, err := s.deps.Users.GetUserByUsername(r.Context(), c.Username)
	hash := dummyHash
	if err == nil {
		hash = user.PasswordHash
	} else if !errors.Is(err, repository.ErrNotFound) {
		s.internalError(w, r, "lookup user", err)
		return
	}

	ok, verr := auth.VerifyPassword(hash, c.Password)
	if verr != nil {
		s.internalError(w, r, "verify password", verr)
		return
	}
	if user == nil || !ok {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if user.IsBanned() {
		writeError(w, http.StatusForbidden, "account is banned")
		return
	}

	if err := s.deps.Sessions.Issue(r.Context(), w, r, user.ID); err != nil {
		s.internalError(w, r, "issue session", err)
		return
	}

	writeJSON(w, http.StatusOK, toUserResponse(user))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Sessions.Revoke(r.Context(), w, r); err != nil {
		s.internalError(w, r, "revoke session", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(u))
}
