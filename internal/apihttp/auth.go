package apihttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"uuid"

	"github.com/go-chi/chi/v5"

	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/repository"
)

// UserStore is what the auth handlers need from the repository.
type UserStore interface {
	CreateUser(ctx context.Context, username, passwordHash string) (*entity.User, error)
	GetUserByUsername(ctx context.Context, username string) (*entity.User, error)
	SearchUsernames(ctx context.Context, prefix string, limit int) ([]string, error)
	SetUserAvatarColor(ctx context.Context, id uuid.UUID, color string) error
	SetUserPassword(ctx context.Context, id uuid.UUID, passwordHash string) error
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
	ID          uuid.UUID   `json:"id"`
	Username    string      `json:"username"`
	Role        entity.Role `json:"role"`
	CreatedAt   time.Time   `json:"createdAt"`
	AvatarColor string      `json:"avatarColor,omitempty"`
}

func toUserResponse(u *entity.User) userResponse {
	return userResponse{ID: u.ID, Username: u.Username, Role: u.Role, CreatedAt: u.CreatedAt, AvatarColor: u.AvatarColor}
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

// handleSearchUsers powers username autocomplete for invites: a short
// prefix match, signed-in users only, small result set.
func (s *Server) handleSearchUsers(w http.ResponseWriter, r *http.Request) {
	prefix := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(prefix) < 2 || len(prefix) > 32 {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	names, err := s.deps.Users.SearchUsernames(r.Context(), prefix, 8)
	if err != nil {
		s.internalError(w, r, "search users", err)
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, names)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(u))
}

// --- profile -----------------------------------------------------------------

type updateMeRequest struct {
	AvatarColor *string `json:"avatarColor"`
}

// handleUpdateMe changes profile fields; today only the avatar colour.
func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	var req updateMeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.AvatarColor != nil {
		if !entity.ValidAvatarColor(*req.AvatarColor) {
			writeError(w, http.StatusBadRequest, "unknown avatar colour")
			return
		}
		if err := s.deps.Users.SetUserAvatarColor(r.Context(), u.ID, *req.AvatarColor); err != nil {
			s.internalError(w, r, "set avatar colour", err)
			return
		}
		u.AvatarColor = *req.AvatarColor
	}
	writeJSON(w, http.StatusOK, toUserResponse(u))
}

type changePasswordRequest struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

// handleChangePassword verifies the current password, stores the new hash
// and logs every other device out; this session is reissued so the caller
// stays signed in.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	var req changePasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if n := len(req.New); n < minPasswordLen || n > maxPasswordLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("password must be %d-%d characters", minPasswordLen, maxPasswordLen))
		return
	}
	ok, err := auth.VerifyPassword(u.PasswordHash, req.Current)
	if err != nil {
		s.internalError(w, r, "verify password", err)
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, "current password is wrong")
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		s.internalError(w, r, "hash password", err)
		return
	}
	if err := s.deps.Users.SetUserPassword(r.Context(), u.ID, hash); err != nil {
		s.internalError(w, r, "set password", err)
		return
	}
	if err := s.deps.Sessions.RevokeAll(r.Context(), u.ID); err != nil {
		s.internalError(w, r, "revoke sessions", err)
		return
	}
	if err := s.deps.Sessions.Issue(r.Context(), w, r, u.ID); err != nil {
		s.internalError(w, r, "issue session", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sessionResponse is one place the user is signed in.
type sessionResponse struct {
	ID         uuid.UUID `json:"id"`
	UserAgent  string    `json:"userAgent"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	Current    bool      `json:"current"`
}

func (s *Server) handleMySessions(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	list, err := s.deps.Sessions.List(r.Context(), r, u.ID)
	if err != nil {
		s.internalError(w, r, "list sessions", err)
		return
	}
	out := make([]sessionResponse, 0, len(list))
	for _, sess := range list {
		out = append(out, sessionResponse{ID: sess.ID, UserAgent: sess.UserAgent, CreatedAt: sess.CreatedAt, LastSeenAt: sess.LastSeenAt, Current: sess.Current})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRevokeSession signs one other session out; the current one
// logs out through /auth/logout instead.
func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	err = s.deps.Sessions.RevokeByID(r.Context(), r, u.ID, id)
	switch {
	case errors.Is(err, auth.ErrCurrentSession):
		writeError(w, http.StatusBadRequest, "this is the current session: log out instead")
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "session not found")
	case err != nil:
		s.internalError(w, r, "revoke session", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleRevokeOtherSessions signs out everywhere but here.
func (s *Server) handleRevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	n, err := s.deps.Sessions.RevokeOthers(r.Context(), r, u.ID)
	if err != nil {
		s.internalError(w, r, "revoke other sessions", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"revoked": n})
}
