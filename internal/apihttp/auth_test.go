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

func TestMySessions(t *testing.T) {
	laptop := newTestEnv(t, fstest.MapFS{})
	phone := &testEnv{t: t, store: laptop.store, handler: laptop.handler}
	tablet := &testEnv{t: t, store: laptop.store, handler: laptop.handler}
	stranger := &testEnv{t: t, store: laptop.store, handler: laptop.handler}

	creds := credentials{Username: "alice", Password: "correct-horse"}
	require.Equal(t, http.StatusCreated, laptop.doWith(http.MethodPost, "/api/v1/auth/register", creds, map[string]string{"User-Agent": "Firefox/140"}).Code)
	require.Equal(t, http.StatusOK, phone.doWith(http.MethodPost, "/api/v1/auth/login", creds, map[string]string{"User-Agent": "Mobile Safari"}).Code)
	require.Equal(t, http.StatusOK, tablet.do(http.MethodPost, "/api/v1/auth/login", creds).Code)
	require.Equal(t, http.StatusCreated, stranger.do(http.MethodPost, "/api/v1/auth/register", credentials{Username: "mallory", Password: "correct-horse"}).Code)

	rec := laptop.do(http.MethodGet, "/api/v1/me/sessions", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	list := decodeBody[[]sessionResponse](t, rec)
	require.Len(t, list, 3, "only alice's sessions")
	var current, onPhone *sessionResponse
	for i := range list {
		if list[i].Current {
			current = &list[i]
		}
		if list[i].UserAgent == "Mobile Safari" {
			onPhone = &list[i]
		}
	}
	require.NotNil(t, current)
	assert.Equal(t, "Firefox/140", current.UserAgent)
	require.NotNil(t, onPhone)

	// The current session is not revoked here; others' ids are unknown.
	assert.Equal(t, http.StatusBadRequest, laptop.do(http.MethodDelete, "/api/v1/me/sessions/"+current.ID.String(), nil).Code)
	assert.Equal(t, http.StatusNotFound, stranger.do(http.MethodDelete, "/api/v1/me/sessions/"+onPhone.ID.String(), nil).Code)
	assert.Equal(t, http.StatusNotFound, laptop.do(http.MethodDelete, "/api/v1/me/sessions/nope", nil).Code)

	// Signing the phone out ends its session.
	assert.Equal(t, http.StatusNoContent, laptop.do(http.MethodDelete, "/api/v1/me/sessions/"+onPhone.ID.String(), nil).Code)
	assert.Equal(t, http.StatusUnauthorized, phone.do(http.MethodGet, "/api/v1/auth/me", nil).Code)
	assert.Equal(t, http.StatusOK, tablet.do(http.MethodGet, "/api/v1/auth/me", nil).Code)

	// Everywhere else: the tablet goes, the laptop and the stranger stay.
	rec = laptop.do(http.MethodDelete, "/api/v1/me/sessions", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.EqualValues(t, 1, decodeBody[map[string]int64](t, rec)["revoked"])
	assert.Equal(t, http.StatusUnauthorized, tablet.do(http.MethodGet, "/api/v1/auth/me", nil).Code)
	assert.Equal(t, http.StatusOK, laptop.do(http.MethodGet, "/api/v1/auth/me", nil).Code)
	assert.Equal(t, http.StatusOK, stranger.do(http.MethodGet, "/api/v1/auth/me", nil).Code)
	assert.Equal(t, http.StatusUnauthorized, phone.do(http.MethodGet, "/api/v1/me/sessions", nil).Code)
}
