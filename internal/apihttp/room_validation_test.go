package apihttp

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errorOf returns the status and the error message of a response.
func errorOf(t *testing.T, e *dbEnv, method, path string, body any) (int, string) {
	t.Helper()
	rec := e.do(method, path, body)
	if rec.Code < 400 {
		return rec.Code, ""
	}
	return rec.Code, decodeBody[map[string]string](t, rec)["error"]
}

func TestRoomInputIsValidated(t *testing.T) {
	envs := newDBEnvs(t, 1)
	owner := envs[0]
	owner.register("keeper")

	// Creating: each bad field says what is wrong.
	for _, c := range []struct {
		body map[string]any
		want string
	}{
		{map[string]any{"name": ""}, "name must be between 1 and 80 characters"},
		{map[string]any{"name": strings.Repeat("n", 81)}, "name must be between 1 and 80 characters"},
		{map[string]any{"name": "Ok", "description": strings.Repeat("d", 301)}, "description must be at most 300 characters"},
		{map[string]any{"name": "Ok", "visibility": "secret"}, "visibility must be public or private"},
		{map[string]any{"name": "Ok", "slug": "No Spaces"}, "slug must be 3-32 characters"},
		{map[string]any{"name": "Ok", "scheduledAt": "tomorrow"}, "scheduledAt must be an RFC 3339 time"},
		{map[string]any{"name": "Ok", "scheduledAt": time.Now().Add(-time.Hour).Format(time.RFC3339)}, "the scheduled start must be in the future"},
		{map[string]any{"name": "Ok", "firstUrl": "https://youtu.be/x"}, "starting with a video is not available"},
		{map[string]any{"name": "Ok", "invites": make([]string, 51)}, "at most 50 invites at creation"},
	} {
		code, msg := errorOf(t, owner, http.MethodPost, "/api/v1/rooms", c.body)
		assert.Equal(t, http.StatusBadRequest, code, c.want)
		assert.Contains(t, msg, c.want)
	}

	// Without a slug one is made up, and it works as the room's link.
	rec := owner.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": "Friday"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	slug := decodeBody[roomResponse](t, rec).Slug
	assert.Regexp(t, `^[a-z0-9-]{3,32}$`, slug)
	require.Equal(t, http.StatusOK, owner.do(http.MethodGet, "/api/v1/rooms/"+slug, nil).Code)
	path := "/api/v1/rooms/" + slug

	// Updating: the same rules, plus scheduledAt as a string or null.
	for _, c := range []struct {
		body map[string]any
		want string
	}{
		{map[string]any{"name": ""}, "name must be between 1 and 80 characters"},
		{map[string]any{"slug": "x"}, "slug must be 3-32 characters"},
		{map[string]any{"visibility": "hidden"}, "visibility must be public or private"},
		{map[string]any{"description": strings.Repeat("d", 301)}, "description must be at most 300 characters"},
		{map[string]any{"scheduledAt": 42}, "scheduledAt must be a string or null"},
	} {
		code, msg := errorOf(t, owner, http.MethodPatch, path, c.body)
		assert.Equal(t, http.StatusBadRequest, code, c.want)
		assert.Contains(t, msg, c.want)
	}

	// A valid change goes through: private, announced, then the
	// announcement withdrawn with null.
	at := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	rec = owner.do(http.MethodPatch, path, map[string]any{"visibility": "private", "scheduledAt": at.Format(time.RFC3339)})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeBody[roomResponse](t, rec)
	assert.Equal(t, "private", string(got.Visibility))
	require.NotNil(t, got.ScheduledAt)
	assert.True(t, got.ScheduledAt.Equal(at))
	rec = owner.do(http.MethodPatch, path, map[string]any{"scheduledAt": nil})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Nil(t, decodeBody[roomResponse](t, rec).ScheduledAt)
}
