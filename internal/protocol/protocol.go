// Package protocol defines the JSON messages exchanged over the room
// WebSocket. Every message carries a "type" field; the remaining fields
// depend on it. web/src/protocol.ts mirrors these types.
package protocol

import (
	"encoding/json"
	"fmt"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
)

// --- client → server ---------------------------------------------------------

// Client message types.
const (
	TypePing        = "ping"
	TypePlay        = "play"
	TypePause       = "pause"
	TypeSeek        = "seek"
	TypeNext        = "next"
	TypeJump        = "jump"
	TypeQueueAdd    = "queue.add"
	TypeQueueRemove = "queue.remove"
	TypeQueueMove   = "queue.move"
	TypeQueueRetry  = "queue.retry"
	TypeQueueVote   = "queue.vote"
	TypeSkipVote    = "skip.vote"
	TypeSettingsSet = "settings.set"
	TypeChatSend    = "chat.send"
	TypeChatDelete  = "chat.delete"
	TypeReport      = "report"
)

// Envelope is the first pass of decoding: only the type.
type Envelope struct {
	Type string `json:"type"`
}

// Ping measures clock offset; T0 is the client's send time in unix ms.
type Ping struct {
	T0 int64 `json:"t0"`
}

// Seek moves playback to an absolute position.
type Seek struct {
	PositionMs int64 `json:"positionMs"`
}

// ItemRef names a queue item.
type ItemRef struct {
	ItemID uuid.UUID `json:"itemId"`
}

// QueueAdd enqueues a URL.
type QueueAdd struct {
	URL string `json:"url"`
}

// QueueMove places ItemID right after AfterID, or at the head when
// AfterID is nil.
type QueueMove struct {
	ItemID  uuid.UUID  `json:"itemId"`
	AfterID *uuid.UUID `json:"afterId"`
}

// SettingsSet changes room settings; nil fields are left untouched.
type SettingsSet struct {
	VoteMode      *bool    `json:"voteMode"`
	SkipThreshold *float64 `json:"skipThreshold"`
	ViewersCanAdd *bool    `json:"viewersCanAdd"`
}

// ChatSend posts a message.
type ChatSend struct {
	Body string `json:"body"`
}

// ChatDelete removes a message (moderators).
type ChatDelete struct {
	ID int64 `json:"id"`
}

// Report is client telemetry: "playing", "buffering" or "ended".
type Report struct {
	State      string `json:"state"`
	PositionMs int64  `json:"positionMs"`
}

// Decode parses a client message into its typed form.
func Decode(data []byte) (string, any, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return "", nil, fmt.Errorf("protocol: %w", err)
	}

	var msg any
	switch env.Type {
	case TypePing:
		msg = &Ping{}
	case TypePlay, TypePause, TypeNext, TypeSkipVote:
		msg = nil
	case TypeSeek:
		msg = &Seek{}
	case TypeJump, TypeQueueRemove, TypeQueueRetry, TypeQueueVote:
		msg = &ItemRef{}
	case TypeQueueAdd:
		msg = &QueueAdd{}
	case TypeQueueMove:
		msg = &QueueMove{}
	case TypeSettingsSet:
		msg = &SettingsSet{}
	case TypeChatSend:
		msg = &ChatSend{}
	case TypeChatDelete:
		msg = &ChatDelete{}
	case TypeReport:
		msg = &Report{}
	default:
		return env.Type, nil, fmt.Errorf("protocol: unknown message type %q", env.Type)
	}

	if msg != nil {
		if err := json.Unmarshal(data, msg); err != nil {
			return env.Type, nil, fmt.Errorf("protocol: %s: %w", env.Type, err)
		}
	}
	return env.Type, msg, nil
}

// --- server → client ---------------------------------------------------------

// Server message types.
const (
	TypeWelcome     = "welcome"
	TypePong        = "pong"
	TypeRoomState   = "room.state"
	TypePlayback    = "playback"
	TypeChatMessage = "chat.message"
	TypeChatDeleted = "chat.deleted"
	TypeKicked      = "kicked"
	TypeError       = "error"
)

// Pong answers Ping with the server receive time in unix ms.
type Pong struct {
	Type string `json:"type"`
	T0   int64  `json:"t0"`
	T1   int64  `json:"t1"`
}

// Playback is the authoritative clock. Position at server time t is
// PositionMs + (t - AtServerMs) * Rate while Playing.
type Playback struct {
	Type       string     `json:"type,omitempty"`
	ItemID     *uuid.UUID `json:"itemId"`
	Playing    bool       `json:"playing"`
	PositionMs int64      `json:"positionMs"`
	AtServerMs int64      `json:"atServerMs"`
	Rate       float64    `json:"rate"`
	Seq        uint64     `json:"seq"`
}

// MediaInfo is the viewer-facing view of a media row.
type MediaInfo struct {
	ID           uuid.UUID          `json:"id"`
	Title        string             `json:"title"`
	DurationMs   int64              `json:"durationMs"`
	ThumbnailURL string             `json:"thumbnailUrl"`
	Status       entity.MediaStatus `json:"status"`
	Progress     float32            `json:"progress"`
	Error        string             `json:"error,omitempty"`
	Renditions   []entity.Rendition `json:"renditions"`
	Manifest     string             `json:"manifest,omitempty"` // path, ready media only
	Token        string             `json:"token,omitempty"`    // append as ?t=
	SourceURL    string             `json:"sourceUrl"`
}

// QueueEntry is a queue item with its media.
type QueueEntry struct {
	ID      uuid.UUID `json:"id"`
	Media   MediaInfo `json:"media"`
	AddedBy string    `json:"addedBy"`
	Votes   int       `json:"votes"`
	Voted   bool      `json:"voted"` // by the receiving viewer
	Current bool      `json:"current"`
}

// Presence is one connected viewer.
type Presence struct {
	Username  string          `json:"username"`
	Color     string          `json:"color,omitempty"` // avatar palette key
	Role      entity.RoomRole `json:"role,omitempty"`
	Buffering bool            `json:"buffering"`
}

// RoomInfo is the static part of the room.
type RoomInfo struct {
	ID         uuid.UUID         `json:"id"`
	Slug       string            `json:"slug"`
	Name       string            `json:"name"`
	Visibility entity.Visibility `json:"visibility"`
	Settings   entity.Settings   `json:"settings"`
	Owner      string            `json:"owner"`
}

// Snapshot is the full room state.
type Snapshot struct {
	Type       string       `json:"type"`
	Room       RoomInfo     `json:"room"`
	Playback   Playback     `json:"playback"`
	Queue      []QueueEntry `json:"queue"`
	Members    []Presence   `json:"members"`
	Guests     int          `json:"guests"`
	SkipVotes  int          `json:"skipVotes"`
	SkipVoted  bool         `json:"skipVoted"`
	SkipNeeded int          `json:"skipNeeded"`
}

// Welcome is the first message after connecting.
type Welcome struct {
	Type     string          `json:"type"`
	Me       *string         `json:"me"` // username, nil for anonymous
	Role     entity.RoomRole `json:"role,omitempty"`
	Snapshot Snapshot        `json:"snapshot"`
	Messages []ChatMessage   `json:"messages"`
}

// ChatMessage is one chat line.
type ChatMessage struct {
	Type      string `json:"type,omitempty"`
	ID        int64  `json:"id"`
	Username  string `json:"username,omitempty"`
	Color     string `json:"color,omitempty"` // author's avatar palette key
	Body      string `json:"body"`
	System    bool   `json:"system,omitempty"`
	CreatedMs int64  `json:"createdMs"`
}

// ChatDeleted announces a removed message.
type ChatDeleted struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
}

// Kicked tells the client its connection is being closed on purpose.
type Kicked struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

// Error reports a rejected command.
type Error struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error codes.
const (
	CodeForbidden = "forbidden"
	CodeInvalid   = "invalid"
	CodeNotFound  = "not_found"
	CodeInternal  = "internal"
	CodeRateLimit = "rate_limited"
)
