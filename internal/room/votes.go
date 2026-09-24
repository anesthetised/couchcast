package room

import (
	"context"
	"fmt"
	"math"
	"sort"

	"uuid"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/protocol"
)

// onlineUsersLocked counts distinct signed-in viewers.
func (r *Room) onlineUsersLocked() int {
	seen := map[string]struct{}{}
	for _, v := range r.viewers {
		if v.user != nil {
			seen[v.user.Username] = struct{}{}
		}
	}
	return len(seen)
}

// skipNeededLocked is how many skip votes end the current item in vote
// mode: ceil(online users × threshold), at least one.
func (r *Room) skipNeededLocked() int {
	if !r.info.Settings.VoteMode {
		return 0
	}
	n := int(math.Ceil(float64(r.onlineUsersLocked()) * r.info.Settings.SkipThreshold))
	if n < 1 {
		n = 1
	}
	return n
}

// SettingsSet updates room options; nil fields are untouched.
func (r *Room) SettingsSet(ctx context.Context, actor access.Actor, in protocol.SettingsSet) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ManageSettings); err != nil {
		return err
	}

	s := r.info.Settings
	if in.VoteMode != nil {
		s.VoteMode = *in.VoteMode
	}
	if in.SkipThreshold != nil {
		if *in.SkipThreshold <= 0 || *in.SkipThreshold > 1 {
			return invalid("skipThreshold must be within (0, 1]")
		}
		s.SkipThreshold = *in.SkipThreshold
	}
	if in.ViewersCanAdd != nil {
		s.ViewersCanAdd = *in.ViewersCanAdd
	}
	if in.Loop != nil {
		s.Loop = *in.Loop
	}
	if in.PauseWhenEmpty != nil {
		s.PauseWhenEmpty = *in.PauseWhenEmpty
		if !s.PauseWhenEmpty {
			r.disarmEmptyPauseLocked()
		}
	}
	if in.WaitForBuffering != nil {
		s.WaitForBuffering = *in.WaitForBuffering
	}
	if in.FairQueue != nil {
		s.FairQueue = *in.FairQueue
	}
	if in.SlowModeSec != nil {
		switch *in.SlowModeSec {
		case 0, 5, 15, 30, 60:
			s.SlowModeSec = *in.SlowModeSec
		default:
			return invalid("slowModeSec must be 0, 5, 15, 30 or 60")
		}
	}

	if err := r.deps.Store.UpdateRoomSettings(ctx, r.info.ID, s); err != nil {
		return err
	}
	r.info.Settings = s

	if s.VoteMode {
		if err := r.reorderByVotesLocked(ctx); err != nil {
			return err
		}
	} else {
		r.skipVotes = map[uuid.UUID]struct{}{}
		if err := r.fairReorderLocked(ctx); err != nil {
			return err
		}
	}
	r.broadcastLocked()
	return nil
}

// QueueVote toggles the actor's upvote on an item and re-sorts the queue.
func (r *Room) QueueVote(ctx context.Context, actor access.Actor, itemID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.Vote); err != nil {
		return err
	}
	if !r.info.Settings.VoteMode {
		return invalid("vote mode is off")
	}
	item := r.itemByID(itemID)
	if item == nil {
		return notFound("queue item")
	}
	if r.current != nil && *r.current == itemID {
		return invalid("the current item cannot be voted on")
	}

	on, err := r.deps.Store.ToggleQueueVote(ctx, itemID, actor.User.ID)
	if err != nil {
		return err
	}
	if on {
		item.Votes++
	} else if item.Votes > 0 {
		item.Votes--
	}
	if err := r.reorderByVotesLocked(ctx); err != nil {
		return err
	}
	r.broadcastLocked()
	return nil
}

// SkipVote toggles the actor's vote to skip the current item; reaching
// the threshold advances immediately.
func (r *Room) SkipVote(ctx context.Context, actor access.Actor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.Vote); err != nil {
		return err
	}
	if !r.info.Settings.VoteMode {
		return invalid("vote mode is off")
	}
	if r.current == nil {
		return errNoCurrent
	}

	id := actor.User.ID
	if _, voted := r.skipVotes[id]; voted {
		delete(r.skipVotes, id)
	} else {
		r.skipVotes[id] = struct{}{}
	}

	if len(r.skipVotes) >= r.skipNeededLocked() {
		r.logLocked(ctx, "skipped "+mediaLabel(r.currentMedia())+" by vote")
		if err := r.nextLocked(ctx); err != nil {
			return err
		}
	}
	r.broadcastLocked()
	return nil
}

// reorderByVotesLocked sorts non-current items by votes (desc) then age
// and persists the new ranks when anything moved.
func (r *Room) reorderByVotesLocked(ctx context.Context) error {
	if len(r.queue) < 2 {
		return nil
	}
	ordered := make([]*entity.QueueItem, 0, len(r.queue))
	var cur *entity.QueueItem
	for _, it := range r.queue {
		if r.current != nil && it.ID == *r.current {
			cur = it
			continue
		}
		ordered = append(ordered, it)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Votes != ordered[j].Votes {
			return ordered[i].Votes > ordered[j].Votes
		}
		return ordered[i].CreatedAt.Before(ordered[j].CreatedAt)
	})
	if cur != nil {
		ordered = append([]*entity.QueueItem{cur}, ordered...)
	}

	changed := false
	ids := make([]uuid.UUID, len(ordered))
	for i, it := range ordered {
		ids[i] = it.ID
		if r.queue[i].ID != it.ID {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := r.deps.Store.SetQueueRanks(ctx, r.info.ID, ids); err != nil {
		return err
	}
	for i, it := range ordered {
		it.Rank = fmt.Sprintf("%08d", i+1)
	}
	r.queue = ordered
	return nil
}
