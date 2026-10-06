package room

import "context"

// fairReorderLocked applies fairOrder when the room takes turns
// (Settings.FairQueue, manual mode).
func (r *Room) fairReorderLocked(ctx context.Context) error {
	if !r.info.Settings.FairQueue || r.info.Settings.VoteMode {
		return nil
	}
	return r.applyOrderLocked(ctx, fairOrder(r.queue, r.clock.current))
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
