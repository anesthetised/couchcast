// Command loadtest puts WebSocket load on the running server and measures
// how quickly room changes reach viewers and what they cost the server.
// `just load` runs it against the development stack; see docs/load.md for
// the profiles and their results:
//
//   - crowd: anonymous viewers in one room, a host seeking, viewers that
//     stop reading (and -storm: everyone buffers at once)
//   - votes: signed-in viewers in one vote-mode room voting, adding and
//     coming and going while the host seeks
//   - rooms: many rooms at once, then every viewer reconnects and
//     buffers at the same moment
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type options struct {
	target, ws, suffix string
	viewers, rooms     int
	stuck, seeks       int
	every, duration    time.Duration
	storm              bool
}

func main() {
	var o options
	profile := flag.String("profile", "crowd", "crowd, votes or rooms")
	flag.StringVar(&o.target, "target", "http://web:8080", "server base URL")
	flag.IntVar(&o.viewers, "viewers", 300, "reading viewers (per room for -profile rooms)")
	flag.IntVar(&o.rooms, "rooms", 20, "rooms (rooms)")
	flag.IntVar(&o.stuck, "stuck", 10, "viewers that never read (crowd)")
	flag.IntVar(&o.seeks, "seeks", 60, "playback changes to time (crowd)")
	flag.DurationVar(&o.every, "every", 200*time.Millisecond, "time between playback changes (crowd)")
	flag.DurationVar(&o.duration, "duration", 2*time.Minute, "how long the load runs (votes, rooms)")
	flag.BoolVar(&o.storm, "storm", false, "every viewer reports buffering at once, then recovers (crowd)")
	flag.Parse()
	o.ws = "ws" + strings.TrimPrefix(o.target, "http")
	o.suffix = fmt.Sprintf("%06d", rand.IntN(1_000_000)) //nolint:gosec // a name for test data, not a secret

	run := map[string]func(context.Context, options) error{"crowd": crowd, "votes": votes, "rooms": rooms}[*profile]
	if run == nil {
		log.Fatalf("unknown profile %q", *profile)
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.duration+10*time.Minute)
	err := run(ctx, o)
	cancel()
	cleanup(o.suffix)
	if err != nil {
		log.Fatal(err)
	}
}

// crowd fills one public room with anonymous viewers, has the host seek
// every o.every and then flood big snapshots until the viewers that
// never read are dropped.
func crowd(ctx context.Context, o options) error {
	// A host with a room whose first video is ready to play.
	cookie, err := register(ctx, o.target, "load_"+o.suffix)
	if err != nil {
		return err
	}
	host := http.Header{"Cookie": {cookie}}
	urls, err := readyMedia(ctx, o.suffix, 1)
	if err != nil {
		return err
	}
	slug := "load-" + o.suffix
	if err := post(ctx, o.target, host, "/api/v1/rooms", fmt.Sprintf(`{"name":"Load %s","slug":%q,"visibility":"public","firstUrl":%q}`, o.suffix, slug, urls[0])); err != nil {
		return err
	}
	before := scrape(ctx, o.target)

	// Viewers record when each playback change reaches them.
	sk := newSeeks()
	var wg sync.WaitGroup
	vs := make([]*viewer, o.viewers)
	for i := range vs {
		vs[i] = newViewer(o.ws+"/api/v1/rooms/"+slug+"/ws", nil, false, func(_ *viewer, typ string, data []byte) { sk.seen(typ, data) })
	}
	connectTime, err := connect(ctx, vs, &wg, nil)
	if err != nil {
		return err
	}

	// Stuck viewers connect and never read again.
	stuckConns := make([]*websocket.Conn, 0, o.stuck)
	for range o.stuck {
		c, err := dial(ctx, o.ws+"/api/v1/rooms/"+slug+"/ws", nil)
		if err != nil {
			return err
		}
		stuckConns = append(stuckConns, c)
	}
	loaded := scrape(ctx, o.target)

	hc, err := dial(ctx, o.ws+"/api/v1/rooms/"+slug+"/ws", host)
	if err != nil {
		return err
	}
	go drain(ctx, hc)
	time.Sleep(time.Second)
	for i := range o.seeks {
		pos := int64(10_000 + i*37)
		sk.sent(pos)
		if err := wsjson.Write(ctx, hc, msg{"type": "seek", "positionMs": pos}); err != nil {
			return err
		}
		time.Sleep(o.every)
	}

	if o.storm {
		storm(ctx, vs)
	}

	// Big snapshots (settings changes) until stuck viewers overflow.
	for i := range 400 {
		_ = wsjson.Write(ctx, hc, msg{"type": "settings.set", "voteMode": i%2 == 0})
		time.Sleep(55 * time.Millisecond) // under the 20 commands/s budget
	}
	time.Sleep(2 * time.Second)
	after := scrape(ctx, o.target)

	for _, c := range append(stuckConns, hc) {
		_ = c.CloseNow()
	}
	for _, v := range vs {
		v.reconnect()
	}
	wg.Wait()
	var closedBy int64
	for _, v := range vs {
		closedBy += v.closedBy.Load()
	}

	fmt.Printf("viewers %d (+%d stuck), %d playback changes every %s\n", o.viewers, o.stuck, o.seeks, o.every)
	fmt.Printf("connected in %s\n", connectTime.Round(time.Millisecond))
	fmt.Printf("playback delivered %d/%d, latency %s\n", sk.lat.len(), o.viewers*o.seeks, &sk.lat)
	// Before the flood every connection is open; after it the stuck ones
	// should be gone and no reading viewer closed.
	fmt.Printf("open connections %.0f → %.0f after the flood (%d stuck); reading viewers closed: %d\n", loaded.conns, after.conns, o.stuck, closedBy)
	fmt.Printf("server heap %.1f MB idle, %.1f MB with the crowd; goroutines %.0f → %.0f\n", before.heap/1e6, loaded.heap/1e6, before.goroutines, loaded.goroutines)
	return nil
}

// drain reads and discards a connection's messages.
func drain(ctx context.Context, c *websocket.Conn) {
	for {
		if _, _, err := c.Read(ctx); err != nil {
			return
		}
	}
}
