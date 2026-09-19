package apihttp

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/ratelimit"
)

func TestRoomCreationLimit(t *testing.T) {
	handler := newDBHandler(t, func(d *Deps) { d.RoomCreateLimiter = ratelimit.PerHour(60, 2) })
	owner := &dbEnv{&testEnv{t: t, handler: handler}}
	owner.register("owner")

	assert.Equal(t, http.StatusCreated, owner.do(http.MethodPost, "/api/v1/rooms", createRoomRequest{Name: "one"}).Code)
	assert.Equal(t, http.StatusCreated, owner.do(http.MethodPost, "/api/v1/rooms", createRoomRequest{Name: "two"}).Code)
	rec := owner.do(http.MethodPost, "/api/v1/rooms", createRoomRequest{Name: "three"})
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "60", rec.Header().Get("Retry-After"))

	// Budgets are per user.
	other := &dbEnv{&testEnv{t: t, handler: handler}}
	other.register("other")
	assert.Equal(t, http.StatusCreated, other.do(http.MethodPost, "/api/v1/rooms", createRoomRequest{Name: "mine"}).Code)
}
