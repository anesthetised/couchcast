package room

import (
	"context"
	"time"

	"uuid"
)

// timers are every timer a Room owns. The rules, which keep a timer from
// acting on a room that moved on or was dropped:
//
//   - timers are armed, replaced and stopped only under the room's mutex;
//   - every callback is armed through Room.afterLocked, so it runs under
//     the mutex and not at all once the room is closed;
//   - a callback may still run after its timer was stopped or replaced
//     (it fired just before), so it re-checks the state it acts on: the
//     item and seq for advance, a pending countdown, an empty room...;
//   - closeLocked marks the room closed and stops them all (stopAll), so
//     an unloaded room has nothing scheduled.
type timers struct {
	advance   *time.Timer // the current item's end (scheduleAdvanceLocked)
	empty     *time.Timer // pause once nobody is here (armEmptyPauseLocked)
	buffer    *time.Timer // patience with a buffering viewer (checkBufferingLocked)
	wait      *time.Timer // how long the room waits for them (startWaitingLocked)
	countdown *time.Timer // the 3-2-1 before a start (play)
	presence  *time.Timer // a coalesced presence broadcast (presenceChangedLocked)
	// left logs "left" per user once the rejoin grace passes (Leave).
	left map[uuid.UUID]*time.Timer
}

// stopAll stops every timer.
func (t *timers) stopAll() {
	for _, tm := range []*time.Timer{t.advance, t.empty, t.buffer, t.wait, t.countdown, t.presence} {
		if tm != nil {
			tm.Stop()
		}
	}
	for _, tm := range t.left {
		tm.Stop()
	}
}

// stop stops the timer in *slot, if any, and clears the slot.
func stop(slot **time.Timer) {
	if *slot != nil {
		(*slot).Stop()
		*slot = nil
	}
}

// timerTimeout bounds what a timer callback may spend on the database.
const timerTimeout = 5 * time.Second

// afterLocked arms fn to run after d under the room's mutex, unless the
// room has been closed by then; fn gets a context bounded by timerTimeout.
func (r *Room) afterLocked(d time.Duration, fn func(ctx context.Context)) *time.Timer {
	return time.AfterFunc(d, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.closed {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), timerTimeout)
		defer cancel()
		fn(ctx)
	})
}
