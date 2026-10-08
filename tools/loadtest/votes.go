package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"
)

// votes is one vote-mode room of signed-in viewers: every snapshot is
// personalised (each viewer's own votes), one viewer votes every second,
// one adds a video every 5 s, 2 % of the viewers leave and come back
// every minute, and the host seeks every second.
func votes(ctx context.Context, o options) error {
	ctx, stop := context.WithCancel(ctx) // ends the viewers
	defer stop()
	hs, err := users(ctx, o.suffix, o.viewers+1)
	if err != nil {
		return err
	}
	host := hs[0]
	const initial = 20
	adds := int(o.duration/(5*time.Second)) + 1
	urls, err := readyMedia(ctx, o.suffix, 1+initial+adds)
	if err != nil {
		return err
	}
	slug := "load-" + o.suffix
	if err := post(ctx, o.target, host, "/api/v1/rooms", fmt.Sprintf(`{"name":"Load %s","slug":%q,"visibility":"public","firstUrl":%q,"settings":{"voteMode":true,"viewersCanAdd":true}}`, o.suffix, slug, urls[0])); err != nil {
		return err
	}
	url := o.ws + "/api/v1/rooms/" + slug + "/ws"

	// The host keeps the list of items that can be voted on.
	var items atomic.Pointer[[]string]
	items.Store(&[]string{})
	var wg sync.WaitGroup
	hv := newViewer(url, host, false, func(_ *viewer, typ string, data []byte) {
		if q, ok := queueOf(typ, data); ok {
			ids := make([]string, 0, len(q))
			for _, e := range q {
				if !e.Current {
					ids = append(ids, e.ID)
				}
			}
			items.Store(&ids)
		}
	})
	if _, err := connect(ctx, []*viewer{hv}, &wg, nil); err != nil {
		return err
	}
	if err := hv.send(ctx, msg{"type": "queue.addMany", "urls": urls[1 : 1+initial]}); err != nil {
		return err
	}
	for deadline := time.Now().Add(10 * time.Second); len(*items.Load()) < initial; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			return fmt.Errorf("queued %d of %d items", len(*items.Load()), initial)
		}
	}

	// A viewer's vote counts from sending it until its own snapshot says
	// so. Viewers remember their own votes, so only one waiting for a vote
	// decodes its snapshots.
	sk := newSeeks()
	var voteLat, reconnects latencies
	type pending struct {
		want bool
		at   time.Time
	}
	type voter struct {
		mu      sync.Mutex
		voted   map[string]bool
		pending map[string]pending
	}
	voters := map[*viewer]*voter{}
	vs := make([]*viewer, o.viewers)
	for i := range vs {
		vt := &voter{voted: map[string]bool{}, pending: map[string]pending{}}
		vs[i] = newViewer(url, hs[i+1], true, func(_ *viewer, typ string, data []byte) {
			sk.seen(typ, data)
			vt.mu.Lock()
			defer vt.mu.Unlock()
			if len(vt.pending) == 0 {
				return
			}
			q, _ := queueOf(typ, data)
			for _, e := range q {
				if p, ok := vt.pending[e.ID]; ok && p.want == e.Voted {
					voteLat.add(time.Since(p.at))
					delete(vt.pending, e.ID)
				}
			}
		})
		voters[vs[i]] = vt
	}
	connectTime, err := connect(ctx, vs, &wg, &reconnects)
	if err != nil {
		return err
	}

	a := scrape(ctx, o.target)
	var sent, added, left int
	vote := time.NewTicker(time.Second)
	time.Sleep(500 * time.Millisecond) // seeks between the votes, not with them
	seek := time.NewTicker(time.Second)
	add := time.NewTicker(5 * time.Second)
	churn := time.NewTicker(time.Duration(float64(time.Minute) / max(1, 0.02*float64(o.viewers))))
	end := time.After(o.duration)
	next := 1 + initial
loop:
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-end:
			break loop
		case <-seek.C:
			pos := int64(10_000 + i*37)
			sk.sent(pos)
			_ = hv.send(ctx, msg{"type": "seek", "positionMs": pos})
		case <-vote.C:
			ids := *items.Load()
			if len(ids) == 0 {
				continue
			}
			v := vs[rand.IntN(len(vs))]    //nolint:gosec // load pattern, not security
			id := ids[rand.IntN(len(ids))] //nolint:gosec
			vt := voters[v]
			vt.mu.Lock()
			vt.voted[id] = !vt.voted[id]
			vt.pending[id] = pending{want: vt.voted[id], at: time.Now()}
			vt.mu.Unlock()
			if v.send(ctx, msg{"type": "queue.vote", "itemId": id}) == nil {
				sent++
			}
		case <-add.C:
			if next < len(urls) && vs[rand.IntN(len(vs))].send(ctx, msg{"type": "queue.add", "url": urls[next]}) == nil { //nolint:gosec
				next++
				added++
			}
		case <-churn.C:
			vs[rand.IntN(len(vs))].reconnect() //nolint:gosec
			left++
		}
	}
	time.Sleep(2 * time.Second) // let the last votes and reconnects land
	b := scrape(ctx, o.target)

	stop()
	wg.Wait()

	fmt.Printf("profile votes: %d signed-in viewers in one vote-mode room for %s\n", o.viewers, o.duration)
	fmt.Printf("connected in %s\n", connectTime.Round(time.Millisecond))
	fmt.Printf("seek to viewers: %s\n", &sk.lat)
	fmt.Printf("vote to the voter's snapshot: %d sent, %s\n", sent, &voteLat)
	fmt.Printf("added %d videos; %d viewers left and came back, reconnect to welcome %s\n", added, left, &reconnects)
	printCost("load", a, b)
	printTraffic(b.at.Sub(a.at).Seconds())
	return nil
}

// entry is the part of a queue entry the profile reads.
type entry struct {
	ID             string
	Voted, Current bool
}

// queueOf decodes the queue of a snapshot (welcome or room.state).
func queueOf(typ string, data []byte) ([]entry, bool) {
	switch typ {
	case "welcome":
		var w struct{ Snapshot struct{ Queue []entry } }
		err := json.Unmarshal(data, &w)
		return w.Snapshot.Queue, err == nil
	case "room.state":
		var s struct{ Queue []entry }
		err := json.Unmarshal(data, &s)
		return s.Queue, err == nil
	}
	return nil, false
}
