package entity

import (
	"time"

	"uuid"
)

// Visibility controls who can open a room.
type Visibility string

const (
	VisibilityPublic  Visibility = "public"
	VisibilityPrivate Visibility = "private"
)

// RoomRole is a user's role inside one room.
type RoomRole string

const (
	RoomRoleOwner     RoomRole = "owner"
	RoomRoleModerator RoomRole = "moderator"
	RoomRoleMember    RoomRole = "member"
)

// IsModerator reports whether the role may control playback and moderate.
func (r RoomRole) IsModerator() bool { return r == RoomRoleOwner || r == RoomRoleModerator }

// Settings are the room options adjustable by moderators.
type Settings struct {
	VoteMode      bool    `json:"voteMode"`
	SkipThreshold float64 `json:"skipThreshold"`
	ViewersCanAdd bool    `json:"viewersCanAdd"`
}

// DefaultSettings is applied to new rooms.
func DefaultSettings() Settings {
	return Settings{VoteMode: false, SkipThreshold: 0.5, ViewersCanAdd: true}
}

// Room is a watch-together room. Playback state columns are read and
// written by the room manager (phase 5) and ignored elsewhere.
type Room struct {
	ID         uuid.UUID
	Slug       string
	Name       string
	OwnerID    uuid.UUID
	Visibility Visibility
	Settings   Settings

	CurrentItemID *uuid.UUID
	Playing       bool
	PositionMs    int64
	PositionAt    time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// IsPublic reports whether anonymous viewers may open the room.
func (r *Room) IsPublic() bool { return r != nil && r.Visibility == VisibilityPublic }

// RoomMember is a user's membership in a room.
type RoomMember struct {
	RoomID   uuid.UUID
	UserID   uuid.UUID
	Username string // populated by list queries
	Role     RoomRole
	JoinedAt time.Time
}

// RoomBan bars a user from a room. Independent of membership and invites:
// lifting the ban restores whatever access the user had.
type RoomBan struct {
	RoomID    uuid.UUID
	UserID    uuid.UUID
	Username  string // populated by list queries
	BannedBy  *uuid.UUID
	Reason    string
	CreatedAt time.Time
}

// InviteStatus is the lifecycle of an invite.
type InviteStatus string

const (
	InviteStatusPending  InviteStatus = "pending"
	InviteStatusAccepted InviteStatus = "accepted"
	InviteStatusDeclined InviteStatus = "declined"
)

// Invite asks a user to join a room. Accepting creates a member row.
type Invite struct {
	ID        uuid.UUID
	RoomID    uuid.UUID
	RoomSlug  string // populated by list queries
	RoomName  string // populated by list queries
	InviteeID uuid.UUID
	InviterID *uuid.UUID
	Inviter   string // username, populated by list queries
	Status    InviteStatus
	CreatedAt time.Time
}

// AuditEntry records a moderator or administrator action.
type AuditEntry struct {
	ID         int64
	ActorID    *uuid.UUID
	Action     string
	TargetType string
	TargetID   string
	RoomID     *uuid.UUID
	Meta       map[string]any
	CreatedAt  time.Time
}
