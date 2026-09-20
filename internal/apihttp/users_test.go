package apihttp

import (
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchUsers(t *testing.T) {
	env := newTestEnv(t, fstest.MapFS{})
	for _, n := range []string{"Alice", "alfred", "bob"} {
		other := &testEnv{t: t, handler: env.handler}
		require.Equal(t, http.StatusCreated, other.do(http.MethodPost, "/api/v1/auth/register", credentials{Username: n, Password: "password-123"}).Code)
	}
	assert.Equal(t, env.do(http.MethodPost, "/api/v1/auth/register", credentials{Username: "carol", Password: "password-123"}).Code, http.StatusCreated)

	assert.Equal(t, http.StatusUnauthorized, (&testEnv{t: t, handler: env.handler}).do(http.MethodGet, "/api/v1/users?q=al", nil).Code)

	rec := env.do(http.MethodGet, "/api/v1/users?q=AL", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.ElementsMatch(t, []string{"Alice", "alfred"}, decodeBody[[]string](t, rec))

	rec = env.do(http.MethodGet, "/api/v1/users?q=a", nil)
	assert.Equal(t, "[]\n", rec.Body.String(), "prefixes shorter than two characters return nothing")
}
