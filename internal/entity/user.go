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
}

// IsAdmin reports whether the user is a site administrator.
func (u *User) IsAdmin() bool { return u != nil && u.Role == RoleAdmin }

// IsBanned reports whether the user is banned site-wide.
func (u *User) IsBanned() bool { return u != nil && u.BannedAt != nil }

// Session is a server-side login session. Only the hash of the token is
// stored; the token itself lives in the client's cookie.
type Session struct {
	TokenHash  []byte
	UserID     uuid.UUID
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}
