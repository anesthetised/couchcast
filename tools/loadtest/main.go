// Command loadtest fills one room with WebSocket viewers and measures how
// quickly playback changes reach them, how the server copes with viewers
// that stop reading, and what the crowd costs in memory. `just load`
// runs it against the development stack; see docs/load.md for results.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/jackc/pgx/v5"
)

func main() {
	target := flag.String("target", "http://web:8080", "server base URL")
	viewers := flag.Int("viewers", 300, "reading viewers")
	stuck := flag.Int("stuck", 10, "viewers that never read")
	seeks := flag.Int("seeks", 60, "playback changes to time")
	every := flag.Duration("every", 200*time.Millisecond, "time between playback changes")
	storm := flag.Bool("storm", false, "every viewer reports buffering at once, then recovers")
	flag.Parse()
	stormMode = *storm
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	err := run(ctx, *target, *viewers, *stuck, *seeks, *every)
	cancel()
	if err != nil {
		log.Fatal(err)
	}
}

type msg map[string]any

var stormMode bool

// clock is a viewer's view of the room clock, from playback messages.
type clock struct {
	mu                 sync.Mutex
	pos, at            int64
	rate               float64
	playing, buffering bool
}

func (c *clock) set(m msg) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pos, c.at = int64(m["positionMs"].(float64)), int64(m["atServerMs"].(float64))
	c.rate, _ = m["rate"].(float64)
	c.playing, _ = m["playing"].(bool)
}

// now is where a well-behaved player would be (same host: clocks agree).
func (c *clock) now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.playing {
		return c.pos
	}
	return c.pos + int64(float64(time.Now().UnixMilli()-c.at)*c.rate)
}

func run(ctx context.Context, target string, viewers, stuck, seeks int, every time.Duration) error {
	ws := "ws" + strings.TrimPrefix(target, "http")
	suffix := fmt.Sprintf("%06d", rand.IntN(1_000_000)) //nolint:gosec // a name for test data, not a secret

	// A host with a room whose first video is ready to play.
	cookie, err := register(ctx, target, "load_"+suffix)
	if err != nil {
		return err
	}
	videoURL, err := readyMedia(ctx, suffix)
	if err != nil {
		return err
	}
	slug := "load-" + suffix
	defer cleanup(suffix, slug)
	if err := post(ctx, target, cookie, "/api/v1/rooms", fmt.Sprintf(`{"name":"Load %s","slug":%q,"visibility":"public","firstUrl":%q}`, suffix, slug, videoURL)); err != nil {
		return err
	}
	before := scrape(ctx, target)

	// Viewers record when each playback change reaches them.
	var (
		mu       sync.Mutex
		sentAt   = map[int64]time.Time{}
		latency  []time.Duration
		closedBy atomic.Int64
	)
	start := time.Now()
	var wg sync.WaitGroup
	conns := make([]*websocket.Conn, 0, viewers)
	clocks := make([]*clock, 0, viewers)
	for i := 0; i < viewers; i++ {
		c, err := dial(ctx, ws+"/api/v1/rooms/"+slug+"/ws", nil)
		if err != nil {
			return fmt.Errorf("viewer %d: %w", i, err)
		}
		conns = append(conns, c)
		clk := &clock{rate: 1}
		clocks = append(clocks, clk)
		wg.Add(1)
		go func() {
			defer wg.Done()
			go heartbeat(ctx, c, clk)
			for {
				var m msg
				if err := wsjson.Read(ctx, c, &m); err != nil {
					if websocket.CloseStatus(err) == websocket.StatusPolicyViolation {
						closedBy.Add(1)
					}
					return
				}
				if m["type"] == "welcome" {
					if s, ok := m["snapshot"].(map[string]any); ok {
						clk.set(s["playback"].(map[string]any))
					}
					continue
				}
				if m["type"] != "playback" {
					continue
				}
				clk.set(m)
				pos := int64(m["positionMs"].(float64))
				now := time.Now()
				mu.Lock()
				if t, ok := sentAt[pos]; ok {
					latency = append(latency, now.Sub(t))
				}
				mu.Unlock()
			}
		}()
	}
	connectTime := time.Since(start)

	// Stuck viewers connect and never read again.
	stuckConns := make([]*websocket.Conn, 0, stuck)
	for i := 0; i < stuck; i++ {
		c, err := dial(ctx, ws+"/api/v1/rooms/"+slug+"/ws", nil)
		if err != nil {
			return err
		}
		stuckConns = append(stuckConns, c)
	}
	loaded := scrape(ctx, target)

	host, err := dial(ctx, ws+"/api/v1/rooms/"+slug+"/ws", http.Header{"Cookie": {cookie}})
	if err != nil {
		return err
	}
	go func() { // drain the host's own messages
		for {
			if _, _, err := host.Read(ctx); err != nil {
				log.Printf("host connection: %v", err)
				return
			}
		}
	}()
	time.Sleep(time.Second)
	for i := 0; i < seeks; i++ {
		pos := int64(10_000 + i*37)
		mu.Lock()
		sentAt[pos] = time.Now()
		mu.Unlock()
		if err := wsjson.Write(ctx, host, msg{"type": "seek", "positionMs": pos}); err != nil {
			return err
		}
		time.Sleep(every)
	}

	if stormMode {
		// Everyone's network hiccups at the same moment.
		for i, c := range conns {
			clk := clocks[i]
			clk.mu.Lock()
			clk.buffering = true
			clk.mu.Unlock()
			_ = wsjson.Write(ctx, c, msg{"type": "report", "state": "buffering", "positionMs": clk.now()})
		}
		time.Sleep(3 * time.Second)
		for i, c := range conns {
			clk := clocks[i]
			clk.mu.Lock()
			clk.buffering = false
			clk.mu.Unlock()
			_ = wsjson.Write(ctx, c, msg{"type": "report", "state": "playing", "positionMs": clk.now()})
		}
		time.Sleep(3 * time.Second)
	}

	// Big snapshots (settings changes) until stuck viewers overflow.
	for i := 0; i < 400; i++ {
		_ = wsjson.Write(ctx, host, msg{"type": "settings.set", "voteMode": i%2 == 0})
		time.Sleep(55 * time.Millisecond) // under the 20 commands/s budget
	}
	time.Sleep(2 * time.Second)
	after := scrape(ctx, target)

	for _, c := range append(append(conns, stuckConns...), host) {
		_ = c.CloseNow()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	report(viewers, stuck, seeks, every, connectTime, latency, closedBy.Load(), before, loaded, after)
	return nil
}

// heartbeat pings and reports like the web app: every 5 s, with the
// position a player on the room clock would have.
func heartbeat(ctx context.Context, c *websocket.Conn, clk *clock) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if wsjson.Write(ctx, c, msg{"type": "ping", "t0": time.Now().UnixMilli()}) != nil {
				return
			}
			clk.mu.Lock()
			state := "playing"
			if clk.buffering {
				state = "buffering"
			}
			clk.mu.Unlock()
			if wsjson.Write(ctx, c, msg{"type": "report", "state": state, "positionMs": clk.now()}) != nil {
				return
			}
		}
	}
}

func report(viewers, stuck, seeks int, every, connect time.Duration, lat []time.Duration, closed int64, before, loaded, after map[string]float64) {
	slices.Sort(lat)
	pct := func(p float64) string {
		if len(lat) == 0 {
			return "-"
		}
		return lat[int(math.Min(float64(len(lat)-1), p*float64(len(lat))))].Round(100 * time.Microsecond).String()
	}
	expected := viewers * seeks
	fmt.Printf("viewers %d (+%d stuck), %d playback changes every %s\n", viewers, stuck, seeks, every)
	fmt.Printf("connected in %s\n", connect.Round(time.Millisecond))
	fmt.Printf("playback delivered %d/%d, latency p50 %s p95 %s p99 %s max %s\n", len(lat), expected, pct(0.5), pct(0.95), pct(0.99), pct(1))
	// Before the flood every connection is open; after it the stuck ones
	// should be gone and no reading viewer closed.
	fmt.Printf("open connections %.0f → %.0f after the flood (%d stuck); reading viewers closed: %d\n", loaded["conns"], after["conns"], stuck, closed)
	mb := func(m map[string]float64) string { return fmt.Sprintf("%.1f MB", m["mem"]/1e6) }
	fmt.Printf("server heap %s idle, %s with the crowd; goroutines %.0f → %.0f\n", mb(before), mb(loaded), before["goroutines"], loaded["goroutines"])
}

// scrape reads the web server's heap in use, goroutines and open
// WebSocket connections.
func scrape(ctx context.Context, target string) map[string]float64 {
	out := map[string]float64{}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target+"/metrics", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return out
	}
	defer func() { _ = resp.Body.Close() }()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		var v float64
		switch {
		case strings.HasPrefix(line, "go_memstats_heap_inuse_bytes "):
			_, _ = fmt.Sscan(strings.Fields(line)[1], &v)
			out["mem"] = v
		case strings.HasPrefix(line, "couchcast_ws_connections"):
			_, _ = fmt.Sscan(strings.Fields(line)[1], &v)
			out["conns"] = v
		case strings.HasPrefix(line, "go_goroutines "):
			_, _ = fmt.Sscan(strings.Fields(line)[1], &v)
			out["goroutines"] = v
		}
	}
	return out
}

func dial(ctx context.Context, url string, h http.Header) (*websocket.Conn, error) {
	c, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: h})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(1 << 20)
	return c, nil
}

func register(ctx context.Context, target, name string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, target+"/api/v1/auth/register",
		strings.NewReader(fmt.Sprintf(`{"username":%q,"password":"load-test-password"}`, name)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("register: %s", resp.Status)
	}
	return resp.Cookies()[0].String(), nil
}

func post(ctx context.Context, target, cookie, path, body string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, target+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		var e struct{ Error string }
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("POST %s: %s %s", path, resp.Status, e.Error)
	}
	return nil
}

// cleanup removes what the run created: the room (queue and chat go with
// it), the host and the media row.
func cleanup(suffix, slug string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, os.Getenv("COUCHCAST_DATABASE_URL"))
	if err != nil {
		log.Printf("cleanup: %v", err)
		return
	}
	defer func() { _ = conn.Close(ctx) }()
	for _, q := range []string{
		`DELETE FROM rooms WHERE slug = $1`,
		`DELETE FROM users WHERE username = $1`,
		`DELETE FROM media WHERE source_key = $1`,
	} {
		arg := slug
		switch {
		case strings.Contains(q, "users"):
			arg = "load_" + suffix
		case strings.Contains(q, "media"):
			arg = "youtube:load" + suffix + "x"
		}
		if _, err := conn.Exec(ctx, q, arg); err != nil {
			log.Printf("cleanup: %v", err)
		}
	}
}

// readyMedia stores a ready media row (no files: the room only needs its
// duration to run the clock) and returns the link that finds it.
func readyMedia(ctx context.Context, suffix string) (string, error) {
	conn, err := pgx.Connect(ctx, os.Getenv("COUCHCAST_DATABASE_URL"))
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close(ctx) }()
	id := "load" + suffix + "x" // 11 characters, a YouTube id
	url := "https://www.youtube.com/watch?v=" + id
	_, err = conn.Exec(ctx, `
		INSERT INTO media (source_key, source_url, title, duration_ms, status, progress, s3_prefix)
		VALUES ($1, $2, 'Load test', 3600000, 'ready', 1, 'media/load/')`, "youtube:"+id, url)
	return url, err
}
