package entity

import (
	"time"

	"uuid"
)

// QueueItem is one entry in a room's playlist. Rank orders items; it is a
// zero-padded decimal string so lexicographic and numeric order agree.
type QueueItem struct {
	ID          uuid.UUID
	RoomID      uuid.UUID
	MediaID     uuid.UUID
	AddedBy     *uuid.UUID
	AddedByName string // populated by list queries
	Rank        string
	Votes       int // populated by list queries
	CreatedAt   time.Time
	PlayedAt    *time.Time // set once the item finished or was skipped
}

// PlaybackState is the authoritative position of a room, persisted on the
// rooms row and broadcast to viewers.
type PlaybackState struct {
	CurrentItemID *uuid.UUID
	Playing       bool
	PositionMs    int64
	PositionAt    time.Time
	Rate          float64
}
