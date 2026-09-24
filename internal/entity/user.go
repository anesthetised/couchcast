// Package entity holds the domain types shared by repositories, services and
// HTTP handlers. Types here carry no behaviour beyond simple predicates.
package entity

import (
	"time"

	"uuid"
)

// Role is a site-wide role. Room roles live in RoomMember.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// User is a registered account.
type User struct {
	ID           uuid.UUID
	Username     string
	PasswordHash string
	Role         Role
	BannedAt     *time.Time
	BannedReason *string
	BannedBy     *uuid.UUID
	CreatedAt    time.Time
	// AvatarColor is a palette key ("amber", "sky", …) or "" for the default.
	AvatarColor string
}

// AvatarColors is the palette users pick from; the web app maps keys to
// tokens so the same name renders the same everywhere.
var AvatarColors = []string{"amber", "coral", "rose", "violet", "sky", "teal", "lime", "slate"}

// ValidAvatarColor reports whether c is "" or a palette key.
func ValidAvatarColor(c string) bool {
	if c == "" {
		return true
	}
	for _, k := range AvatarColors {
		if k == c {
			return true
		}
	}
	return false
}

// IsAdmin reports whether the user is a site administrator.
func (u *User) IsAdmin() bool { return u != nil && u.Role == RoleAdmin }

// IsBanned reports whether the user is banned site-wide.
func (u *User) IsBanned() bool { return u != nil && u.BannedAt != nil }

// Session is a server-side login session. Only the hash of the token is
// stored; the token itself lives in the client's cookie.
type Session struct {
	TokenHash  []byte
	ID         uuid.UUID // public handle; the hash stays server-side
	UserID     uuid.UUID
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}
