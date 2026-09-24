package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/entity"
)

func TestPushSubscriptionsAndReminders(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	ann, err := repo.CreateUser(ctx, "Ann", "h")
	require.NoError(t, err)
	bob, err := repo.CreateUser(ctx, "bob", "h")
	require.NoError(t, err)

	// One endpoint per browser; saving it for another account moves it.
	require.NoError(t, repo.SavePushSubscription(ctx, ann.ID, "https://push/1", "p", "a", "ua"))
	require.NoError(t, repo.SavePushSubscription(ctx, ann.ID, "https://push/2", "p", "a", "ua"))
	require.NoError(t, repo.SavePushSubscription(ctx, bob.ID, "https://push/2", "p2", "a2", "ua"))
	subs, err := repo.ListPushSubscriptions(ctx, ann.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	subs, _ = repo.ListPushSubscriptions(ctx, bob.ID)
	require.Len(t, subs, 1)
	assert.Equal(t, "p2", subs[0].P256dh)
	require.NoError(t, repo.TouchPushSubscription(ctx, subs[0].ID, time.Now()))
	require.NoError(t, repo.DeletePushSubscription(ctx, ann.ID, "https://push/2"), "not ann's any more: no-op")
	subs, _ = repo.ListPushSubscriptions(ctx, bob.ID)
	assert.Len(t, subs, 1)
	require.NoError(t, repo.DeletePushEndpoint(ctx, "https://push/2"))
	subs, _ = repo.ListPushSubscriptions(ctx, bob.ID)
	assert.Empty(t, subs)

	ids, err := repo.UserIDsByUsername(ctx, []string{"ann", "bob", "nobody"})
	require.NoError(t, err)
	assert.Equal(t, ann.ID, ids["ann"], "case-insensitive")
	assert.Equal(t, bob.ID, ids["bob"])
	assert.Len(t, ids, 2)

	// A start within the lead is claimed once; a new time is claimed again.
	room, err := repo.CreateRoom(ctx, "friday", "Friday", ann.ID, entity.VisibilityPublic, entity.DefaultSettings())
	require.NoError(t, err)
	require.NoError(t, repo.UpsertMember(ctx, room.ID, bob.ID, entity.RoomRoleMember))
	now := time.Now()
	at := now.Add(8 * time.Minute)
	require.NoError(t, repo.SetRoomSchedule(ctx, room.ID, &at))
	due, err := repo.ClaimScheduleReminders(ctx, now, 10*time.Minute)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, "friday", due[0].Slug)
	assert.ElementsMatch(t, []any{ann.ID, bob.ID}, []any{due[0].Members[0], due[0].Members[1]})
	due, _ = repo.ClaimScheduleReminders(ctx, now, 10*time.Minute)
	assert.Empty(t, due, "already reminded")

	later := now.Add(30 * time.Minute)
	require.NoError(t, repo.SetRoomSchedule(ctx, room.ID, &later))
	due, _ = repo.ClaimScheduleReminders(ctx, now, 10*time.Minute)
	assert.Empty(t, due, "too far ahead")
	due, _ = repo.ClaimScheduleReminders(ctx, now.Add(25*time.Minute), 10*time.Minute)
	assert.Len(t, due, 1, "the new time is announced")
}
