package access

import (
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"

	"github.com/anesthetised/couchcast/internal/entity"
)

func TestCan(t *testing.T) {
	public := &entity.Room{Visibility: entity.VisibilityPublic, Settings: entity.DefaultSettings()}
	private := &entity.Room{Visibility: entity.VisibilityPrivate, Settings: entity.DefaultSettings()}
	noAdd := &entity.Room{Visibility: entity.VisibilityPublic, Settings: entity.Settings{ViewersCanAdd: false}}

	user := &entity.User{ID: uuid.New(), Role: entity.RoleUser}
	admin := &entity.User{ID: uuid.New(), Role: entity.RoleAdmin}
	now := time.Now()
	siteBanned := &entity.User{ID: uuid.New(), Role: entity.RoleUser, BannedAt: &now}

	member := func(r entity.RoomRole) *entity.RoomMember { return &entity.RoomMember{Role: r} }

	anon := Actor{}
	guest := Actor{User: user}
	mem := Actor{User: user, Member: member(entity.RoomRoleMember)}
	mod := Actor{User: user, Member: member(entity.RoomRoleModerator)}
	owner := Actor{User: user, Member: member(entity.RoomRoleOwner)}
	adm := Actor{User: admin}
	roomBanned := Actor{User: user, Member: member(entity.RoomRoleMember), Banned: true}
	siteBan := Actor{User: siteBanned, Member: member(entity.RoomRoleOwner)}

	all := []Action{ViewRoom, Chat, Vote, ReportMedia, AddToQueue, ControlPlayback, ManageQueue, ManageSettings,
		ModerateChat, Invite, BanMember, RemoveMember, ManageModerators, ManageRoom, DeleteRoom}

	// Bans override everything, even for owners.
	for _, action := range all {
		assert.False(t, Can(roomBanned, action, public), "room-banned %s", action)
		assert.False(t, Can(siteBan, action, public), "site-banned %s", action)
	}

	cases := []struct {
		name  string
		actor Actor
		room  *entity.Room
		allow []Action
	}{
		{"anonymous public", anon, public, []Action{ViewRoom}},
		{"anonymous private", anon, private, nil},
		{"guest public", guest, public, []Action{ViewRoom, Chat, Vote, ReportMedia, AddToQueue}},
		{"guest public no-add", guest, noAdd, []Action{ViewRoom, Chat, Vote, ReportMedia}},
		{"guest private", guest, private, nil},
		{"member private", mem, private, []Action{ViewRoom, Chat, Vote, ReportMedia, AddToQueue}},
		{"moderator", mod, private, []Action{ViewRoom, Chat, Vote, ReportMedia, AddToQueue, ControlPlayback, ManageQueue,
			ManageSettings, ModerateChat, Invite, BanMember, RemoveMember}},
		{"owner", owner, private, all},
		{"admin private", adm, private, []Action{ViewRoom, Chat, Vote, ReportMedia, AddToQueue, DeleteRoom}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			allowed := map[Action]bool{}
			for _, a := range tc.allow {
				allowed[a] = true
			}
			for _, action := range all {
				assert.Equal(t, allowed[action], Can(tc.actor, action, tc.room), "%s", action)
			}
		})
	}

	assert.False(t, Can(owner, ViewRoom, nil))
}

func TestCanTarget(t *testing.T) {
	room := &entity.Room{Visibility: entity.VisibilityPublic}
	user := &entity.User{ID: uuid.New()}
	mod := Actor{User: user, Member: &entity.RoomMember{Role: entity.RoomRoleModerator}}
	owner := Actor{User: user, Member: &entity.RoomMember{Role: entity.RoomRoleOwner}}
	member := Actor{User: user, Member: &entity.RoomMember{Role: entity.RoomRoleMember}}

	assert.True(t, CanTarget(mod, BanMember, room, entity.RoomRoleMember))
	assert.True(t, CanTarget(mod, BanMember, room, ""), "non-members can be banned too")
	assert.False(t, CanTarget(mod, BanMember, room, entity.RoomRoleModerator))
	assert.False(t, CanTarget(mod, BanMember, room, entity.RoomRoleOwner))

	assert.True(t, CanTarget(owner, BanMember, room, entity.RoomRoleModerator))
	assert.False(t, CanTarget(owner, BanMember, room, entity.RoomRoleOwner))
	assert.True(t, CanTarget(owner, RemoveMember, room, entity.RoomRoleModerator))

	assert.False(t, CanTarget(member, BanMember, room, entity.RoomRoleMember))
}
