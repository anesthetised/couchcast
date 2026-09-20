package apihttp

import (
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
)

func TestProfile(t *testing.T) {
	env := newTestEnv(t, fstest.MapFS{})
	rec := env.do(http.MethodPatch, "/api/v1/me", map[string]any{"avatarColor": "sky"})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	rec = env.do(http.MethodPost, "/api/v1/auth/register", credentials{Username: "carol", Password: "password-123"})
	assert.Equal(t, http.StatusCreated, rec.Code)

	// Avatar colour: palette keys only, reflected in /auth/me.
	rec = env.do(http.MethodPatch, "/api/v1/me", map[string]any{"avatarColor": "plaid"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = env.do(http.MethodPatch, "/api/v1/me", map[string]any{"avatarColor": "sky"})
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "sky", decodeBody[userResponse](t, rec).AvatarColor)
	rec = env.do(http.MethodGet, "/api/v1/auth/me", nil)
	assert.Equal(t, "sky", decodeBody[userResponse](t, rec).AvatarColor)

	// Password: wrong current, too short, then success keeps this session
	// and the new password logs in.
	rec = env.do(http.MethodPost, "/api/v1/me/password", map[string]any{"current": "nope", "new": "password-456"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = env.do(http.MethodPost, "/api/v1/me/password", map[string]any{"current": "password-123", "new": "short"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = env.do(http.MethodPost, "/api/v1/me/password", map[string]any{"current": "password-123", "new": "password-456"})
	assert.Equal(t, http.StatusNoContent, rec.Code)
	rec = env.do(http.MethodGet, "/api/v1/auth/me", nil)
	assert.Equal(t, http.StatusOK, rec.Code, "session reissued")

	other := newTestEnvSharing(t, env)
	rec = other.do(http.MethodPost, "/api/v1/auth/login", credentials{Username: "carol", Password: "password-123"})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	rec = other.do(http.MethodPost, "/api/v1/auth/login", credentials{Username: "carol", Password: "password-456"})
	assert.Equal(t, http.StatusOK, rec.Code)
}

// newTestEnvSharing is a second client against the same router and store.
func newTestEnvSharing(t *testing.T, env *testEnv) *testEnv {
	t.Helper()
	return &testEnv{t: t, store: env.store, handler: env.handler}
}
