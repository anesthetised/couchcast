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
	TypePing             = "ping"
	TypePlay             = "play"
	TypePause            = "pause"
	TypeSeek             = "seek"
	TypeNext             = "next"
	TypeJump             = "jump"
	TypeQueueAdd         = "queue.add"
	TypeQueueRemove      = "queue.remove"
	TypeQueueMove        = "queue.move"
	TypeQueueRetry       = "queue.retry"
	TypeQueueVote        = "queue.vote"
	TypeQueueReplay      = "queue.replay"
	TypeQueueClearPlayed = "queue.clearPlayed"
	TypeQueueClear       = "queue.clear"
	TypeQueueShuffle     = "queue.shuffle"
	TypeQueueAddMany     = "queue.addMany"
	TypeSkipVote         = "skip.vote"
	TypeSettingsSet      = "settings.set"
	TypeSessionEnd       = "session.end"
	TypeRateSet          = "rate.set"
	TypeChatSend         = "chat.send"
	TypeChatDelete       = "chat.delete"
	TypeChatEdit         = "chat.edit"
	TypeChatClear        = "chat.clear"
	TypeChatPin          = "chat.pin"
	TypeChatUnpin        = "chat.unpin"
	TypeChatTyping       = "chat.typing"
	TypeReact            = "react"
	TypeReport           = "report"
)

// Envelope is the first pass of decoding: only the type.
type Envelope struct {
	Type string `json:"type"`
	// Ref is an optional number the client picks per command; an error
	// caused by the command echoes it, so the client knows what failed.
	Ref int64 `json:"ref,omitempty"`
}

// Ping measures clock offset; T0 is the client's send time in unix ms.
type Ping struct {
	T0 int64 `json:"t0"`
}

// Seek moves playback to an absolute position.
type Seek struct {
	PositionMs int64 `json:"positionMs"`
}

// RateSet changes the playback speed for everyone.
type RateSet struct {
	Rate float64 `json:"rate"`
}

// ItemRef names a queue item.
type ItemRef struct {
	ItemID uuid.UUID `json:"itemId"`
}

// QueueAdd enqueues a URL; Next places it right after the current item
// (ignored in vote mode).
type QueueAdd struct {
	URL  string `json:"url"`
	Next bool   `json:"next,omitempty"`
	// Force adds a video that is already queued or in the history; without
	// it the server answers CodeDuplicate and the client asks first.
	Force bool `json:"force,omitempty"`
}

// Play resumes playback; Countdown shows everyone 3-2-1 first (a room
// with an announced session always counts down).
type Play struct {
	Countdown bool `json:"countdown,omitempty"`
}

// QueueAddMany enqueues several URLs in order (a playlist import); links
// the room already has are skipped.
type QueueAddMany struct {
	URLs []string `json:"urls"`
	Next bool     `json:"next,omitempty"`
}

// QueueMove places ItemID right after AfterID, or at the head when
// AfterID is nil.
type QueueMove struct {
	ItemID  uuid.UUID  `json:"itemId"`
	AfterID *uuid.UUID `json:"afterId"`
}

// SettingsSet changes room settings; nil fields are left untouched.
type SettingsSet struct {
	VoteMode         *bool    `json:"voteMode"`
	SkipThreshold    *float64 `json:"skipThreshold"`
	ViewersCanAdd    *bool    `json:"viewersCanAdd"`
	Loop             *bool    `json:"loop"`
	SlowModeSec      *int     `json:"slowModeSec"`
	PauseWhenEmpty   *bool    `json:"pauseWhenEmpty"`
	WaitForBuffering *bool    `json:"waitForBuffering"`
	FairQueue        *bool    `json:"fairQueue"`
}

// React sends an ephemeral emoji reaction.
type React struct {
	Emoji string `json:"emoji"`
}

// ChatSend posts a message, optionally answering another one.
type ChatSend struct {
	Body    string `json:"body"`
	ReplyTo *int64 `json:"replyTo,omitempty"`
}

// ChatEdit rewrites the author's own recent message.
type ChatEdit struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

// ChatDelete removes a message: one's own, or anyone's for moderators.
type ChatDelete struct {
	ID int64 `json:"id"`
}

// ChatPin pins a message above the chat (moderators).
type ChatPin struct {
	ID int64 `json:"id"`
}

// Report is client telemetry: "playing", "buffering" or "ended".
type Report struct {
	State      string `json:"state"`
	PositionMs int64  `json:"positionMs"`
}

// Decode parses a client message into its typed form.
func Decode(data []byte) (string, any, error) {
	env, msg, err := DecodeEnvelope(data)
	return env.Type, msg, err
}

// DecodeEnvelope is Decode that also returns the envelope (with Ref).
func DecodeEnvelope(data []byte) (Envelope, any, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return env, nil, fmt.Errorf("protocol: %w", err)
	}

	var msg any
	switch env.Type {
	case TypePing:
		msg = &Ping{}
	case TypePlay:
		msg = &Play{}
	case TypePause, TypeNext, TypeSkipVote, TypeQueueClearPlayed, TypeQueueClear, TypeQueueShuffle, TypeSessionEnd, TypeChatTyping, TypeChatClear, TypeChatUnpin:
		msg = nil
	case TypeSeek:
		msg = &Seek{}
	case TypeRateSet:
		msg = &RateSet{}
	case TypeJump, TypeQueueRemove, TypeQueueRetry, TypeQueueVote, TypeQueueReplay:
		msg = &ItemRef{}
	case TypeQueueAdd:
		msg = &QueueAdd{}
	case TypeQueueAddMany:
		msg = &QueueAddMany{}
	case TypeQueueMove:
		msg = &QueueMove{}
	case TypeSettingsSet:
		msg = &SettingsSet{}
	case TypeChatSend:
		msg = &ChatSend{}
	case TypeChatDelete:
		msg = &ChatDelete{}
	case TypeChatEdit:
		msg = &ChatEdit{}
	case TypeChatPin:
		msg = &ChatPin{}
	case TypeReact:
		msg = &React{}
	case TypeReport:
		msg = &Report{}
	default:
		return env, nil, fmt.Errorf("protocol: unknown message type %q", env.Type)
	}

	if msg != nil {
		if err := json.Unmarshal(data, msg); err != nil {
			return env, nil, fmt.Errorf("protocol: %s: %w", env.Type, err)
		}
	}
	return env, msg, nil
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
	TypeChatEdited  = "chat.edited"
	TypeChatCleared = "chat.cleared"
	TypeChatPinned  = "chat.pinned"
	TypeTyping      = "typing"
	TypeReaction    = "reaction"
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
	SpeedBps     int64              `json:"speedBps,omitempty"`
	EtaMs        int64              `json:"etaMs,omitempty"`
	Error        string             `json:"error,omitempty"`
	Renditions   []entity.Rendition `json:"renditions"`
	Subtitles    []entity.Subtitle  `json:"subtitles,omitempty"`
	Chapters     []entity.Chapter   `json:"chapters,omitempty"`
	Storyboard   *entity.Storyboard `json:"storyboard,omitempty"`
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
	// PlayedMs is set on history entries only.
	PlayedMs int64 `json:"playedMs,omitempty"`
}

// Presence is one connected viewer.
type Presence struct {
	Username  string          `json:"username"`
	Color     string          `json:"color,omitempty"` // avatar palette key
	Role      entity.RoomRole `json:"role,omitempty"`
	Buffering bool            `json:"buffering"`
	// LagMs is how far this viewer's video is from the room clock
	// (positive: behind), in half-second steps; 0 within a second.
	LagMs int64 `json:"lagMs,omitempty"`
}

// RoomInfo is the static part of the room.
type RoomInfo struct {
	ID          uuid.UUID         `json:"id"`
	Slug        string            `json:"slug"`
	Name        string            `json:"name"`
	Visibility  entity.Visibility `json:"visibility"`
	Settings    entity.Settings   `json:"settings"`
	Owner       string            `json:"owner"`
	Description string            `json:"description,omitempty"`
	// Pinned is the message a moderator pinned above the chat.
	Pinned *ChatMessage `json:"pinned,omitempty"`
	// ScheduledMs is the announced start, cleared once playback starts.
	ScheduledMs int64 `json:"scheduledMs,omitempty"`
}

// Snapshot is the full room state.
type Snapshot struct {
	Type       string       `json:"type"`
	Room       RoomInfo     `json:"room"`
	Playback   Playback     `json:"playback"`
	Queue      []QueueEntry `json:"queue"`
	Played     []QueueEntry `json:"played"` // newest first
	Members    []Presence   `json:"members"`
	Guests     int          `json:"guests"`
	SkipVotes  int          `json:"skipVotes"`
	SkipVoted  bool         `json:"skipVoted"`
	SkipNeeded int          `json:"skipNeeded"`
	// Waiting lists the viewers the room paused for (WaitForBuffering).
	Waiting []string `json:"waiting,omitempty"`
	// CountdownMs is the server time a counted-down start begins at.
	CountdownMs int64 `json:"countdownMs,omitempty"`
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
	EditedMs  int64  `json:"editedMs,omitempty"`
	ReplyTo   *Quote `json:"replyTo,omitempty"`
}

// Quote is the replied-to message as shown with a reply.
type Quote struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Body     string `json:"body"`
}

// ChatPinned announces the pinned message; nil clears it.
type ChatPinned struct {
	Type    string       `json:"type"`
	Message *ChatMessage `json:"message"`
}

// ChatDeleted announces a removed message.
type ChatDeleted struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
}

// ChatEdited carries a message whose body the author changed.
type ChatEdited struct {
	Type    string      `json:"type"`
	Message ChatMessage `json:"message"`
}

// ChatCleared tells clients to drop every message they hold.
type ChatCleared struct {
	Type string `json:"type"`
}

// Typing says a user is composing; nothing is stored.
type Typing struct {
	Type     string `json:"type"`
	Username string `json:"username"`
}

// Reaction is an ephemeral emoji from a viewer.
type Reaction struct {
	Type     string `json:"type"`
	Username string `json:"username"`
	Emoji    string `json:"emoji"`
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
	Ref     int64  `json:"ref,omitempty"` // the failed command's Ref
}

// Error codes.
const (
	CodeForbidden = "forbidden"
	CodeInvalid   = "invalid"
	CodeNotFound  = "not_found"
	CodeInternal  = "internal"
	CodeRateLimit = "rate_limited"
	CodeDuplicate = "duplicate"
)
