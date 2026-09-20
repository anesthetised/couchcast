package apihttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"testing/fstest"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/metrics"
	"github.com/anesthetised/couchcast/internal/ratelimit"
	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

type fakeAdmit struct{}

func (fakeAdmit) Key(raw string) (string, error) {
	if raw == "bad" {
		return "", errors.New("unsupported")
	}
	return "url:" + raw, nil
}

type fakeQueue struct{ calls []string }

func (f *fakeQueue) QueueAdd(_ context.Context, roomID uuid.UUID, actor access.Actor, raw string) error {
	f.calls = append(f.calls, roomID.String()+" "+actor.User.Username+" "+raw)
	if raw == "https://fails" {
		return errors.New("boom")
	}
	return nil
}

func TestCreateRoomWithExtras(t *testing.T) {
	repo := repository.New(repotest.Pool(t))
	queue := &fakeQueue{}
	srv := New(Deps{
		Logger: slog.New(slog.DiscardHandler), DB: repo, Metrics: metrics.New("test"), Static: fstest.MapFS{},
		Users: repo, Rooms: repo, Admin: repo, Sessions: auth.NewSessions(repo, time.Hour, false),
		AuthLimiter: ratelimit.New(6000, 1000), LoginLimiter: ratelimit.New(6000, 1000),
		Admit: fakeAdmit{}, LiveQueue: queue,
	})
	owner := &dbEnv{&testEnv{t: t, handler: srv.Handler()}}
	guest := &dbEnv{&testEnv{t: t, handler: srv.Handler()}}
	owner.register("owner")
	guest.register("guest")
	ctx := context.Background()

	// Bad first link: nothing is created.
	rec := owner.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": "x", "firstUrl": "bad"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rooms, _, _ := repo.ListDirectory(ctx, repository.DirectoryQuery{Limit: 10})
	assert.Empty(t, rooms)

	// Blocked first link.
	require.NoError(t, repo.BlockSource(ctx, "url:https://blocked", "x", nil))
	rec = owner.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": "x", "firstUrl": "https://blocked"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Full creation: settings, first video queued through the live room,
	// invites sent, unknown invitee and self reported as warnings only.
	rec = owner.do(http.MethodPost, "/api/v1/rooms", map[string]any{
		"name": "Party", "slug": "party", "visibility": "private",
		"firstUrl": "https://first",
		"settings": map[string]any{"voteMode": true, "viewersCanAdd": false},
		"invites":  []string{"guest", "nobody", "owner"},
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	resp := decodeBody[createRoomResponse](t, rec)
	assert.Equal(t, "party", resp.Slug)
	assert.True(t, resp.Settings.VoteMode)
	assert.False(t, resp.Settings.ViewersCanAdd)
	assert.Equal(t, []string{"no such user: nobody"}, resp.Warnings)
	require.Len(t, queue.calls, 1)
	assert.Equal(t, resp.ID.String()+" owner https://first", queue.calls[0])

	inv := guest.do(http.MethodGet, "/api/v1/invites", nil)
	invites := decodeBody[[]inviteResponse](t, inv)
	require.Len(t, invites, 1)
	assert.Equal(t, "party", invites[0].RoomSlug)

	// A queue failure after creation is a warning, not an error.
	rec = owner.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": "Y", "firstUrl": "https://fails"})
	require.Equal(t, http.StatusCreated, rec.Code)
	resp = decodeBody[createRoomResponse](t, rec)
	require.Len(t, resp.Warnings, 1)
	assert.Contains(t, resp.Warnings[0], "could not be queued")

	// Plain creation still works without extras.
	rec = owner.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": "Plain"})
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Empty(t, decodeBody[createRoomResponse](t, rec).Warnings)
}
