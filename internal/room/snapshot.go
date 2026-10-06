package room

import (
	"sort"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/protocol"
)

// Broadcasts are prepared, then delivered. snapshotLocked builds the
// viewer-independent state, personalize adds what differs per viewer and
// snapshotsLocked pairs each connection with its message; send hands them
// to the connections. Preparation reads the room, so it runs under the
// room's lock; so does delivery, which is cheap because Conn.Send never
// blocks (the hub buffers and drops slow clients).

// envelope is one prepared message for one connection.
type envelope struct {
	conn Conn
	msg  any
}

// send delivers prepared messages.
func send(out []envelope) {
	for _, e := range out {
		e.conn.Send(e.msg)
	}
}

func (r *Room) mediaInfoLocked(m *entity.Media) protocol.MediaInfo {
	if m == nil {
		return protocol.MediaInfo{}
	}
	info := protocol.MediaInfo{
		ID: m.ID, Title: m.Title, DurationMs: m.DurationMs, ThumbnailURL: m.ThumbnailURL,
		Status: m.Status, Progress: m.Progress, SpeedBps: m.SpeedBps, EtaMs: m.EtaMs, Error: m.Error, Renditions: m.Renditions, Subtitles: m.Subtitles, Chapters: m.Chapters, Storyboard: m.Storyboard, SourceURL: m.SourceURL,
	}
	if info.Renditions == nil {
		info.Renditions = []entity.Rendition{}
	}
	if m.IsReady() && r.deps.Signer != nil {
		info.Manifest = "/media/" + m.ID.String() + "/manifest.mpd"
		info.Token = r.deps.Signer.Sign(m.ID, r.now())
	}
	return info
}

// snapshotLocked builds the viewer-independent snapshot.
func (r *Room) snapshotLocked() protocol.Snapshot {
	snap := protocol.Snapshot{
		Type: protocol.TypeRoomState,
		Room: protocol.RoomInfo{
			ID: r.info.ID, Slug: r.info.Slug, Name: r.info.Name, Visibility: r.info.Visibility,
			Settings: r.info.Settings, Owner: r.owner, Description: r.info.Description, Pinned: r.pinned,
			ScheduledMs: unixMs(r.info.ScheduledAt),
		},
		Playback: r.clock.playback(),
		Waiting:  r.waiting,
		CountdownMs: func() int64 {
			if r.countdown == nil {
				return 0
			}
			return r.countdown.UnixMilli()
		}(),
		Queue: make([]protocol.QueueEntry, 0, len(r.queue)),
	}
	snap.Playback.Type = ""

	for _, it := range r.queue {
		snap.Queue = append(snap.Queue, protocol.QueueEntry{
			ID: it.ID, Media: r.mediaInfoLocked(r.media[it.MediaID]), AddedBy: it.AddedByName, Votes: it.Votes,
			Current: r.clock.isCurrent(it.ID),
		})
	}
	snap.Played = make([]protocol.QueueEntry, 0, len(r.played))
	for _, it := range r.played {
		var playedMs int64
		if it.PlayedAt != nil {
			playedMs = it.PlayedAt.UnixMilli()
		}
		snap.Played = append(snap.Played, protocol.QueueEntry{
			ID: it.ID, Media: r.mediaInfoLocked(r.media[it.MediaID]), AddedBy: it.AddedByName, PlayedMs: playedMs,
		})
	}

	snap.Members, snap.Guests = presenceOf(r.viewers)

	snap.SkipVotes = len(r.skipVotes)
	snap.SkipNeeded = r.skipNeededLocked()

	return snap
}

// presenceOf lists the signed-in viewers once per user (a user with
// several tabs is buffering if any tab is, and as far behind as the worst
// one), sorted by name, and counts the anonymous ones.
func presenceOf(viewers map[Conn]*viewer) (members []protocol.Presence, guests int) {
	members = []protocol.Presence{}
	seen := map[string]int{}
	for _, v := range viewers {
		if v.user == nil {
			guests++
			continue
		}
		if i, ok := seen[v.user.Username]; ok {
			members[i].Buffering = members[i].Buffering || v.buffering
			if abs(v.lagMs) > abs(members[i].LagMs) {
				members[i].LagMs = v.lagMs // the worst of their tabs
			}
			continue
		}
		seen[v.user.Username] = len(members)
		members = append(members, protocol.Presence{Username: v.user.Username, Color: v.user.AvatarColor, Role: v.role, Buffering: v.buffering, LagMs: v.lagMs})
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Username < members[j].Username })
	return members, guests
}

// personalize adds the viewer-specific flags (their skip vote, their
// queue votes) to a copy of the snapshot; base is never modified.
func personalize(base protocol.Snapshot, v *viewer, skipVotes map[uuid.UUID]struct{}, voted map[uuid.UUID]bool) protocol.Snapshot {
	snap := base
	if v.user == nil {
		return snap
	}
	_, snap.SkipVoted = skipVotes[v.user.ID]
	if len(voted) > 0 {
		snap.Queue = make([]protocol.QueueEntry, len(base.Queue))
		copy(snap.Queue, base.Queue)
		for i := range snap.Queue {
			snap.Queue[i].Voted = voted[snap.Queue[i].ID]
		}
	}
	return snap
}

func (r *Room) personalizedLocked(base protocol.Snapshot, v *viewer) protocol.Snapshot {
	return personalize(base, v, r.skipVotes, r.votesFor(v))
}

// snapshotsLocked prepares every viewer's copy of base, except for one
// connection (nil: everyone).
func (r *Room) snapshotsLocked(base protocol.Snapshot, except Conn) []envelope {
	out := make([]envelope, 0, len(r.viewers))
	for c, v := range r.viewers {
		if c != except {
			out = append(out, envelope{c, r.personalizedLocked(base, v)})
		}
	}
	return out
}

// broadcastLocked sends the current snapshot to every viewer.
func (r *Room) broadcastLocked() {
	// A full snapshot carries the latest presence too.
	if r.presenceTimer != nil {
		r.presenceTimer.Stop()
		r.presenceTimer = nil
	}
	send(r.snapshotsLocked(r.snapshotLocked(), nil))
}

// presenceChangedLocked broadcasts a presence change, coalesced over
// Deps.PresenceEvery: in a big room N viewers changing at once would
// otherwise send N full snapshots to N viewers and overflow them all.
func (r *Room) presenceChangedLocked() {
	if r.deps.PresenceEvery <= 0 {
		r.broadcastLocked()
		return
	}
	if r.presenceTimer == nil {
		r.presenceTimer = time.AfterFunc(r.deps.PresenceEvery, r.flushPresence)
	}
}

func (r *Room) flushPresence() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.presenceTimer == nil {
		return
	}
	r.broadcastLocked()
}

// broadcastPlaybackLocked sends only the clock: cheaper and jitter-free.
func (r *Room) broadcastPlaybackLocked() {
	pb := r.clock.playback()
	out := make([]envelope, 0, len(r.viewers))
	for c := range r.viewers {
		out = append(out, envelope{c, pb})
	}
	send(out)
}

// votesFor returns the viewer's queue votes, from memory: a broadcast to
// every viewer must not cost a query each (#121).
func (r *Room) votesFor(v *viewer) map[uuid.UUID]bool {
	if v.user == nil || !r.info.Settings.VoteMode {
		return nil
	}
	return r.votes[v.user.ID]
}
