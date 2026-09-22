package apihttp

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

func TestScheduledSessions(t *testing.T) {
	repo := repository.New(repotest.Pool(t))
	handler := newDBHandler(t, func(d *Deps) { d.Rooms, d.Users, d.DB, d.Directory = repo, repo, repo, repo })
	newEnv := func() *dbEnv { return &dbEnv{&testEnv{t: t, handler: handler}} }
	owner, alice := newEnv(), newEnv()
	owner.register("owner")
	alice.register("alice")

	soon := time.Now().Add(20 * time.Minute).UTC().Truncate(time.Second)
	rec := owner.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": "Friday", "slug": "friday", "scheduledAt": soon.Format(time.RFC3339)})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	room := decodeBody[roomResponse](t, rec)
	require.NotNil(t, room.ScheduledAt)
	assert.True(t, room.ScheduledAt.Equal(soon))
	rec = owner.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": "Later", "slug": "later"})
	require.Equal(t, http.StatusCreated, rec.Code)

	// The past is refused; a later time and null both patch.
	rec = owner.do(http.MethodPatch, "/api/v1/rooms/later", map[string]any{"scheduledAt": time.Now().Add(-time.Hour).Format(time.RFC3339)})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	far := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Second)
	rec = owner.do(http.MethodPatch, "/api/v1/rooms/later", map[string]any{"scheduledAt": far.Format(time.RFC3339)})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, decodeBody[roomResponse](t, rec).ScheduledAt)

	// The directory filters and orders upcoming rooms, soonest first.
	dir := decodeBody[directoryResponse](t, alice.do(http.MethodGet, "/api/v1/rooms?upcoming=1", nil))
	require.Len(t, dir.Rooms, 2)
	assert.Equal(t, "friday", dir.Rooms[0].Slug)
	assert.Equal(t, soon.UnixMilli(), dir.Rooms[0].ScheduledMs)

	// /me/upcoming lists the caller's member rooms starting within the
	// horizon; alice is in neither, the owner only in the near one.
	assert.Empty(t, decodeBody[[]upcomingResponse](t, alice.do(http.MethodGet, "/api/v1/me/upcoming", nil)))
	up := decodeBody[[]upcomingResponse](t, owner.do(http.MethodGet, "/api/v1/me/upcoming", nil))
	require.Len(t, up, 1)
	assert.Equal(t, "friday", up[0].Slug)

	rec = owner.do(http.MethodPatch, "/api/v1/rooms/later", map[string]any{"scheduledAt": nil})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Nil(t, decodeBody[roomResponse](t, rec).ScheduledAt)
	rec = owner.do(http.MethodPatch, "/api/v1/rooms/later", map[string]any{"name": "Renamed"})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Nil(t, decodeBody[roomResponse](t, rec).ScheduledAt, "untouched fields stay")
}
