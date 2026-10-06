package room

import (
	"context"
	"slices"
	"strings"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/protocol"
)

// Presence: viewers joining and leaving (with the rejoin grace), kicks,
// client reports (buffering, lag) and waiting for buffering viewers.
// Everything here runs under the room's mutex; the only database writes
// are chat log lines and the clock when the room pauses or resumes.

// connectedLocked reports whether the user has a live connection here.
func (r *Room) connectedLocked(user uuid.UUID) bool {
	for _, v := range r.viewers {
		if v.user != nil && v.user.ID == user {
			return true
		}
	}
	return false
}

// Join registers a connection and sends it the welcome message with the
// chat backlog.
func (r *Room) Join(ctx context.Context, conn Conn, actor access.Actor) {
	messages := r.recentMessages(ctx)

	r.mu.Lock()
	defer r.mu.Unlock()

	r.disarmEmptyPauseLocked()
	first := actor.User != nil && !r.userOnlineLocked(actor.User.ID) &&
		r.now().Sub(r.leftAt[actor.User.ID]) > r.rejoinGrace()
	if actor.User != nil {
		if t, ok := r.timers.left[actor.User.ID]; ok {
			t.Stop()
			delete(r.timers.left, actor.User.ID)
		}
	}
	v := &viewer{conn: conn, user: actor.User, role: actor.Role()}
	r.viewers[conn] = v
	r.lastActive = r.now()

	base := r.snapshotLocked()
	welcome := protocol.Welcome{
		Type:     protocol.TypeWelcome,
		Role:     v.role,
		Snapshot: r.personalizedLocked(base, v),
		Messages: messages,
	}
	if v.user != nil {
		name := v.user.Username
		welcome.Me = &name
	}
	if welcome.Messages == nil {
		welcome.Messages = []protocol.ChatMessage{}
	}
	conn.Send(welcome)

	// Others learn about the new presence (coalesced); everyone, including
	// the newcomer, gets the log line after the welcome.
	if r.deps.PresenceEvery > 0 {
		r.presenceChangedLocked()
	} else {
		send(r.snapshotsLocked(base, conn))
	}
	if first {
		r.logLocked(ctx, actor.User.Username+" joined")
	}
}

// defaultRejoinGrace is how long after leaving a return is still "the
// same visit".
const defaultRejoinGrace = 2 * time.Minute

func (r *Room) rejoinGrace() time.Duration {
	if r.deps.RejoinGrace > 0 {
		return r.deps.RejoinGrace
	}
	return defaultRejoinGrace
}

// onLeftLocked fires after the grace period: if the user is still gone,
// log it.
func (r *Room) onLeftLocked(ctx context.Context, userID uuid.UUID, username string) {
	delete(r.timers.left, userID)
	if r.userOnlineLocked(userID) {
		return
	}
	r.logLocked(ctx, username+" left")
}

// userOnlineLocked reports whether the user has another connection.
func (r *Room) userOnlineLocked(userID uuid.UUID) bool {
	for _, v := range r.viewers {
		if v.user != nil && v.user.ID == userID {
			return true
		}
	}
	return false
}

// Leave unregisters a connection.
func (r *Room) Leave(conn Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.viewers[conn]
	if !ok {
		return
	}
	delete(r.viewers, conn)
	r.lastActive = r.now()
	r.armEmptyPauseLocked()
	r.checkBufferingLocked()
	if v.user != nil && !r.userOnlineLocked(v.user.ID) {
		r.leftAt[v.user.ID] = r.now()
		if t, ok := r.timers.left[v.user.ID]; ok {
			t.Stop()
		}
		id, name := v.user.ID, v.user.Username
		r.timers.left[id] = r.afterLocked(r.rejoinGrace(), func(ctx context.Context) { r.onLeftLocked(ctx, id, name) })
	}
	r.presenceChangedLocked()
}

// UpdateRole refreshes the role shown for a connection.
func (r *Room) UpdateRole(conn Conn, role entity.RoomRole) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.viewers[conn]; ok && v.role != role {
		v.role = role
		r.broadcastLocked()
	}
}

// Kick closes every connection of the user.
func (r *Room) Kick(userID uuid.UUID, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for c, v := range r.viewers {
		if v.user != nil && v.user.ID == userID {
			delete(r.viewers, c)
			c.Send(protocol.Kicked{Type: protocol.TypeKicked, Reason: reason})
			c.Close(reason)
		}
	}
	r.armEmptyPauseLocked()
	r.broadcastLocked()
}

// Viewers returns the number of connections.
func (r *Room) Viewers() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.viewers)
}

// Report records client telemetry.
func (r *Room) Report(conn Conn, state string, positionMs int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.viewers[conn]
	if !ok {
		return
	}
	// How far the viewer's video is from the clock, coarsely: presence
	// only changes (and is broadcast) when that moves by half a second
	// or crosses the one-second "in sync" band.
	changed := false
	if r.clock.current != nil && state != "ended" {
		if lag := lagOf(r.positionLocked(r.now()) - positionMs); lag != v.lagMs {
			v.lagMs = lag
			changed = true
		}
	}
	buffering := state == "buffering"
	if changed && v.buffering == buffering {
		r.presenceChangedLocked()
	}
	if v.buffering != buffering {
		v.buffering = buffering
		if !buffering {
			v.ignored = false
		}
		r.checkBufferingLocked()
		r.presenceChangedLocked()
	}
}

// lagOf rounds a distance from the clock (positive: behind) to half
// seconds, and to 0 inside the band where the synchroniser keeps up.
func lagOf(ms int64) int64 {
	if ms > -1000 && ms < 1000 {
		return 0
	}
	return (ms + sign(ms)*250) / 500 * 500
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func sign(n int64) int64 {
	if n < 0 {
		return -1
	}
	return 1
}

const (
	// bufferPatience is how long a viewer may buffer before the room waits.
	bufferPatience = 4 * time.Second
	// maxWait is how long the room waits before going on without them.
	maxWait = 30 * time.Second
)

// stuckLocked lists the viewers the room would wait for.
func (r *Room) stuckLocked() []string {
	var out []string
	for _, v := range r.viewers {
		if v.buffering && !v.ignored && v.user != nil && !slices.Contains(out, v.user.Username) {
			out = append(out, v.user.Username)
		}
	}
	slices.Sort(out)
	return out
}

// checkBufferingLocked reacts to a change in who is buffering: resume
// when the room waited and everyone is ready, start the patience timer
// when someone starts buffering while the room plays.
func (r *Room) checkBufferingLocked() {
	stuck := r.stuckLocked()
	if r.waiting != nil {
		if len(stuck) == 0 {
			r.endWaitLocked(true)
		} else {
			r.waiting = stuck
		}
		return
	}
	if !r.info.Settings.WaitForBuffering || !r.clock.playing || len(stuck) == 0 {
		stop(&r.timers.buffer)
		return
	}
	if r.timers.buffer == nil {
		r.timers.buffer = r.afterLocked(r.patience(bufferPatience), r.startWaitingLocked)
	}
}

// patience lets tests shorten the timers through Deps.RejoinGrace's
// sibling; production uses the constants.
func (r *Room) patience(d time.Duration) time.Duration {
	if r.deps.WaitScale > 0 {
		return time.Duration(float64(d) * r.deps.WaitScale)
	}
	return d
}

// startWaitingLocked pauses the room for whoever is still buffering.
func (r *Room) startWaitingLocked(ctx context.Context) {
	r.timers.buffer = nil
	stuck := r.stuckLocked()
	if !r.info.Settings.WaitForBuffering || !r.clock.playing || len(stuck) == 0 || r.waiting != nil {
		return
	}
	r.waiting = stuck
	r.setPlaybackLocked(false, r.positionLocked(r.now()))
	r.timers.wait = r.afterLocked(r.patience(maxWait), r.stopWaitingLocked)
	r.savePlaybackLocked(ctx)
	r.logLocked(ctx, "waiting for "+strings.Join(stuck, ", "))
	r.broadcastLocked()
}

// stopWaitingLocked goes on without the viewers still buffering after
// maxWait.
func (r *Room) stopWaitingLocked(ctx context.Context) {
	r.timers.wait = nil
	if r.waiting == nil {
		return
	}
	for _, v := range r.viewers {
		if v.buffering {
			v.ignored = true
		}
	}
	r.logLocked(ctx, "continuing without "+strings.Join(r.waiting, ", "))
	r.endWaitLocked(true)
}

// endWaitLocked leaves the waiting state, resuming playback when asked
// (a moderator's own play/pause leaves it without touching the clock).
func (r *Room) endWaitLocked(resume bool) {
	stop(&r.timers.wait)
	if r.waiting == nil {
		return
	}
	r.waiting = nil
	if resume && !r.clock.playing && r.clock.current != nil {
		r.setPlaybackLocked(true, r.clock.positionMs)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.savePlaybackLocked(ctx)
	}
	r.broadcastLocked()
}
