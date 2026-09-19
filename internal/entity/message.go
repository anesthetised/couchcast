package entity

import (
	"time"

	"uuid"
)

// Message is one chat line. Deleted messages keep their row (with
// deleted_at set) so moderation is auditable; they are never sent out.
type Message struct {
	ID        int64
	RoomID    uuid.UUID
	UserID    *uuid.UUID
	Username  string // populated by queries
	Body      string
	CreatedAt time.Time
	DeletedAt *time.Time
	DeletedBy *uuid.UUID
}
