package apihttp

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLeaveAndTransfer(t *testing.T) {
	envs := newDBEnvs(t, 3)
	owner, alice, bob := envs[0], envs[1], envs[2]
	owner.register("owner")
	alice.register("alice")
	bob.register("bob")

	rec := owner.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": "Movies", "slug": "movies", "visibility": "private", "description": "  Friday nights  "})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "Friday nights", decodeBody[roomResponse](t, rec).Description)

	// Description is patchable and bounded.
	rec = owner.do(http.MethodPatch, "/api/v1/rooms/movies", map[string]any{"description": string(make([]byte, 301))})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = owner.do(http.MethodPatch, "/api/v1/rooms/movies", map[string]any{"description": "Horror season"})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "Horror season", decodeBody[roomResponse](t, rec).Description)

	for _, name := range []string{"alice", "bob"} {
		rec = owner.do(http.MethodPost, "/api/v1/rooms/movies/invites", map[string]any{"username": name})
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	}
	for _, e := range []*dbEnv{alice, bob} {
		rec = e.do(http.MethodGet, "/api/v1/invites", nil)
		id := decodeBody[[]inviteResponse](t, rec)[0].ID
		rec = e.do(http.MethodPost, "/api/v1/invites/"+id.String()+"/accept", nil)
		require.Equal(t, http.StatusOK, rec.Code)
	}

	// A member may leave; the owner may not until the room is handed over.
	rec = bob.do(http.MethodDelete, "/api/v1/rooms/movies/members/bob", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	rec = bob.do(http.MethodGet, "/api/v1/rooms/movies", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code, "private room closed to a former member")
	rec = owner.do(http.MethodDelete, "/api/v1/rooms/movies/members/owner", nil)
	assert.Equal(t, http.StatusConflict, rec.Code)
	// Members still cannot remove others.
	rec = alice.do(http.MethodDelete, "/api/v1/rooms/movies/members/owner", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Transfer: only to a member, only by the owner.
	rec = alice.do(http.MethodPost, "/api/v1/rooms/movies/owner", map[string]any{"username": "alice"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = owner.do(http.MethodPost, "/api/v1/rooms/movies/owner", map[string]any{"username": "bob"})
	assert.Equal(t, http.StatusNotFound, rec.Code, "bob left")
	rec = owner.do(http.MethodPost, "/api/v1/rooms/movies/owner", map[string]any{"username": "owner"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = owner.do(http.MethodPost, "/api/v1/rooms/movies/owner", map[string]any{"username": "alice"})
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	rec = alice.do(http.MethodGet, "/api/v1/rooms/movies", nil)
	room := decodeBody[roomResponse](t, rec)
	assert.Equal(t, "alice", room.Owner)
	assert.EqualValues(t, "owner", room.MyRole)
	rec = owner.do(http.MethodGet, "/api/v1/rooms/movies", nil)
	assert.EqualValues(t, "moderator", decodeBody[roomResponse](t, rec).MyRole)

	// The former owner may now leave.
	rec = owner.do(http.MethodDelete, "/api/v1/rooms/movies/members/owner", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}
