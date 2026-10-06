package room

import (
	"context"
	"fmt"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/notify"
	"github.com/anesthetised/couchcast/internal/protocol"
)

// Playback: the commands that move the clock (play, pause, seek, speed,
// next, jump, the 3-2-1 countdown), advancing at the end of an item, the
// empty-room pause and persisting the clock. Commands take the room's
// mutex; the clock is saved through Store.UpdateRoomPlayback and a failed
// save never fails a command (savePlaybackLocked).

// currentDurationLocked is the current media's length (0: unknown).
func (r *Room) currentDurationLocked() int64 {
	if m := r.currentMedia(); m != nil {
		return m.DurationMs
	}
	return 0
}

// positionLocked returns the position at time t.
func (r *Room) positionLocked(t time.Time) int64 {
	return r.clock.position(t, r.currentDurationLocked())
}

// setPlaybackLocked restarts the clock from now and re-arms what depends
// on it: the end-of-item timer and the empty-room pause.
func (r *Room) setPlaybackLocked(playing bool, positionMs int64) {
	r.clock.restart(playing, positionMs, r.now())
	r.scheduleAdvanceLocked()
	r.armEmptyPauseLocked()
}

// armEmptyPauseLocked starts the empty-room countdown when the room is
// playing with nobody in it; it is a no-op otherwise or when armed.
func (r *Room) armEmptyPauseLocked() {
	if !r.info.Settings.PauseWhenEmpty || !r.clock.playing || len(r.viewers) > 0 || r.timers.empty != nil {
		return
	}
	r.timers.empty = r.afterLocked(r.rejoinGrace(), r.pauseIfEmptyLocked)
}

func (r *Room) disarmEmptyPauseLocked() {
	stop(&r.timers.empty)
}

// pauseIfEmptyLocked fires after the grace period: still nobody here,
// still playing — pause where the clock is, once, and say so in the log.
func (r *Room) pauseIfEmptyLocked(ctx context.Context) {
	r.timers.empty = nil
	if !r.info.Settings.PauseWhenEmpty || !r.clock.playing || len(r.viewers) > 0 {
		return
	}
	r.setPlaybackLocked(false, r.positionLocked(r.now()))
	r.savePlaybackLocked(ctx)
	r.logLocked(ctx, "paused: everyone left")
}

// scheduleAdvanceLocked arms the timer that moves to the next item when
// the current one ends.
func (r *Room) scheduleAdvanceLocked() {
	stop(&r.timers.advance)
	m := r.currentMedia()
	if !r.clock.playing || m == nil || m.DurationMs <= 0 || r.clock.current == nil {
		return
	}
	remaining := r.clock.remaining(r.now(), m.DurationMs)
	itemID := *r.clock.current
	seq := r.clock.seq
	r.timers.advance = r.afterLocked(remaining+500*time.Millisecond, func(ctx context.Context) { r.onEndedLocked(ctx, itemID, seq) })
}

// onEndedLocked fires from the advance timer: still the same item on the
// same clock, still playing — go to the next one.
func (r *Room) onEndedLocked(ctx context.Context, itemID uuid.UUID, seq uint64) {
	if !r.clock.isCurrent(itemID) || r.clock.seq != seq || !r.clock.playing {
		return
	}
	if err := r.nextLocked(ctx); err != nil {
		r.deps.Logger.Error("auto advance", "room", r.info.Slug, "error", err)
	}
	r.broadcastLocked()
}

// setCurrentLocked switches to an item (or nothing) at position 0 and
// starts playing when its media is ready.
func (r *Room) setCurrentLocked(item *entity.QueueItem) {
	r.skipVotes = map[uuid.UUID]struct{}{}
	if item == nil {
		r.clock.current = nil
		r.setPlaybackLocked(false, 0)
		return
	}
	r.endWaitLocked(false)
	r.cancelCountdownLocked()
	id := item.ID
	r.clock.current = &id
	r.clock.rate = 1 // a new video always starts at normal speed
	m := r.media[item.MediaID]
	r.setPlaybackLocked(m.IsReady(), 0)

	// Whoever queued it hears about it if they are not watching.
	if r.deps.Notifier != nil && item.AddedBy != nil && !r.connectedLocked(*item.AddedBy) {
		r.deps.Notifier.Notify(*item.AddedBy, notify.Message{
			Title: "Your video is starting",
			Body:  mediaTitle(m) + " — " + r.info.Name,
			URL:   "/r/" + r.info.Slug,
			Tag:   "start-" + item.ID.String(),
		})
	}
}

func mediaTitle(m *entity.Media) string {
	if m == nil {
		return "A video"
	}
	if m.Title != "" {
		return m.Title
	}
	return m.SourceURL
}

// allowedRates are the speeds a moderator may pick.
var allowedRates = []float64{0.5, 0.75, 1, 1.25, 1.5, 2}

// SetRate changes the room's playback speed from now on.
func (r *Room) SetRate(ctx context.Context, actor access.Actor, rate float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ControlPlayback); err != nil {
		return err
	}
	if r.clock.current == nil {
		return errNoCurrent
	}
	ok := false
	for _, a := range allowedRates {
		if a == rate {
			ok = true
			break
		}
	}
	if !ok {
		return invalid("rate must be one of 0.5, 0.75, 1, 1.25, 1.5, 2")
	}
	if rate == r.clock.rate {
		return nil
	}
	// Fold the elapsed time in at the old speed, then switch.
	pos := r.positionLocked(r.now())
	r.clock.rate = rate
	r.setPlaybackLocked(r.clock.playing, pos)
	r.savePlaybackLocked(ctx)
	r.logLocked(ctx, fmt.Sprintf("%s set speed to %g×", actor.User.Username, rate))
	r.broadcastPlaybackLocked()
	return nil
}

// nextLocked drops the current item and advances to the following one.
func (r *Room) nextLocked(ctx context.Context) error {
	if r.clock.current == nil {
		return errNoCurrent
	}
	idx := r.indexOf(*r.clock.current)
	if err := r.markPlayedLocked(ctx, *r.clock.current); err != nil {
		return err
	}

	next := following(r.queue, idx)
	// Items whose ingest failed can never play: skip them into the history
	// instead of stalling the room on them.
	for next != nil {
		m := r.media[next.MediaID]
		if m == nil || m.Status != entity.MediaFailed {
			break
		}
		r.logLocked(ctx, "skipped "+mediaLabel(m)+" (not playable)")
		if err := r.markPlayedLocked(ctx, next.ID); err != nil {
			return err
		}
		next = following(r.queue, idx)
	}
	// Loop: the queue ran out, so the history becomes the queue again in
	// the order it was played.
	if next == nil && r.info.Settings.Loop && len(r.played) > 0 {
		if err := r.deps.Store.RequeuePlayed(ctx, r.info.ID); err != nil {
			return err
		}
		if err := r.reloadQueue(ctx); err != nil {
			return err
		}
		if r.info.Settings.VoteMode {
			if err := r.reorderByVotesLocked(ctx); err != nil {
				return err
			}
		}
		if len(r.queue) > 0 {
			next = r.queue[0]
		}
		// Nobody watching: stop at the top instead of cycling on (and
		// logging every lap) for an empty room; play resumes it.
		if len(r.viewers) == 0 && next != nil {
			r.skipVotes = map[uuid.UUID]struct{}{}
			id := next.ID
			r.clock.current, r.clock.rate = &id, 1
			r.setPlaybackLocked(false, 0)
			r.logLocked(ctx, "queue restarted from the top, paused until someone is back")
			r.savePlaybackLocked(ctx)
			return nil
		}
		r.logLocked(ctx, "queue restarted from the top")
	}
	r.setCurrentLocked(next)
	r.savePlaybackLocked(ctx)
	return nil
}

// savePlaybackLocked persists the room's clock after a command. A failure
// (Postgres briefly unreachable) must not fail the command: the room in
// memory is authoritative and has already told everyone, so it is logged
// and the room is marked unsaved for Tick to retry.
func (r *Room) savePlaybackLocked(ctx context.Context) {
	if err := r.persistLocked(ctx); err != nil {
		if !r.unsaved {
			r.deps.Logger.Warn("persist playback, will retry", "room", r.info.Slug, "error", err)
		}
		r.unsaved = true
		return
	}
	r.unsaved = false
}

func (r *Room) persistLocked(ctx context.Context) error {
	r.lastPersist = r.now()
	// The session has started: the announcement has served its purpose.
	if r.clock.playing && r.info.ScheduledAt != nil {
		r.info.ScheduledAt = nil
		if err := r.deps.Store.SetRoomSchedule(ctx, r.info.ID, nil); err != nil {
			r.deps.Logger.Warn("clear schedule", "room", r.info.Slug, "error", err)
		}
	}
	return r.deps.Store.UpdateRoomPlayback(ctx, r.info.ID, r.clock.state())
}

// Playback returns the live authoritative clock.
func (r *Room) Playback() protocol.Playback {
	r.mu.Lock()
	defer r.mu.Unlock()
	pb := r.clock.playback()
	pb.Type = ""
	return pb
}

// Play resumes playback.
func (r *Room) Play(ctx context.Context, actor access.Actor) error {
	return r.play(ctx, actor, false)
}

// PlayCountdown starts playback after a 3-2-1 every viewer sees.
func (r *Room) PlayCountdown(ctx context.Context, actor access.Actor) error {
	return r.play(ctx, actor, true)
}

// countdownFor is how long the 3-2-1 before a counted start lasts.
const countdownFor = 3 * time.Second

func (r *Room) play(ctx context.Context, actor access.Actor, countdown bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ControlPlayback); err != nil {
		return err
	}
	m := r.currentMedia()
	if m == nil {
		return errNoCurrent
	}
	if !m.IsReady() {
		return invalid("media is not ready yet")
	}
	r.endWaitLocked(false)
	if r.clock.playing {
		return nil
	}
	pos := startFrom(r.clock.positionMs, m.DurationMs)
	// The announced session starts with a countdown, as does any start a
	// moderator asks to count down; a second play during it starts now.
	if (countdown || r.info.ScheduledAt != nil) && r.countdown == nil {
		at := r.now().Add(countdownFor)
		r.countdown = &at
		r.timers.countdown = r.afterLocked(r.patience(countdownFor), r.endCountdownLocked)
		r.broadcastLocked()
		return nil
	}
	r.cancelCountdownLocked()
	r.setPlaybackLocked(true, pos)
	r.savePlaybackLocked(ctx)
	r.broadcastPlaybackLocked()
	return nil
}

// endCountdownLocked starts playback when the countdown runs out.
func (r *Room) endCountdownLocked(ctx context.Context) {
	if r.countdown == nil {
		return
	}
	r.countdown, r.timers.countdown = nil, nil
	m := r.currentMedia()
	if m == nil || !m.IsReady() || r.clock.playing {
		r.broadcastLocked()
		return
	}
	pos := startFrom(r.clock.positionMs, m.DurationMs)
	r.setPlaybackLocked(true, pos)
	r.savePlaybackLocked(ctx)
	r.broadcastLocked()
}

// cancelCountdownLocked drops a pending countdown (pause, seek to another
// item, a direct play).
func (r *Room) cancelCountdownLocked() {
	stop(&r.timers.countdown)
	if r.countdown != nil {
		r.countdown = nil
		r.broadcastLocked()
	}
}

// Pause freezes playback.
func (r *Room) Pause(ctx context.Context, actor access.Actor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ControlPlayback); err != nil {
		return err
	}
	if r.clock.current == nil {
		return errNoCurrent
	}
	r.endWaitLocked(false)
	r.cancelCountdownLocked()
	if !r.clock.playing {
		return nil
	}
	r.setPlaybackLocked(false, r.positionLocked(r.now()))
	r.savePlaybackLocked(ctx)
	r.broadcastPlaybackLocked()
	return nil
}

// Seek jumps to an absolute position, keeping the playing flag.
func (r *Room) Seek(ctx context.Context, actor access.Actor, positionMs int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ControlPlayback); err != nil {
		return err
	}
	m := r.currentMedia()
	if m == nil {
		return errNoCurrent
	}
	r.setPlaybackLocked(r.clock.playing && m.IsReady(), clampSeek(positionMs, m.DurationMs))
	r.savePlaybackLocked(ctx)
	r.broadcastPlaybackLocked()
	return nil
}

// Next skips to the following queue item.
func (r *Room) Next(ctx context.Context, actor access.Actor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ControlPlayback); err != nil {
		return err
	}
	if r.clock.current == nil {
		return errNoCurrent
	}
	// Logged first so a loop restart reads after the skip in the chat.
	r.logLocked(ctx, actor.User.Username+" skipped "+mediaLabel(r.currentMedia()))
	if err := r.nextLocked(ctx); err != nil {
		return err
	}
	r.broadcastLocked()
	return nil
}

// Jump makes the given item current, dropping the one that was playing.
func (r *Room) Jump(ctx context.Context, actor access.Actor, itemID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ControlPlayback); err != nil {
		return err
	}
	target := r.itemByID(itemID)
	if target == nil {
		return notFound("queue item")
	}
	if r.clock.current != nil && *r.clock.current != itemID {
		if err := r.markPlayedLocked(ctx, *r.clock.current); err != nil {
			return err
		}
	}
	r.setCurrentLocked(target)
	r.savePlaybackLocked(ctx)
	r.logLocked(ctx, actor.User.Username+" jumped to "+mediaLabel(r.media[target.MediaID]))
	r.broadcastLocked()
	return nil
}
