package apihttp

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

func TestStars(t *testing.T) {
	repo := repository.New(repotest.Pool(t))
	handler := newDBHandler(t, func(d *Deps) { d.Rooms, d.Users, d.DB, d.Stars, d.Directory = repo, repo, repo, repo, repo })
	newEnv := func() *dbEnv { return &dbEnv{&testEnv{t: t, handler: handler}} }
	owner, alice, anon := newEnv(), newEnv(), newEnv()
	owner.register("owner")
	alice.register("alice")
	for _, slug := range []string{"movies", "series"} {
		rec := owner.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": slug, "slug": slug})
		require.Equal(t, http.StatusCreated, rec.Code)
	}
	rec := owner.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": "Secret", "slug": "secret", "visibility": "private"})
	require.Equal(t, http.StatusCreated, rec.Code)

	// Anyone who may view the room may star it; anonymous may not.
	assert.Equal(t, http.StatusUnauthorized, anon.do(http.MethodPut, "/api/v1/rooms/movies/star", nil).Code)
	assert.Equal(t, http.StatusForbidden, alice.do(http.MethodPut, "/api/v1/rooms/secret/star", nil).Code)
	assert.Equal(t, http.StatusNoContent, alice.do(http.MethodPut, "/api/v1/rooms/movies/star", nil).Code)
	assert.Equal(t, http.StatusNoContent, alice.do(http.MethodPut, "/api/v1/rooms/movies/star", nil).Code, "idempotent")

	room := decodeBody[roomResponse](t, alice.do(http.MethodGet, "/api/v1/rooms/movies", nil))
	assert.True(t, room.Starred)
	room = decodeBody[roomResponse](t, owner.do(http.MethodGet, "/api/v1/rooms/movies", nil))
	assert.False(t, room.Starred, "stars are per user")

	// The directory flags and filters starred rooms for the viewer.
	dir := decodeBody[directoryResponse](t, alice.do(http.MethodGet, "/api/v1/rooms?starred=1", nil))
	require.Len(t, dir.Rooms, 1)
	assert.Equal(t, "movies", dir.Rooms[0].Slug)
	assert.True(t, dir.Rooms[0].Starred)
	dir = decodeBody[directoryResponse](t, alice.do(http.MethodGet, "/api/v1/rooms", nil))
	assert.Len(t, dir.Rooms, 2)
	dir = decodeBody[directoryResponse](t, anon.do(http.MethodGet, "/api/v1/rooms?starred=1", nil))
	assert.Len(t, dir.Rooms, 2, "anonymous ignores the filter")

	assert.Equal(t, http.StatusNoContent, alice.do(http.MethodDelete, "/api/v1/rooms/movies/star", nil).Code)
	dir = decodeBody[directoryResponse](t, alice.do(http.MethodGet, "/api/v1/rooms?starred=1", nil))
	assert.Empty(t, dir.Rooms)
}
