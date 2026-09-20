package apihttp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

func TestInviteLinks(t *testing.T) {
	repo := repository.New(repotest.Pool(t))
	handler := newDBHandler(t, func(d *Deps) {
		d.Rooms, d.Users, d.DB, d.InviteLinks = repo, repo, repo, repo
	})
	newEnv := func() *dbEnv { return &dbEnv{&testEnv{t: t, handler: handler}} }
	owner, alice, bob, anon := newEnv(), newEnv(), newEnv(), newEnv()
	owner.register("owner")
	alice.register("alice")
	bob.register("bob")
	rec := owner.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": "Movies", "slug": "movies", "visibility": "private"})
	require.Equal(t, http.StatusCreated, rec.Code)

	// Only moderators mint links; bad parameters are rejected.
	rec = alice.do(http.MethodPost, "/api/v1/rooms/movies/invite-links", map[string]any{})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = owner.do(http.MethodPost, "/api/v1/rooms/movies/invite-links", map[string]any{"expiresIn": "2h"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = owner.do(http.MethodPost, "/api/v1/rooms/movies/invite-links", map[string]any{"maxUses": 0})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = owner.do(http.MethodPost, "/api/v1/rooms/movies/invite-links", map[string]any{"expiresIn": "7d", "maxUses": 1})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	created := decodeBody[inviteLinkResponse](t, rec)
	require.Contains(t, created.URL, "http://example.com/join/")
	token := strings.TrimPrefix(created.URL, "http://example.com/join/")
	assert.NotNil(t, created.ExpiresAt)

	// Listing never shows the token.
	rec = owner.do(http.MethodGet, "/api/v1/rooms/movies/invite-links", nil)
	list := decodeBody[[]inviteLinkResponse](t, rec)
	require.Len(t, list, 1)
	assert.Empty(t, list[0].URL)
	assert.Equal(t, "owner", list[0].CreatedBy)

	// Preview works signed out; joining needs a session.
	rec = anon.do(http.MethodGet, "/api/v1/join/"+token, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	preview := decodeBody[joinPreviewResponse](t, rec)
	assert.Equal(t, "movies", preview.RoomSlug)
	assert.True(t, preview.Valid)
	rec = anon.do(http.MethodPost, "/api/v1/join/"+token, nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	rec = anon.do(http.MethodGet, "/api/v1/join/nope", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Alice joins and can now open the private room; the single use is spent.
	rec = alice.do(http.MethodPost, "/api/v1/join/"+token, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = alice.do(http.MethodGet, "/api/v1/rooms/movies", nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.EqualValues(t, "member", decodeBody[roomResponse](t, rec).MyRole)
	rec = alice.do(http.MethodPost, "/api/v1/join/"+token, nil)
	assert.Equal(t, http.StatusOK, rec.Code, "a member is let through without charging the link")
	rec = bob.do(http.MethodGet, "/api/v1/join/"+token, nil)
	assert.Equal(t, "used up", decodeBody[joinPreviewResponse](t, rec).Reason)
	rec = bob.do(http.MethodPost, "/api/v1/join/"+token, nil)
	assert.Equal(t, http.StatusGone, rec.Code)

	// A fresh unlimited link, revoked, stops working; a banned user is refused.
	rec = owner.do(http.MethodPost, "/api/v1/rooms/movies/invite-links", map[string]any{})
	created = decodeBody[inviteLinkResponse](t, rec)
	token2 := strings.TrimPrefix(created.URL, "http://example.com/join/")
	rec = owner.do(http.MethodPut, "/api/v1/rooms/movies/bans/bob", map[string]any{"reason": "no"})
	require.Equal(t, http.StatusNoContent, rec.Code)
	rec = bob.do(http.MethodPost, "/api/v1/join/"+token2, nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = owner.do(http.MethodDelete, "/api/v1/rooms/movies/invite-links/"+created.ID.String(), nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	rec = owner.do(http.MethodDelete, "/api/v1/rooms/movies/invite-links/"+created.ID.String(), nil)
	assert.Equal(t, http.StatusNotFound, rec.Code, "already revoked")
	rec = owner.do(http.MethodPost, "/api/v1/join/"+token2, nil)
	assert.Equal(t, http.StatusOK, rec.Code, "the owner is a member already")
	rec = bob.do(http.MethodGet, "/api/v1/join/"+token2, nil)
	assert.Equal(t, "banned", decodeBody[joinPreviewResponse](t, rec).Reason)
}
