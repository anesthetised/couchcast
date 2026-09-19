package apihttp

import (
	"net/http"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/ratelimit"
)

func TestAuthFlow(t *testing.T) {
	env := newTestEnv(t, fstest.MapFS{})

	// Anonymous.
	assert.Equal(t, http.StatusUnauthorized, env.do(http.MethodGet, "/api/v1/auth/me", nil).Code)

	// Validation.
	rec := env.do(http.MethodPost, "/api/v1/auth/register", credentials{Username: "Al", Password: "longenough"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = env.do(http.MethodPost, "/api/v1/auth/register", credentials{Username: "alice", Password: "short"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = env.do(http.MethodPost, "/api/v1/auth/register", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Register logs in and keeps the username's case.
	rec = env.do(http.MethodPost, "/api/v1/auth/register", credentials{Username: " Alice ", Password: "correct-horse"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	created := decodeBody[userResponse](t, rec)
	assert.Equal(t, "Alice", created.Username)
	assert.Equal(t, "user", string(created.Role))
	require.Len(t, env.cookies, 1)

	rec = env.do(http.MethodGet, "/api/v1/auth/me", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, created.ID, decodeBody[userResponse](t, rec).ID)

	// Duplicate, regardless of case.
	rec = env.do(http.MethodPost, "/api/v1/auth/register", credentials{Username: "alice", Password: "correct-horse"})
	assert.Equal(t, http.StatusConflict, rec.Code)

	// Logout clears the cookie and the session.
	assert.Equal(t, http.StatusNoContent, env.do(http.MethodPost, "/api/v1/auth/logout", nil).Code)
	assert.Empty(t, env.cookies)
	assert.Empty(t, env.store.sessions)
	assert.Equal(t, http.StatusUnauthorized, env.do(http.MethodGet, "/api/v1/auth/me", nil).Code)

	// Login: wrong password and unknown user look identical.
	rec = env.do(http.MethodPost, "/api/v1/auth/login", credentials{Username: "alice", Password: "nope-nope"})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	rec = env.do(http.MethodPost, "/api/v1/auth/login", credentials{Username: "nobody", Password: "nope-nope"})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	rec = env.do(http.MethodPost, "/api/v1/auth/login", credentials{Username: "ALICE", Password: "correct-horse"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, http.StatusOK, env.do(http.MethodGet, "/api/v1/auth/me", nil).Code)

	// Banned users are rejected on login and on existing sessions.
	now := time.Now()
	env.store.users[created.ID].BannedAt = &now
	assert.Equal(t, http.StatusForbidden, env.do(http.MethodGet, "/api/v1/auth/me", nil).Code)
	assert.Empty(t, env.cookies, "ban clears the cookie")
	rec = env.do(http.MethodPost, "/api/v1/auth/login", credentials{Username: "alice", Password: "correct-horse"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAuthOriginCheck(t *testing.T) {
	env := newTestEnv(t, fstest.MapFS{})

	same := env.doWith(http.MethodPost, "/api/v1/auth/logout", nil, map[string]string{"Origin": "http://example.com"})
	assert.Equal(t, http.StatusNoContent, same.Code)

	cross := env.doWith(http.MethodPost, "/api/v1/auth/logout", nil, map[string]string{"Origin": "http://evil.example"})
	assert.Equal(t, http.StatusForbidden, cross.Code)
}

func TestLoginRateLimits(t *testing.T) {
	env := newTestEnv(t, fstest.MapFS{}, func(d *Deps) {
		d.LoginLimiter = ratelimit.New(60, 2)
	})
	rec := env.do(http.MethodPost, "/api/v1/auth/register", credentials{Username: "carol", Password: "correct-horse"})
	require.Equal(t, http.StatusCreated, rec.Code)
	env.do(http.MethodPost, "/api/v1/auth/logout", nil)

	// Per-username limiter: two attempts, then 429.
	bad := credentials{Username: "carol", Password: "wrong-wrong"}
	assert.Equal(t, http.StatusUnauthorized, env.do(http.MethodPost, "/api/v1/auth/login", bad).Code)
	assert.Equal(t, http.StatusUnauthorized, env.do(http.MethodPost, "/api/v1/auth/login", bad).Code)
	assert.Equal(t, http.StatusTooManyRequests, env.do(http.MethodPost, "/api/v1/auth/login", bad).Code)
}

func TestAuthIPRateLimit(t *testing.T) {
	env := newTestEnv(t, fstest.MapFS{}, func(d *Deps) {
		d.AuthLimiter = ratelimit.New(60, 1)
	})
	c := credentials{Username: "dave", Password: "correct-horse"}
	assert.Equal(t, http.StatusCreated, env.do(http.MethodPost, "/api/v1/auth/register", c).Code)
	assert.Equal(t, http.StatusTooManyRequests, env.do(http.MethodPost, "/api/v1/auth/login", c).Code)
}
