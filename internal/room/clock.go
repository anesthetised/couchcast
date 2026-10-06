package room

import (
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/protocol"
)

// clock is the room's authoritative playback clock: the current item and,
// while playing, a position that runs at rate from positionAt. It is a
// plain value; the Room embeds it, guards it with its mutex and decides
// when it changes. Durations come in as arguments (0: unknown, no cap).
type clock struct {
	current    *uuid.UUID
	playing    bool
	positionMs int64
	positionAt time.Time
	rate       float64 // playback speed, 1 = normal
	seq        uint64  // bumped on every change, so clients and timers spot stale ones
}

// position returns the position at time t, held at the end of the media.
func (c clock) position(t time.Time, durationMs int64) int64 {
	if !c.playing {
		return c.positionMs
	}
	pos := c.positionMs + int64(float64(t.Sub(c.positionAt).Milliseconds())*c.rate)
	if durationMs > 0 && pos > durationMs {
		return durationMs
	}
	return pos
}

// restart restarts the clock from now at positionMs with the playing flag.
func (c *clock) restart(playing bool, positionMs int64, now time.Time) {
	c.playing = playing
	c.positionMs = positionMs
	c.positionAt = now
	c.seq++
}

// remaining is the wall time until the media ends at the current rate.
func (c clock) remaining(now time.Time, durationMs int64) time.Duration {
	left := time.Duration(float64(durationMs-c.position(now, durationMs))/c.rate) * time.Millisecond
	return max(left, 0)
}

// isCurrent reports whether the item is the current one.
func (c clock) isCurrent(itemID uuid.UUID) bool {
	return c.current != nil && *c.current == itemID
}

func (c clock) playback() protocol.Playback {
	return protocol.Playback{
		Type: protocol.TypePlayback, ItemID: c.current, Playing: c.playing,
		PositionMs: c.positionMs, AtServerMs: c.positionAt.UnixMilli(), Rate: c.rate, Seq: c.seq,
	}
}

func (c clock) state() entity.PlaybackState {
	return entity.PlaybackState{CurrentItemID: c.current, Playing: c.playing, PositionMs: c.positionMs, PositionAt: c.positionAt, Rate: c.rate}
}

// clockFrom restores the clock persisted with the room.
func clockFrom(info *entity.Room) clock {
	c := clock{current: info.CurrentItemID, playing: info.Playing, positionMs: info.PositionMs, positionAt: info.PositionAt, rate: info.Rate}
	if c.rate <= 0 {
		c.rate = 1
	}
	return c
}

// startFrom is where play resumes: where it stopped, or from the top once
// the media has played to its end.
func startFrom(positionMs, durationMs int64) int64 {
	if durationMs > 0 && positionMs >= durationMs {
		return 0
	}
	return positionMs
}

// clampSeek keeps a seek target inside the media.
func clampSeek(positionMs, durationMs int64) int64 {
	positionMs = max(positionMs, 0)
	if durationMs > 0 {
		positionMs = min(positionMs, durationMs)
	}
	return positionMs
}
