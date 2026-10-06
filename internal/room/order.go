package room

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
)

// The queue's order rules are pure functions of the queue and the current
// item: each returns a new slice (never reordering its input in place),
// with the current item first wherever it was, and applyOrderLocked
// persists the result.

// splitCurrent separates the current item from the waiting ones.
func splitCurrent(queue []*entity.QueueItem, current *uuid.UUID) (cur *entity.QueueItem, waiting []*entity.QueueItem) {
	waiting = make([]*entity.QueueItem, 0, len(queue))
	for _, it := range queue {
		if current != nil && it.ID == *current {
			cur = it
		} else {
			waiting = append(waiting, it)
		}
	}
	return cur, waiting
}

// withCurrent puts cur (if any) in front of the waiting items.
func withCurrent(cur *entity.QueueItem, waiting []*entity.QueueItem) []*entity.QueueItem {
	if cur == nil {
		return waiting
	}
	return append([]*entity.QueueItem{cur}, waiting...)
}

// voteOrder sorts the waiting items by votes (most first), then by age.
func voteOrder(queue []*entity.QueueItem, current *uuid.UUID) []*entity.QueueItem {
	cur, waiting := splitCurrent(queue, current)
	sort.SliceStable(waiting, func(i, j int) bool {
		if waiting[i].Votes != waiting[j].Votes {
			return waiting[i].Votes > waiting[j].Votes
		}
		return waiting[i].CreatedAt.Before(waiting[j].CreatedAt)
	})
	return withCurrent(cur, waiting)
}

// fairOrder interleaves the waiting items by who added them: each
// person's videos keep their own order and people take turns, starting
// with whoever comes after the current video's adder, so one big batch
// cannot hold the room.
func fairOrder(queue []*entity.QueueItem, current *uuid.UUID) []*entity.QueueItem {
	cur, waiting := splitCurrent(queue, current)
	if len(waiting) < 2 {
		return slices.Clone(queue) // nobody to take turns with
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
	turns := make([]*entity.QueueItem, 0, len(waiting))
	for len(turns) < len(waiting) {
		for _, a := range order {
			if g := groups[a]; len(g) > 0 {
				turns = append(turns, g[0])
				groups[a] = g[1:]
			}
		}
	}
	return withCurrent(cur, turns)
}

// placeAfter moves the item at idx right after afterID (nil = the head),
// never ahead of the current item; an unknown anchor is an error.
func placeAfter(queue []*entity.QueueItem, idx int, afterID, current *uuid.UUID) ([]*entity.QueueItem, error) {
	item := queue[idx]
	rest := append(append([]*entity.QueueItem{}, queue[:idx]...), queue[idx+1:]...)

	insertAt := 0
	if afterID != nil {
		i := slices.IndexFunc(rest, func(it *entity.QueueItem) bool { return it.ID == *afterID })
		if i < 0 {
			return nil, notFound("anchor item")
		}
		insertAt = i + 1
	}
	if current != nil && insertAt == 0 && len(rest) > 0 && rest[0].ID == *current {
		insertAt = 1
	}
	return slices.Concat(rest[:insertAt], []*entity.QueueItem{item}, rest[insertAt:]), nil
}

// batchAfterCurrent orders the current item, then the batch, then the
// rest of the queue.
func batchAfterCurrent(queue, batch []*entity.QueueItem, current *uuid.UUID) []*entity.QueueItem {
	cur, waiting := splitCurrent(queue, current)
	return withCurrent(cur, slices.Concat(batch, waiting))
}

// shuffled reorders the items after a leading current item with shuffle
// (rand.Shuffle in the room, a fixed permutation in tests).
func shuffled(queue []*entity.QueueItem, current *uuid.UUID, shuffle func(n int, swap func(i, j int))) []*entity.QueueItem {
	out := slices.Clone(queue)
	rest := out
	if current != nil && len(out) > 0 && out[0].ID == *current {
		rest = out[1:]
	}
	shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	return out
}

// following is the item that takes over from the one that was at idx
// before it left the queue: the one now at idx, else the head.
func following(queue []*entity.QueueItem, idx int) *entity.QueueItem {
	if idx >= 0 && idx < len(queue) {
		return queue[idx]
	}
	if len(queue) > 0 {
		return queue[0]
	}
	return nil
}

// applyOrderLocked makes ordered the queue: it persists the ranks and
// renumbers the items in memory, or does nothing when the order did not
// change.
func (r *Room) applyOrderLocked(ctx context.Context, ordered []*entity.QueueItem) error {
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
