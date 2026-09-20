package apihttp

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

func TestMutes(t *testing.T) {
	repo := repository.New(repotest.Pool(t))
	handler := newDBHandler(t, func(d *Deps) { d.Rooms, d.Users, d.DB, d.Mutes = repo, repo, repo, repo })
	newEnv := func() *dbEnv { return &dbEnv{&testEnv{t: t, handler: handler}} }
	owner, alice := newEnv(), newEnv()
	owner.register("owner")
	alice.register("alice")
	rec := owner.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": "Movies", "slug": "movies"})
	require.Equal(t, http.StatusCreated, rec.Code)

	rec = alice.do(http.MethodPut, "/api/v1/rooms/movies/mutes/owner", map[string]any{"minutes": 5})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = owner.do(http.MethodPut, "/api/v1/rooms/movies/mutes/owner", map[string]any{"minutes": 5})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = owner.do(http.MethodPut, "/api/v1/rooms/movies/mutes/alice", map[string]any{"minutes": 7})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = owner.do(http.MethodPut, "/api/v1/rooms/movies/mutes/alice", map[string]any{"minutes": 30, "reason": "spam"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "spam", decodeBody[muteResponse](t, rec).Reason)

	rec = owner.do(http.MethodGet, "/api/v1/rooms/movies/mutes", nil)
	list := decodeBody[[]muteResponse](t, rec)
	require.Len(t, list, 1)
	assert.Equal(t, "alice", list[0].Username)

	rec = owner.do(http.MethodDelete, "/api/v1/rooms/movies/mutes/alice", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	rec = owner.do(http.MethodDelete, "/api/v1/rooms/movies/mutes/alice", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = owner.do(http.MethodGet, "/api/v1/rooms/movies/mutes", nil)
	assert.Empty(t, decodeBody[[]muteResponse](t, rec))
}
