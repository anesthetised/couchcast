package room

import (
	"context"
	"fmt"
	"slices"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
)

// fairReorderLocked interleaves the waiting items by who added them
// (Settings.FairQueue, manual mode): each person's videos keep their own
// order and people take turns, starting with whoever comes after the
// current video's adder, so one big batch cannot hold the room. It
// persists the new ranks only when the order changed.
func (r *Room) fairReorderLocked(ctx context.Context) error {
	if !r.info.Settings.FairQueue || r.info.Settings.VoteMode {
		return nil
	}
	var cur *entity.QueueItem
	waiting := make([]*entity.QueueItem, 0, len(r.queue))
	for _, it := range r.queue {
		if r.current != nil && it.ID == *r.current {
			cur = it
		} else {
			waiting = append(waiting, it)
		}
	}
	if len(waiting) < 2 {
		return nil
	}

	adder := func(it *entity.QueueItem) uuid.UUID {
		if it.AddedBy == nil {
			return uuid.Nil()
		}
		return *it.AddedBy
	}
	var order []uuid.UUID
	groups := map[uuid.UUID][]*entity.QueueItem{}
	for _, it := range waiting {
		a := adder(it)
		if _, seen := groups[a]; !seen {
			order = append(order, a)
		}
		groups[a] = append(groups[a], it)
	}
	// Whoever is playing now takes their next turn last.
	if cur != nil {
		if i := slices.Index(order, adder(cur)); i >= 0 {
			order = append(order[i+1:], order[:i+1]...)
		}
	}

	ordered := make([]*entity.QueueItem, 0, len(r.queue))
	if cur != nil {
		ordered = append(ordered, cur)
	}
	for len(ordered) < len(r.queue) {
		for _, a := range order {
			if g := groups[a]; len(g) > 0 {
				ordered = append(ordered, g[0])
				groups[a] = g[1:]
			}
		}
	}
	if slices.Equal(ordered, r.queue) {
		return nil
	}
	ids := make([]uuid.UUID, len(ordered))
	for i, it := range ordered {
		ids[i] = it.ID
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

// orderedByRule reports whether something other than a moderator's hand
// orders the queue (votes or turns), and why, for refusals.
func (r *Room) orderedByRule() string {
	switch {
	case r.info.Settings.VoteMode:
		return "the queue is ordered by votes while vote mode is on"
	case r.info.Settings.FairQueue:
		return "the queue takes turns while fair queue is on"
	}
	return ""
}
