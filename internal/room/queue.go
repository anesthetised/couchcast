package room

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/ingest"
	"github.com/anesthetised/couchcast/internal/protocol"
	"github.com/anesthetised/couchcast/internal/repository"
)

// Queue: loading the queue and history, the queue commands (add, bulk
// add, remove, move, clear, shuffle, replay, retry) and media updates from
// the ingest. Commands take the room's mutex and write through Store; the
// order rules themselves are pure (order.go). Admission, which may resolve
// a host, runs before the mutex is taken.

// playedKept is how much history a room keeps in memory and sends out.
const playedKept = 20

func (r *Room) reloadQueue(ctx context.Context) error {
	items, err := r.deps.Store.ListQueue(ctx, r.info.ID)
	if err != nil {
		return err
	}
	played, err := r.deps.Store.ListPlayed(ctx, r.info.ID, playedKept)
	if err != nil {
		return err
	}
	ids := make([]uuid.UUID, 0, len(items)+len(played))
	for i := range items {
		ids = append(ids, items[i].MediaID)
	}
	for i := range played {
		ids = append(ids, played[i].MediaID)
	}
	media, err := r.deps.Store.GetMediaBatch(ctx, ids)
	if err != nil {
		return err
	}
	votes, err := r.deps.Store.ListQueueVotes(ctx, r.info.ID)
	if err != nil {
		return err
	}

	r.votes = make(map[uuid.UUID]map[uuid.UUID]bool, len(votes))
	for user, items := range votes {
		set := make(map[uuid.UUID]bool, len(items))
		for _, id := range items {
			set[id] = true
		}
		r.votes[user] = set
	}
	r.queue = r.queue[:0]
	for i := range items {
		r.queue = append(r.queue, &items[i])
	}
	r.played = r.played[:0]
	for i := range played {
		r.played = append(r.played, &played[i])
	}
	r.media = media
	return nil
}

// markPlayedLocked moves a queue item into the history.
func (r *Room) markPlayedLocked(ctx context.Context, itemID uuid.UUID) error {
	now := r.now()
	if err := r.deps.Store.MarkQueueItemPlayed(ctx, r.info.ID, itemID, now); err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	idx := r.indexOf(itemID)
	if idx < 0 {
		return nil
	}
	item := r.queue[idx]
	r.queue = append(r.queue[:idx], r.queue[idx+1:]...)
	item.PlayedAt = &now
	r.played = append([]*entity.QueueItem{item}, r.played...)
	if len(r.played) > playedKept {
		r.played = r.played[:playedKept]
	}
	return nil
}

func (r *Room) itemByID(id uuid.UUID) *entity.QueueItem {
	for _, it := range r.queue {
		if it.ID == id {
			return it
		}
	}
	return nil
}

func (r *Room) indexOf(id uuid.UUID) int {
	for i, it := range r.queue {
		if it.ID == id {
			return i
		}
	}
	return -1
}

func (r *Room) currentMedia() *entity.Media {
	if r.clock.current == nil {
		return nil
	}
	it := r.itemByID(*r.clock.current)
	if it == nil {
		return nil
	}
	return r.media[it.MediaID]
}

// QueueAdd admits a URL and appends it to the queue, or places it right
// after the current item when next is set (manual mode only).
func (r *Room) QueueAdd(ctx context.Context, actor access.Actor, rawURL string, next, force bool) error {
	r.mu.Lock()
	err := r.canAddLocked(actor, 1)
	if err == nil && r.deps.QueueAddLimiter != nil && actor.User != nil && !r.deps.QueueAddLimiter.Allow(actor.User.ID.String()) {
		err = &Error{Code: protocol.CodeRateLimit, Message: "you are adding videos too quickly"}
	}
	r.mu.Unlock()
	if err != nil {
		return err
	}

	// Admission may resolve the link's host: the room lock is not held,
	// so playback and chat never wait for DNS.
	adm, err := r.deps.Admit.Admit(ctx, rawURL)
	if err != nil {
		if msg := admitMessage(err); msg != "" {
			return &Error{Code: protocol.CodeInvalid, Message: msg}
		}
		return err // internal: logged by the hub, "internal error" to the client
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// The room may have changed meanwhile: check again.
	if err := r.canAddLocked(actor, 1); err != nil {
		return err
	}

	var addedBy *uuid.UUID
	if actor.User != nil {
		addedBy = &actor.User.ID
	}
	var (
		media *entity.Media
		item  *entity.QueueItem
	)
	err = r.deps.Store.InTx(ctx, func(q repository.Querier) error {
		var err error
		if media, err = r.deps.Admit.Create(ctx, q, adm); err != nil {
			return err
		}
		if !force {
			if err := r.duplicateLocked(media.ID); err != nil {
				return err
			}
		}
		item, err = r.deps.Store.AddQueueItem(ctx, q, r.info.ID, media.ID, addedBy)
		return err
	})
	if err != nil {
		return err
	}

	if actor.User != nil {
		item.AddedByName = actor.User.Username
	}
	r.queue = append(r.queue, item)
	r.media[media.ID] = media
	if next && r.orderedByRule() == "" && r.clock.current != nil && len(r.queue) > 1 {
		ordered, err := placeAfter(r.queue, len(r.queue)-1, r.clock.current, r.clock.current)
		if err != nil {
			return err
		}
		if err := r.applyOrderLocked(ctx, ordered); err != nil {
			return err
		}
	}
	if err := r.fairReorderLocked(ctx); err != nil {
		return err
	}
	if actor.User != nil {
		r.logLocked(ctx, actor.User.Username+" added "+mediaLabel(media))
	}

	if r.clock.current == nil {
		r.setCurrentLocked(item)
		r.savePlaybackLocked(ctx)
	}
	r.broadcastLocked()
	return nil
}

// canAddLocked checks that the actor may add n more items to the room now.
func (r *Room) canAddLocked(actor access.Actor, n int) error {
	if r.closed {
		return notFound("room")
	}
	if err := r.requireLocked(actor, access.AddToQueue); err != nil {
		return err
	}
	if len(r.queue)+n > 200 {
		return invalid("queue is full")
	}
	return nil
}

// maxAddMany bounds one bulk add (ingest.PlaylistLimit on the import side).
const maxAddMany = 50

// QueueAddMany queues several links at once, in order — a playlist
// import. Links the room already has (queued or played), unsupported or
// blocked links are skipped, and so is anything past the queue limit. The
// add budget is charged once. With next (manual mode, something playing)
// the batch lands right after the current item.
func (r *Room) QueueAddMany(ctx context.Context, actor access.Actor, urls []string, next bool) error {
	if len(urls) == 0 || len(urls) > maxAddMany {
		return invalid(fmt.Sprintf("send between 1 and %d links", maxAddMany))
	}
	r.mu.Lock()
	err := r.canAddLocked(actor, 0)
	if err == nil && r.deps.QueueAddLimiter != nil && actor.User != nil && !r.deps.QueueAddLimiter.Allow(actor.User.ID.String()) {
		err = &Error{Code: protocol.CodeRateLimit, Message: "you are adding videos too quickly"}
	}
	r.mu.Unlock()
	if err != nil {
		return err
	}

	// Admission first, without the room lock (it may resolve every host);
	// links that fail it are skipped like duplicates.
	admitted := make([]ingest.Admission, 0, len(urls))
	skipped := 0
	for _, raw := range urls {
		a, err := r.deps.Admit.Admit(ctx, raw)
		if errors.Is(err, errUnsupported) || errors.Is(err, errBlocked) || errors.Is(err, errPrivate) || errors.Is(err, errUnknownHost) {
			skipped++
			continue
		}
		if err != nil {
			return err
		}
		admitted = append(admitted, a)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.canAddLocked(actor, 0); err != nil {
		return err
	}

	seen := map[uuid.UUID]bool{}
	for _, it := range r.queue {
		seen[it.MediaID] = true
	}
	for _, it := range r.played {
		seen[it.MediaID] = true
	}

	var addedBy *uuid.UUID
	if actor.User != nil {
		addedBy = &actor.User.ID
	}
	var (
		added  []*entity.QueueItem
		medias []*entity.Media
	)
	err = r.deps.Store.InTx(ctx, func(q repository.Querier) error {
		for _, a := range admitted {
			if len(r.queue)+len(added) >= 200 {
				skipped++
				continue
			}
			media, err := r.deps.Admit.Create(ctx, q, a)
			if err != nil {
				return err
			}
			if seen[media.ID] {
				skipped++
				continue
			}
			seen[media.ID] = true
			item, err := r.deps.Store.AddQueueItem(ctx, q, r.info.ID, media.ID, addedBy)
			if err != nil {
				return err
			}
			if actor.User != nil {
				item.AddedByName = actor.User.Username
			}
			added = append(added, item)
			medias = append(medias, media)
		}
		if len(added) == 0 {
			return &Error{Code: protocol.CodeDuplicate, Message: "nothing to add: these videos are already here or cannot be played"}
		}
		return nil
	})
	if err != nil {
		return err
	}

	for i, item := range added {
		r.media[item.MediaID] = medias[i]
	}
	if next && r.orderedByRule() == "" && r.clock.current != nil {
		// One re-rank for the whole batch: current, the batch, the rest.
		if err := r.applyOrderLocked(ctx, batchAfterCurrent(r.queue, added, r.clock.current)); err != nil {
			return err
		}
	} else {
		r.queue = append(r.queue, added...)
	}
	// In vote mode the batch has no votes yet and is the newest: the end
	// of the queue is already where the vote order puts it.
	if err := r.fairReorderLocked(ctx); err != nil {
		return err
	}

	if actor.User != nil {
		line := fmt.Sprintf("%s added %d %s from a playlist", actor.User.Username, len(added), plural(len(added), "video", "videos"))
		if skipped > 0 {
			line += fmt.Sprintf(" (%d skipped)", skipped)
		}
		r.logLocked(ctx, line)
	}
	if r.clock.current == nil {
		r.setCurrentLocked(added[0])
		r.savePlaybackLocked(ctx)
	}
	r.broadcastLocked()
	return nil
}

// duplicateLocked answers CodeDuplicate when the media is already queued
// or was played in this session, so the client can ask before doubling.
func (r *Room) duplicateLocked(mediaID uuid.UUID) error {
	for _, it := range r.queue {
		if it.MediaID == mediaID {
			return &Error{Code: protocol.CodeDuplicate, Message: "this video is already in the queue"}
		}
	}
	for _, it := range r.played {
		if it.MediaID == mediaID {
			return &Error{Code: protocol.CodeDuplicate, Message: "this video has already been played"}
		}
	}
	return nil
}

// QueueClear drops every waiting item; the current one keeps playing.
func (r *Room) QueueClear(ctx context.Context, actor access.Actor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ManageQueue); err != nil {
		return err
	}
	if err := r.deps.Store.ClearQueue(ctx, r.info.ID, r.clock.current); err != nil {
		return err
	}
	kept := r.queue[:0]
	for _, it := range r.queue {
		if r.clock.current != nil && it.ID == *r.clock.current {
			kept = append(kept, it)
		}
	}
	dropped := len(r.queue) - len(kept)
	r.queue = kept
	if actor.User != nil && dropped > 0 {
		r.logLocked(ctx, fmt.Sprintf("%s cleared the queue (%d %s)", actor.User.Username, dropped, plural(dropped, "video", "videos")))
	}
	r.broadcastLocked()
	return nil
}

// QueueShuffle reorders the waiting items at random; the current one
// stays first. Vote mode owns the order and refuses.
func (r *Room) QueueShuffle(ctx context.Context, actor access.Actor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ManageQueue); err != nil {
		return err
	}
	if why := r.orderedByRule(); why != "" {
		return invalid(why)
	}
	waiting := len(r.queue)
	if r.clock.current != nil && len(r.queue) > 0 && r.queue[0].ID == *r.clock.current {
		waiting--
	}
	if waiting < 2 {
		return nil
	}
	if err := r.applyOrderLocked(ctx, shuffled(r.queue, r.clock.current, rand.Shuffle)); err != nil {
		return err
	}
	if actor.User != nil {
		r.logLocked(ctx, actor.User.Username+" shuffled the queue")
	}
	r.broadcastLocked()
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// QueueReplay re-queues an item from the history as a fresh entry.
func (r *Room) QueueReplay(ctx context.Context, actor access.Actor, itemID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.AddToQueue); err != nil {
		return err
	}
	var src *entity.QueueItem
	for _, it := range r.played {
		if it.ID == itemID {
			src = it
			break
		}
	}
	if src == nil {
		return notFound("played item")
	}
	if len(r.queue) >= 200 {
		return invalid("queue is full")
	}
	var addedBy *uuid.UUID
	if actor.User != nil {
		addedBy = &actor.User.ID
	}
	var item *entity.QueueItem
	err := r.deps.Store.InTx(ctx, func(q repository.Querier) error {
		var err error
		item, err = r.deps.Store.AddQueueItem(ctx, q, r.info.ID, src.MediaID, addedBy)
		return err
	})
	if err != nil {
		return err
	}
	if actor.User != nil {
		item.AddedByName = actor.User.Username
		r.logLocked(ctx, actor.User.Username+" re-added "+mediaLabel(r.media[src.MediaID]))
	}
	r.queue = append(r.queue, item)
	if err := r.fairReorderLocked(ctx); err != nil {
		return err
	}
	if r.clock.current == nil {
		r.setCurrentLocked(item)
		r.savePlaybackLocked(ctx)
	}
	r.broadcastLocked()
	return nil
}

// QueueClearPlayed empties the history.
func (r *Room) QueueClearPlayed(ctx context.Context, actor access.Actor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ManageQueue); err != nil {
		return err
	}
	if err := r.deps.Store.ClearPlayed(ctx, r.info.ID); err != nil {
		return err
	}
	r.played = r.played[:0]
	r.broadcastLocked()
	return nil
}

// admitMessage explains an admission refusal to the viewer; "" for
// anything else, which is an internal failure.
func admitMessage(err error) string {
	switch {
	case errors.Is(err, errUnsupported):
		return "this link is not supported"
	case errors.Is(err, errBlocked):
		return "this video has been blocked by an administrator"
	case errors.Is(err, errPrivate):
		return "links to private or local addresses are not allowed"
	case errors.Is(err, errUnknownHost):
		return "this site could not be found"
	default:
		return ""
	}
}

// QueueRemove deletes an item; removing the current one advances.
func (r *Room) QueueRemove(ctx context.Context, actor access.Actor, itemID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.itemByID(itemID)
	if item == nil {
		return notFound("queue item")
	}
	// Members may remove what they added themselves.
	own := actor.User != nil && item.AddedBy != nil && *item.AddedBy == actor.User.ID
	if !own || r.clock.isCurrent(item.ID) {
		if err := r.requireLocked(actor, access.ManageQueue); err != nil {
			return err
		}
	}
	if r.clock.isCurrent(itemID) {
		if err := r.nextLocked(ctx); err != nil {
			return err
		}
		r.broadcastLocked()
		return nil
	}
	if err := r.deps.Store.DeleteQueueItem(ctx, r.info.ID, itemID); err != nil {
		return err
	}
	if idx := r.indexOf(itemID); idx >= 0 {
		r.queue = append(r.queue[:idx], r.queue[idx+1:]...)
	}
	r.broadcastLocked()
	return nil
}

// QueueMove places an item after another (nil = head). The current item
// always stays first.
func (r *Room) QueueMove(ctx context.Context, actor access.Actor, itemID uuid.UUID, afterID *uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ManageQueue); err != nil {
		return err
	}
	if why := r.orderedByRule(); why != "" {
		return invalid(why)
	}
	idx := r.indexOf(itemID)
	if idx < 0 {
		return notFound("queue item")
	}
	if r.clock.isCurrent(itemID) {
		return invalid("the current item cannot be moved")
	}

	ordered, err := placeAfter(r.queue, idx, afterID, r.clock.current)
	if err != nil {
		return err
	}
	if err := r.applyOrderLocked(ctx, ordered); err != nil {
		return err
	}
	r.broadcastLocked()
	return nil
}

// QueueRetry re-queues a failed ingest.
func (r *Room) QueueRetry(ctx context.Context, actor access.Actor, itemID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ManageQueue); err != nil {
		return err
	}
	item := r.itemByID(itemID)
	if item == nil {
		return notFound("queue item")
	}
	m := r.media[item.MediaID]
	if m == nil || m.Status != entity.MediaFailed {
		return invalid("media is not in a failed state")
	}
	if err := r.deps.Admit.Retry(ctx, m); err != nil {
		return err
	}
	m.Status, m.Error, m.Progress = entity.MediaQueued, "", 0
	r.broadcastLocked()
	return nil
}

// MediaUpdated replaces a media row after an ingest progress event.
func (r *Room) MediaUpdated(m *entity.Media) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mediaUpdatedLocked(m) {
		r.broadcastLocked()
	}
}

// mediaUpdatedLocked stores a fresh media row and starts the current item
// when it just became playable; false when the room does not hold it.
func (r *Room) mediaUpdatedLocked(m *entity.Media) bool {
	old, ok := r.media[m.ID]
	if !ok {
		return false
	}
	r.media[m.ID] = m

	// The current item just became playable: start it.
	if cur := r.currentMedia(); cur != nil && cur.ID == m.ID && m.IsReady() && !old.IsReady() && !r.clock.playing && r.clock.positionMs == 0 {
		r.setPlaybackLocked(true, 0)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.savePlaybackLocked(ctx)
	}
	return true
}

// unsettledMedia lists the media the room holds that are not ready yet:
// what a missed progress notification could leave stale.
func (r *Room) unsettledMedia() []uuid.UUID {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []uuid.UUID
	for id, m := range r.media {
		if !m.IsReady() {
			ids = append(ids, id)
		}
	}
	return ids
}

// ReconcileMedia applies fresh rows for unsettled media whose status or
// progress moved on without the room hearing of it, as MediaUpdated
// would have, and broadcasts once if anything changed.
func (r *Room) ReconcileMedia(fresh map[uuid.UUID]*entity.Media) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	changed := false
	for id, old := range r.media {
		m, ok := fresh[id]
		if !ok || old.IsReady() || (m.Status == old.Status && m.Progress == old.Progress) {
			continue
		}
		changed = r.mediaUpdatedLocked(m) || changed
	}
	if changed {
		r.broadcastLocked()
	}
}

// ReloadQueue re-reads the queue after rows changed outside the room
// (an administrator deleted a media item).
func (r *Room) ReloadQueue(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.reloadQueue(ctx); err != nil {
		return err
	}
	if r.clock.current != nil && r.itemByID(*r.clock.current) == nil {
		var next *entity.QueueItem
		if len(r.queue) > 0 {
			next = r.queue[0]
		}
		r.setCurrentLocked(next)
		r.savePlaybackLocked(ctx)
	}
	r.broadcastLocked()
	return nil
}
