package apihttp

import (
	"log/slog"
	"net/http"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/metrics"
	"github.com/anesthetised/couchcast/internal/ratelimit"
	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

// dbEnv drives the real router over the real repository. One env per
// client so each has its own cookie jar.
type dbEnv struct {
	*testEnv
}

// newDBHandler builds the router over the real repository.
func newDBHandler(t *testing.T, opts ...func(*Deps)) http.Handler {
	t.Helper()
	repo := repository.New(repotest.Pool(t))
	deps := Deps{
		Logger:       slog.New(slog.DiscardHandler),
		DB:           repo,
		Metrics:      metrics.New("test"),
		Static:       fstest.MapFS{},
		Users:        repo,
		Rooms:        repo,
		Sessions:     auth.NewSessions(repo, time.Hour, false),
		AuthLimiter:  ratelimit.New(6000, 1000),
		LoginLimiter: ratelimit.New(6000, 1000),
	}
	for _, opt := range opts {
		opt(&deps)
	}
	return New(deps).Handler()
}

func newDBEnvs(t *testing.T, n int) []*dbEnv {
	t.Helper()
	handler := newDBHandler(t)
	out := make([]*dbEnv, n)
	for i := range out {
		out[i] = &dbEnv{&testEnv{t: t, handler: handler}}
	}
	return out
}

func (e *dbEnv) register(username string) {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/api/v1/auth/register", credentials{Username: username, Password: "password-123"})
	require.Equal(e.t, http.StatusCreated, rec.Code, rec.Body.String())
}

func TestRoomsCRUD(t *testing.T) {
	envs := newDBEnvs(t, 2)
	owner, other := envs[0], envs[1]
	owner.register("owner")
	other.register("other")

	// Anonymous cannot create.
	anon := &dbEnv{&testEnv{t: t, handler: owner.handler}}
	assert.Equal(t, http.StatusUnauthorized, anon.do(http.MethodPost, "/api/v1/rooms", createRoomRequest{Name: "x"}).Code)

	// Validation.
	assert.Equal(t, http.StatusBadRequest, owner.do(http.MethodPost, "/api/v1/rooms", createRoomRequest{Name: ""}).Code)
	assert.Equal(t, http.StatusBadRequest, owner.do(http.MethodPost, "/api/v1/rooms", createRoomRequest{Name: "x", Slug: "api"}).Code)
	assert.Equal(t, http.StatusBadRequest, owner.do(http.MethodPost, "/api/v1/rooms", createRoomRequest{Name: "x", Slug: "Bad Slug"}).Code)
	assert.Equal(t, http.StatusBadRequest, owner.do(http.MethodPost, "/api/v1/rooms", createRoomRequest{Name: "x", Visibility: "secret"}).Code)

	// Generated slug.
	rec := owner.do(http.MethodPost, "/api/v1/rooms", createRoomRequest{Name: "Movie night"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	gen := decodeBody[roomResponse](t, rec)
	assert.Len(t, gen.Slug, 8)
	assert.Equal(t, "owner", string(gen.MyRole))
	assert.Equal(t, "owner", gen.Owner)
	assert.Equal(t, 1, gen.MemberCount)
	assert.Equal(t, "public", string(gen.Visibility))

	// Custom slug, private.
	rec = owner.do(http.MethodPost, "/api/v1/rooms", createRoomRequest{Name: "Secret", Slug: "Secret-Club", Visibility: "private"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	priv := decodeBody[roomResponse](t, rec)
	assert.Equal(t, "secret-club", priv.Slug)
	assert.Equal(t, http.StatusConflict, owner.do(http.MethodPost, "/api/v1/rooms", createRoomRequest{Name: "dup", Slug: "secret-club"}).Code)

	// Visibility: anonymous sees public, not private; member sees both.
	assert.Equal(t, http.StatusOK, anon.do(http.MethodGet, "/api/v1/rooms/"+gen.Slug, nil).Code)
	assert.Equal(t, http.StatusUnauthorized, anon.do(http.MethodGet, "/api/v1/rooms/secret-club", nil).Code)
	assert.Equal(t, http.StatusForbidden, other.do(http.MethodGet, "/api/v1/rooms/secret-club", nil).Code)
	assert.Equal(t, http.StatusNotFound, other.do(http.MethodGet, "/api/v1/rooms/nope", nil).Code)
	rec = owner.do(http.MethodGet, "/api/v1/rooms/secret-club", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "owner", string(decodeBody[roomResponse](t, rec).MyRole))

	// Update: only the owner.
	name := "Renamed"
	assert.Equal(t, http.StatusForbidden, other.do(http.MethodPatch, "/api/v1/rooms/"+gen.Slug, updateRoomRequest{Name: &name}).Code)
	slug := "renamed-room"
	rec = owner.do(http.MethodPatch, "/api/v1/rooms/"+gen.Slug, updateRoomRequest{Name: &name, Slug: &slug})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "renamed-room", decodeBody[roomResponse](t, rec).Slug)
	taken := "secret-club"
	assert.Equal(t, http.StatusConflict, owner.do(http.MethodPatch, "/api/v1/rooms/renamed-room", updateRoomRequest{Slug: &taken}).Code)

	// My rooms.
	rec = owner.do(http.MethodGet, "/api/v1/me/rooms", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, decodeBody[[]roomResponse](t, rec), 2)
	rec = other.do(http.MethodGet, "/api/v1/me/rooms", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, decodeBody[[]roomResponse](t, rec))

	// Delete: only the owner.
	assert.Equal(t, http.StatusForbidden, other.do(http.MethodDelete, "/api/v1/rooms/renamed-room", nil).Code)
	assert.Equal(t, http.StatusNoContent, owner.do(http.MethodDelete, "/api/v1/rooms/renamed-room", nil).Code)
	assert.Equal(t, http.StatusNotFound, owner.do(http.MethodGet, "/api/v1/rooms/renamed-room", nil).Code)
}

func TestRoomModeratorsBansInvites(t *testing.T) {
	envs := newDBEnvs(t, 3)
	owner, mod, guest := envs[0], envs[1], envs[2]
	owner.register("owner")
	mod.register("mod")
	guest.register("guest")

	rec := owner.do(http.MethodPost, "/api/v1/rooms", createRoomRequest{Name: "Private", Slug: "club", Visibility: "private"})
	require.Equal(t, http.StatusCreated, rec.Code)
	base := "/api/v1/rooms/club"

	// Promote mod (adds membership in a private room).
	assert.Equal(t, http.StatusNotFound, owner.do(http.MethodPut, base+"/moderators/nobody", nil).Code)
	assert.Equal(t, http.StatusBadRequest, owner.do(http.MethodPut, base+"/moderators/owner", nil).Code)
	assert.Equal(t, http.StatusNoContent, owner.do(http.MethodPut, base+"/moderators/mod", nil).Code)
	rec = mod.do(http.MethodGet, base, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "moderator", string(decodeBody[roomResponse](t, rec).MyRole))

	// Moderators cannot manage moderators.
	assert.Equal(t, http.StatusForbidden, mod.do(http.MethodPut, base+"/moderators/guest", nil).Code)

	// Invite flow: mod invites guest.
	assert.Equal(t, http.StatusForbidden, guest.do(http.MethodGet, base, nil).Code)
	assert.Equal(t, http.StatusNotFound, mod.do(http.MethodPost, base+"/invites", inviteRequest{Username: "nobody"}).Code)
	rec = mod.do(http.MethodPost, base+"/invites", inviteRequest{Username: "Guest"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	inv := decodeBody[inviteResponse](t, rec)
	assert.Equal(t, "mod", inv.Inviter)
	assert.Equal(t, http.StatusConflict, mod.do(http.MethodPost, base+"/invites", inviteRequest{Username: "guest"}).Code)

	rec = guest.do(http.MethodGet, "/api/v1/invites", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, decodeBody[[]inviteResponse](t, rec), 1)

	// Only the invitee can act on it.
	assert.Equal(t, http.StatusNotFound, mod.do(http.MethodPost, "/api/v1/invites/"+inv.ID.String()+"/accept", nil).Code)
	rec = guest.do(http.MethodPost, "/api/v1/invites/"+inv.ID.String()+"/accept", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, http.StatusConflict, guest.do(http.MethodPost, "/api/v1/invites/"+inv.ID.String()+"/accept", nil).Code)
	assert.Equal(t, http.StatusOK, guest.do(http.MethodGet, base, nil).Code)
	assert.Equal(t, http.StatusConflict, mod.do(http.MethodPost, base+"/invites", inviteRequest{Username: "guest"}).Code, "already a member")

	rec = guest.do(http.MethodGet, base+"/members", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	members := decodeBody[[]memberResponse](t, rec)
	require.Len(t, members, 3)
	assert.Equal(t, "owner", members[0].Username)
	assert.Equal(t, "mod", members[1].Username)

	// Ban: guest loses access; membership and invite survive.
	assert.Equal(t, http.StatusForbidden, guest.do(http.MethodGet, base+"/bans", nil).Code)
	assert.Equal(t, http.StatusBadRequest, mod.do(http.MethodPut, base+"/bans/mod", banRequest{}).Code, "self")
	assert.Equal(t, http.StatusForbidden, mod.do(http.MethodPut, base+"/bans/owner", banRequest{}).Code)
	assert.Equal(t, http.StatusNoContent, mod.do(http.MethodPut, base+"/bans/guest", banRequest{Reason: "spoilers"}).Code)
	assert.Equal(t, http.StatusForbidden, guest.do(http.MethodGet, base, nil).Code)
	rec = mod.do(http.MethodGet, base+"/bans", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	bans := decodeBody[[]banResponse](t, rec)
	require.Len(t, bans, 1)
	assert.Equal(t, "spoilers", bans[0].Reason)

	// Banned users cannot be invited or promoted until unbanned.
	assert.Equal(t, http.StatusConflict, mod.do(http.MethodPost, base+"/invites", inviteRequest{Username: "guest"}).Code)
	assert.Equal(t, http.StatusConflict, owner.do(http.MethodPut, base+"/moderators/guest", nil).Code)

	// Unban restores access with no new invite.
	assert.Equal(t, http.StatusNoContent, mod.do(http.MethodDelete, base+"/bans/guest", nil).Code)
	assert.Equal(t, http.StatusNotFound, mod.do(http.MethodDelete, base+"/bans/guest", nil).Code)
	rec = guest.do(http.MethodGet, base, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "member", string(decodeBody[roomResponse](t, rec).MyRole))

	// Moderators can only be banned/removed by the owner.
	assert.Equal(t, http.StatusForbidden, guest.do(http.MethodPut, base+"/bans/mod", banRequest{}).Code)
	assert.Equal(t, http.StatusNoContent, owner.do(http.MethodPut, base+"/bans/mod", banRequest{Reason: "power trip"}).Code)
	assert.Equal(t, http.StatusForbidden, mod.do(http.MethodGet, base, nil).Code)
	assert.Equal(t, http.StatusNoContent, owner.do(http.MethodDelete, base+"/bans/mod", nil).Code)

	// Demote and remove.
	assert.Equal(t, http.StatusNoContent, owner.do(http.MethodDelete, base+"/moderators/mod", nil).Code)
	assert.Equal(t, http.StatusNotFound, owner.do(http.MethodDelete, base+"/moderators/mod", nil).Code)
	assert.Equal(t, http.StatusForbidden, mod.do(http.MethodDelete, base+"/members/guest", nil).Code, "demoted mod is a plain member")
	assert.Equal(t, http.StatusNoContent, owner.do(http.MethodDelete, base+"/members/guest", nil).Code)
	assert.Equal(t, http.StatusForbidden, guest.do(http.MethodGet, base, nil).Code, "removed from private room")
	assert.Equal(t, http.StatusNotFound, owner.do(http.MethodDelete, base+"/members/guest", nil).Code)

	// Removed user can be re-invited and decline.
	rec = owner.do(http.MethodPost, base+"/invites", inviteRequest{Username: "guest"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	inv = decodeBody[inviteResponse](t, rec)
	assert.Equal(t, http.StatusNoContent, guest.do(http.MethodPost, "/api/v1/invites/"+inv.ID.String()+"/decline", nil).Code)
	rec = guest.do(http.MethodGet, "/api/v1/invites", nil)
	assert.Empty(t, decodeBody[[]inviteResponse](t, rec))
}
