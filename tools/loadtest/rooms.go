package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// rooms runs o.rooms public rooms of o.viewers anonymous viewers each,
// every host seeking once a second; then every viewer drops its
// connection at once and comes back (a server restart or a network blip
// looks like this), and then everyone buffers at once.
func rooms(ctx context.Context, o options) error {
	ctx, stop := context.WithCancel(ctx) // ends the viewers
	defer stop()
	hosts, err := users(ctx, o.suffix, o.rooms)
	if err != nil {
		return err
	}
	urls, err := readyMedia(ctx, o.suffix, 1)
	if err != nil {
		return err
	}
	sk := newSeeks()
	var reconnects latencies
	var wg sync.WaitGroup
	var all []*viewer
	hvs := make([]*viewer, o.rooms)
	start := time.Now()
	for r := range o.rooms {
		slug := fmt.Sprintf("load-%s-%d", o.suffix, r)
		if err := post(ctx, o.target, hosts[r], "/api/v1/rooms", fmt.Sprintf(`{"name":"Load %s %d","slug":%q,"visibility":"public","firstUrl":%q}`, o.suffix, r, slug, urls[0])); err != nil {
			return err
		}
		url := o.ws + "/api/v1/rooms/" + slug + "/ws"
		hvs[r] = newViewer(url, hosts[r], true, nil)
		vs := make([]*viewer, o.viewers)
		for i := range vs {
			vs[i] = newViewer(url, nil, true, func(_ *viewer, typ string, data []byte) { sk.seen(typ, data) })
		}
		if _, err := connect(ctx, append([]*viewer{hvs[r]}, vs...), &wg, &reconnects); err != nil {
			return err
		}
		all = append(all, vs...)
	}
	connectTime := time.Since(start)

	// Hosts take turns, so seeks spread over the second.
	a := scrape(ctx, o.target)
	tick := time.NewTicker(time.Second / time.Duration(o.rooms))
	end := time.After(o.duration)
loop:
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-end:
			break loop
		case <-tick.C:
			pos := int64(10_000 + i*37)
			sk.sent(pos)
			_ = hvs[i%o.rooms].send(ctx, msg{"type": "seek", "positionMs": pos})
		}
	}
	tick.Stop()
	b := scrape(ctx, o.target)
	steady := sk.lat.String()
	secs := b.at.Sub(a.at).Seconds()
	fmt.Printf("profile rooms: %d rooms × %d anonymous viewers for %s\n", o.rooms, o.viewers, o.duration)
	fmt.Printf("connected in %s\n", connectTime.Round(time.Millisecond))
	fmt.Printf("seek to viewers: %s\n", steady)
	printCost("steady", a, b)
	printTraffic(secs)

	// Everyone at once: reconnect, then buffer.
	burst := time.Now()
	for _, v := range all {
		v.reconnect()
	}
	for deadline := time.Now().Add(time.Minute); reconnects.len() < len(all); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			break
		}
	}
	back := time.Since(burst)
	storm(ctx, all)
	c := scrape(ctx, o.target)
	fmt.Printf("all %d viewers reconnecting at once: %d back in %s, reconnect to welcome %s\n", len(all), reconnects.len(), back.Round(time.Millisecond), &reconnects)
	var closed int64
	for _, v := range all {
		closed += v.closedBy.Load()
	}
	fmt.Printf("closed by the server: %d\n", closed)
	printCost("burst and storm", b, c)
	printTraffic(c.at.Sub(b.at).Seconds())

	stop()
	wg.Wait()
	return nil
}
