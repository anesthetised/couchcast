package repository

import (
	"context"
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/entity"
)

func TestUsers(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	u, err := repo.CreateUser(ctx, "alice", "hash")
	require.NoError(t, err)
	assert.Equal(t, entity.RoleUser, u.Role)
	assert.False(t, u.IsBanned())

	_, err = repo.CreateUser(ctx, "Alice", "hash")
	assert.ErrorIs(t, err, ErrConflict, "uniqueness ignores case")

	got, err := repo.GetUserByUsername(ctx, "ALICE")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)

	_, err = repo.GetUserByID(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, repo.SetUserRole(ctx, u.ID, entity.RoleAdmin))
	got, err = repo.GetUserByID(ctx, u.ID)
	require.NoError(t, err)
	assert.True(t, got.IsAdmin())

	admin := got
	require.NoError(t, repo.BanUser(ctx, u.ID, admin.ID, "spam", time.Now()))
	got, err = repo.GetUserByID(ctx, u.ID)
	require.NoError(t, err)
	assert.True(t, got.IsBanned())
	assert.Equal(t, "spam", *got.BannedReason)

	require.NoError(t, repo.UnbanUser(ctx, u.ID))
	got, err = repo.GetUserByID(ctx, u.ID)
	require.NoError(t, err)
	assert.False(t, got.IsBanned())

	assert.ErrorIs(t, repo.SetUserRole(ctx, uuid.New(), entity.RoleUser), ErrNotFound)
}

func TestSessions(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)

	u, err := repo.CreateUser(ctx, "bob", "hash")
	require.NoError(t, err)

	hash := []byte("token-hash-1")
	require.NoError(t, repo.CreateSession(ctx, hash, u.ID, "Firefox", now.Add(time.Hour)))

	s, got, err := repo.GetSessionUser(ctx, hash, now)
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
	assert.Equal(t, hash, s.TokenHash)
	assert.Equal(t, "Firefox", s.UserAgent)
	assert.NotEqual(t, uuid.Nil(), s.ID)

	// Expired sessions are invisible.
	_, _, err = repo.GetSessionUser(ctx, hash, now.Add(2*time.Hour))
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, repo.TouchSession(ctx, hash, now.Add(time.Hour), now.Add(3*time.Hour)))
	_, _, err = repo.GetSessionUser(ctx, hash, now.Add(2*time.Hour))
	require.NoError(t, err)

	require.NoError(t, repo.CreateSession(ctx, []byte("token-hash-2"), u.ID, "", now.Add(time.Hour)))
	other, _ := repo.CreateUser(ctx, "eve", "hash")

	// The list shows live sessions, most recently seen first; one is
	// removed by its public id (only by its owner), or all but one.
	list, err := repo.ListUserSessions(ctx, u.ID, now)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, hash, list[0].TokenHash, "touched later")
	assert.ErrorIs(t, repo.DeleteUserSession(ctx, other.ID, list[1].ID), ErrNotFound)
	require.NoError(t, repo.DeleteUserSession(ctx, u.ID, list[1].ID))
	assert.ErrorIs(t, repo.DeleteUserSession(ctx, u.ID, list[1].ID), ErrNotFound)
	require.NoError(t, repo.CreateSession(ctx, []byte("token-hash-3"), u.ID, "", now.Add(time.Hour)))
	n, err := repo.DeleteOtherSessions(ctx, u.ID, hash)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	require.NoError(t, repo.CreateSession(ctx, []byte("token-hash-2"), u.ID, "", now.Add(time.Hour)))
	n, err = repo.DeleteUserSessions(ctx, u.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)

	_, _, err = repo.GetSessionUser(ctx, hash, now)
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, repo.CreateSession(ctx, []byte("old"), u.ID, "", now.Add(-time.Minute)))
	n, err = repo.DeleteExpiredSessions(ctx, now)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
}

func TestUserProfileWrites(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	alice, err := repo.CreateUser(ctx, "Alice", "h1")
	require.NoError(t, err)
	for _, n := range []string{"alfred", "Albert", "al_x", "alzheimer", "bob"} {
		_, err := repo.CreateUser(ctx, n, "h")
		require.NoError(t, err)
	}
	banned, err := repo.CreateUser(ctx, "alarmed", "h")
	require.NoError(t, err)
	require.NoError(t, repo.BanUser(ctx, banned.ID, alice.ID, "spam", time.Now()))

	require.NoError(t, repo.SetUserAvatarColor(ctx, alice.ID, "teal"))
	require.NoError(t, repo.SetUserPassword(ctx, alice.ID, "h2"))
	got, err := repo.GetUserByID(ctx, alice.ID)
	require.NoError(t, err)
	assert.Equal(t, "teal", got.AvatarColor)
	assert.Equal(t, "h2", got.PasswordHash)

	// Prefix search ignores case and banned accounts; _ is literal.
	names, err := repo.SearchUsernames(ctx, "AL", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"al_x", "Albert", "alfred", "Alice", "alzheimer"}, names)
	names, err = repo.SearchUsernames(ctx, "al_", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"al_x"}, names)
	names, err = repo.SearchUsernames(ctx, "al", 2)
	require.NoError(t, err)
	assert.Len(t, names, 2)
	require.NoError(t, repo.Ping(ctx))
}
