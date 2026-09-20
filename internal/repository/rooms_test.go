package repository

import (
	"context"
	"testing"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/entity"
)

func TestRoomsMembersBans(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	owner, err := repo.CreateUser(ctx, "owner", "h")
	require.NoError(t, err)
	bob, err := repo.CreateUser(ctx, "bob", "h")
	require.NoError(t, err)

	room, err := repo.CreateRoom(ctx, "movie-night", "Movie night", owner.ID, entity.VisibilityPrivate, entity.DefaultSettings())
	require.NoError(t, err)
	assert.Equal(t, 0.5, room.Settings.SkipThreshold)

	_, err = repo.CreateRoom(ctx, "movie-night", "dup", owner.ID, entity.VisibilityPublic, entity.DefaultSettings())
	assert.ErrorIs(t, err, ErrConflict)

	// Owner membership was created in the same transaction.
	m, err := repo.GetMember(ctx, room.ID, owner.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.RoomRoleOwner, m.Role)
	assert.Equal(t, "owner", m.Username)

	_, err = repo.GetMember(ctx, room.ID, bob.ID)
	assert.ErrorIs(t, err, ErrNotFound)

	// Promote bob straight to moderator (adds membership).
	require.NoError(t, repo.UpsertMember(ctx, room.ID, bob.ID, entity.RoomRoleModerator))
	members, err := repo.ListMembers(ctx, room.ID)
	require.NoError(t, err)
	require.Len(t, members, 2)
	assert.Equal(t, entity.RoomRoleOwner, members[0].Role)
	assert.Equal(t, "bob", members[1].Username)

	// Owner row cannot be demoted or removed through the member paths.
	require.NoError(t, repo.UpsertMember(ctx, room.ID, owner.ID, entity.RoomRoleMember))
	m, err = repo.GetMember(ctx, room.ID, owner.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.RoomRoleOwner, m.Role)
	assert.ErrorIs(t, repo.DeleteMember(ctx, room.ID, owner.ID), ErrNotFound)

	// Bans are independent of membership.
	require.NoError(t, repo.CreateBan(ctx, room.ID, bob.ID, owner.ID, "rude"))
	ban, err := repo.GetBan(ctx, room.ID, bob.ID)
	require.NoError(t, err)
	assert.Equal(t, "rude", ban.Reason)
	m, err = repo.GetMember(ctx, room.ID, bob.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.RoomRoleModerator, m.Role, "membership survives the ban")

	bans, err := repo.ListBans(ctx, room.ID)
	require.NoError(t, err)
	assert.Len(t, bans, 1)

	require.NoError(t, repo.DeleteBan(ctx, room.ID, bob.ID))
	assert.ErrorIs(t, repo.DeleteBan(ctx, room.ID, bob.ID), ErrNotFound)

	// Update and settings.
	updated, err := repo.UpdateRoom(ctx, room.ID, "film-night", "Film night", entity.VisibilityPublic, "Fridays")
	require.NoError(t, err)
	assert.Equal(t, "film-night", updated.Slug)
	assert.Equal(t, "Fridays", updated.Description)
	require.NoError(t, repo.UpdateRoomSettings(ctx, room.ID, entity.Settings{VoteMode: true, SkipThreshold: 0.7}))
	got, err := repo.GetRoomBySlug(ctx, "film-night")
	require.NoError(t, err)
	assert.True(t, got.Settings.VoteMode)

	rooms, err := repo.ListRoomsForUser(ctx, bob.ID)
	require.NoError(t, err)
	require.Len(t, rooms, 1)
	assert.Equal(t, entity.RoomRoleModerator, rooms[0].Role)

	n, err := repo.CountMembers(ctx, room.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	require.NoError(t, repo.DeleteMember(ctx, room.ID, bob.ID))
	require.NoError(t, repo.DeleteRoom(ctx, room.ID))
	_, err = repo.GetRoomByID(ctx, room.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = repo.GetMember(ctx, room.ID, owner.ID)
	assert.ErrorIs(t, err, ErrNotFound, "memberships cascade")
}

func TestInvites(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	owner, _ := repo.CreateUser(ctx, "owner", "h")
	carol, _ := repo.CreateUser(ctx, "carol", "h")
	room, err := repo.CreateRoom(ctx, "secret", "Secret", owner.ID, entity.VisibilityPrivate, entity.DefaultSettings())
	require.NoError(t, err)

	inv, err := repo.CreateInvite(ctx, room.ID, carol.ID, owner.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.InviteStatusPending, inv.Status)
	assert.Equal(t, "owner", inv.Inviter)
	assert.Equal(t, "secret", inv.RoomSlug)

	_, err = repo.CreateInvite(ctx, room.ID, carol.ID, owner.ID)
	assert.ErrorIs(t, err, ErrConflict, "pending invite cannot be duplicated")

	pending, err := repo.ListPendingInvites(ctx, carol.ID)
	require.NoError(t, err)
	assert.Len(t, pending, 1)

	// Decline, then re-invite resets to pending.
	require.NoError(t, repo.DeclineInvite(ctx, inv.ID))
	assert.ErrorIs(t, repo.DeclineInvite(ctx, inv.ID), ErrNotFound)
	inv2, err := repo.CreateInvite(ctx, room.ID, carol.ID, owner.ID)
	require.NoError(t, err)
	assert.Equal(t, inv.ID, inv2.ID)
	assert.Equal(t, entity.InviteStatusPending, inv2.Status)

	// Accept adds membership.
	require.NoError(t, repo.AcceptInvite(ctx, inv2.ID))
	m, err := repo.GetMember(ctx, room.ID, carol.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.RoomRoleMember, m.Role)
	assert.ErrorIs(t, repo.AcceptInvite(ctx, inv2.ID), ErrNotFound, "already accepted")
	inv3, err := repo.CreateInvite(ctx, room.ID, carol.ID, owner.ID)
	require.NoError(t, err, "accepted invite can be re-issued after the member left")
	assert.Equal(t, entity.InviteStatusPending, inv3.Status)

	_, err = repo.GetInvite(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, repo.RecordAudit(ctx, entity.AuditEntry{
		ActorID: &owner.ID, Action: "invite.create", TargetType: "user", TargetID: carol.ID.String(), RoomID: &room.ID,
	}))
}
