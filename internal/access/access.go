// Package access is the single place where room permissions are decided.
// Handlers and the WebSocket hub build an Actor and ask Can; nothing else
// inspects roles.
package access

import "github.com/anesthetised/couchcast/internal/entity"

// Action is something a user may try to do in a room.
type Action string

const (
	ViewRoom         Action = "room.view"
	Chat             Action = "chat.send"
	Vote             Action = "vote"
	ReportMedia      Action = "media.report"
	AddToQueue       Action = "queue.add"
	ControlPlayback  Action = "playback.control"
	ManageQueue      Action = "queue.manage"
	ManageSettings   Action = "settings.manage"
	ModerateChat     Action = "chat.moderate"
	Invite           Action = "invite"
	BanMember        Action = "member.ban"
	RemoveMember     Action = "member.remove"
	ManageModerators Action = "moderators.manage"
	ManageRoom       Action = "room.manage"
	DeleteRoom       Action = "room.delete"
)

// Actor is who is asking. User is nil for anonymous viewers; Member is nil
// when the user has no membership row; Banned is the room ban flag.
type Actor struct {
	User   *entity.User
	Member *entity.RoomMember
	Banned bool
}

// Role returns the actor's room role, or "" for non-members.
func (a Actor) Role() entity.RoomRole {
	if a.Member == nil {
		return ""
	}
	return a.Member.Role
}

func (a Actor) isUser() bool  { return a.User != nil && !a.User.IsBanned() }
func (a Actor) isAdmin() bool { return a.isUser() && a.User.IsAdmin() }
func (a Actor) isMod() bool   { return a.isUser() && a.Role().IsModerator() }
func (a Actor) isOwner() bool { return a.isUser() && a.Role() == entity.RoomRoleOwner }

// Can reports whether the actor may perform the action in the room.
// Check order: site ban, room ban, then role and visibility.
func Can(a Actor, action Action, room *entity.Room) bool {
	if room == nil || (a.User != nil && a.User.IsBanned()) || a.Banned {
		return false
	}

	canView := room.IsPublic() || a.Member != nil || a.isAdmin()

	switch action {
	case ViewRoom:
		return canView
	case Chat, Vote, ReportMedia:
		return a.isUser() && canView
	case AddToQueue:
		return a.isMod() || (a.isUser() && canView && room.Settings.ViewersCanAdd)
	case ControlPlayback, ManageQueue, ManageSettings, ModerateChat, Invite, BanMember, RemoveMember:
		return a.isMod()
	case ManageModerators, ManageRoom:
		return a.isOwner()
	case DeleteRoom:
		return a.isOwner() || a.isAdmin()
	default:
		return false
	}
}

// CanTarget is Can for actions aimed at another member: moderators may act
// on plain members, only the owner may act on moderators, and nobody may
// act on the owner.
func CanTarget(a Actor, action Action, room *entity.Room, target entity.RoomRole) bool {
	if !Can(a, action, room) {
		return false
	}
	switch target {
	case entity.RoomRoleOwner:
		return false
	case entity.RoomRoleModerator:
		return a.isOwner()
	default:
		return true
	}
}
