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
	require.NoError(t, repo.CreateSession(ctx, hash, u.ID, now.Add(time.Hour)))

	s, got, err := repo.GetSessionUser(ctx, hash, now)
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
	assert.Equal(t, hash, s.TokenHash)

	// Expired sessions are invisible.
	_, _, err = repo.GetSessionUser(ctx, hash, now.Add(2*time.Hour))
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, repo.TouchSession(ctx, hash, now.Add(time.Hour), now.Add(3*time.Hour)))
	_, _, err = repo.GetSessionUser(ctx, hash, now.Add(2*time.Hour))
	require.NoError(t, err)

	require.NoError(t, repo.CreateSession(ctx, []byte("token-hash-2"), u.ID, now.Add(time.Hour)))
	n, err := repo.DeleteUserSessions(ctx, u.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)

	_, _, err = repo.GetSessionUser(ctx, hash, now)
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, repo.CreateSession(ctx, []byte("old"), u.ID, now.Add(-time.Minute)))
	n, err = repo.DeleteExpiredSessions(ctx, now)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
}
