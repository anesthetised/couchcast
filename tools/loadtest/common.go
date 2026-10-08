package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/jackc/pgx/v5"
)

type msg map[string]any

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

func (c *clock) setBuffering(b bool) {
	c.mu.Lock()
	c.buffering = b
	c.mu.Unlock()
}

// traffic counts what the server sent to every client, by message type.
var traffic = struct {
	sync.Mutex
	n, bytes map[string]int64
}{n: map[string]int64{}, bytes: map[string]int64{}}

var errorsLogged atomic.Int64

func countTraffic(typ string, size int) {
	traffic.Lock()
	traffic.n[typ]++
	traffic.bytes[typ] += int64(size)
	traffic.Unlock()
}

// read returns the next message's type and bytes, counting it in
// traffic and keeping the viewer's clock current. Only what a profile
// needs is decoded: the load tool shares the machine with the server,
// and decoding every snapshot would make it the bottleneck.
func read(ctx context.Context, c *websocket.Conn, clk *clock) (string, []byte, error) {
	_, data, err := c.Read(ctx)
	if err != nil {
		return "", nil, err
	}
	typ := typeOf(data)
	countTraffic(typ, len(data))
	if typ == "error" && errorsLogged.Add(1) <= 5 {
		log.Printf("server error: %s", data)
	}
	switch typ {
	case "welcome":
		var w struct{ Snapshot struct{ Playback msg } }
		if json.Unmarshal(data, &w) == nil && w.Snapshot.Playback != nil {
			clk.set(w.Snapshot.Playback)
		}
	case "playback":
		var m msg
		if json.Unmarshal(data, &m) == nil {
			clk.set(m)
		}
	}
	return typ, data, nil
}

// typeOf reads the message type, which the server writes first.
func typeOf(data []byte) string {
	const prefix = `{"type":"`
	if rest, ok := bytes.CutPrefix(data, []byte(prefix)); ok {
		if i := bytes.IndexByte(rest, '"'); i >= 0 {
			return string(rest[:i])
		}
	}
	var m struct{ Type string }
	_ = json.Unmarshal(data, &m)
	return m.Type
}

// latencies collects how long things took, safe for concurrent use.
type latencies struct {
	mu sync.Mutex
	d  []time.Duration
}

func (l *latencies) add(d time.Duration) {
	l.mu.Lock()
	l.d = append(l.d, d)
	l.mu.Unlock()
}

func (l *latencies) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	slices.Sort(l.d)
	pct := func(p float64) string {
		if len(l.d) == 0 {
			return "-"
		}
		return l.d[int(math.Min(float64(len(l.d)-1), p*float64(len(l.d))))].Round(100 * time.Microsecond).String()
	}
	return fmt.Sprintf("n %d, p50 %s p95 %s p99 %s max %s", len(l.d), pct(0.5), pct(0.95), pct(0.99), pct(1))
}

func (l *latencies) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.d)
}

// seeks times how long a host's seek takes to reach each viewer: the
// position is the key, so every seek must go to a position of its own.
type seeks struct {
	mu     sync.Mutex
	sentAt map[int64]time.Time
	lat    latencies
}

func newSeeks() *seeks { return &seeks{sentAt: map[int64]time.Time{}} }

func (s *seeks) sent(pos int64) {
	s.mu.Lock()
	s.sentAt[pos] = time.Now()
	s.mu.Unlock()
}

// seen records a playback message arriving at a viewer.
func (s *seeks) seen(typ string, data []byte) {
	if typ != "playback" {
		return
	}
	var m struct{ PositionMs int64 }
	if json.Unmarshal(data, &m) != nil {
		return
	}
	pos := m.PositionMs
	s.mu.Lock()
	t, ok := s.sentAt[pos]
	s.mu.Unlock()
	if ok {
		s.lat.add(time.Since(t))
	}
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
			if report(ctx, c, clk) != nil {
				return
			}
		}
	}
}

func report(ctx context.Context, c *websocket.Conn, clk *clock) error {
	clk.mu.Lock()
	state := "playing"
	if clk.buffering {
		state = "buffering"
	}
	clk.mu.Unlock()
	return wsjson.Write(ctx, c, msg{"type": "report", "state": state, "positionMs": clk.now()})
}

// storm has every viewer report buffering at the same moment, then
// recover, as the start of a big session does.
func storm(ctx context.Context, vs []*viewer) {
	for _, buffering := range []bool{true, false} {
		for _, v := range vs {
			v.clk.setBuffering(buffering)
			if c := v.conn.Load(); c != nil {
				_ = report(ctx, c, &v.clk)
			}
		}
		time.Sleep(3 * time.Second)
	}
}

// server is what the web server and the database say about themselves.
type server struct {
	at                    time.Time
	heap, goroutines, cpu float64
	conns, xacts, tuples  float64
	self                  float64 // the load tool's own CPU seconds
}

// scrape reads the web server's heap, goroutines, CPU time and open
// WebSocket connections, and the database's committed transactions and
// rows returned (pg_stat_database; the load tool's own few count too).
func scrape(ctx context.Context, target string) server {
	s := server{at: time.Now()}
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
		s.self = time.Duration(ru.Utime.Nano() + ru.Stime.Nano()).Seconds()
	}
	if conn, err := pgx.Connect(ctx, os.Getenv("COUCHCAST_DATABASE_URL")); err == nil {
		_ = conn.QueryRow(ctx, `SELECT xact_commit, tup_returned + tup_fetched FROM pg_stat_database WHERE datname = current_database()`).Scan(&s.xacts, &s.tuples)
		_ = conn.Close(ctx)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target+"/metrics", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return s
	}
	defer func() { _ = resp.Body.Close() }()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 {
			continue
		}
		var v float64
		_, _ = fmt.Sscan(f[1], &v)
		switch strings.SplitN(f[0], "{", 2)[0] { // labels aside
		case "go_memstats_heap_inuse_bytes":
			s.heap = v
		case "go_goroutines":
			s.goroutines = v
		case "process_cpu_seconds_total":
			s.cpu = v
		case "couchcast_ws_connections":
			s.conns = v
		}
	}
	return s
}

// printCost reports what a phase cost the server, from two scrapes.
func printCost(phase string, a, b server) {
	secs := b.at.Sub(a.at).Seconds()
	fmt.Printf("%s (%.0f s): server CPU %.2f cores (load tool %.2f), %.0f db transactions/s, %.0f rows read/s; heap %.1f → %.1f MB, goroutines %.0f → %.0f\n",
		phase, secs, (b.cpu-a.cpu)/secs, (b.self-a.self)/secs, (b.xacts-a.xacts)/secs, (b.tuples-a.tuples)/secs, a.heap/1e6, b.heap/1e6, a.goroutines, b.goroutines)
}

// printTraffic reports what the server sent, by message type, and resets
// the counters.
func printTraffic(secs float64) {
	traffic.Lock()
	defer traffic.Unlock()
	var total int64
	types := make([]string, 0, len(traffic.n))
	for t, b := range traffic.bytes {
		types = append(types, t)
		total += b
	}
	slices.SortFunc(types, func(a, b string) int { return int(traffic.bytes[b] - traffic.bytes[a]) })
	fmt.Printf("sent to clients: %.1f MB/s", float64(total)/secs/1e6)
	for _, t := range types {
		fmt.Printf("; %s %d × %.1f kB", t, traffic.n[t], float64(traffic.bytes[t])/float64(traffic.n[t])/1e3)
	}
	fmt.Println()
	clear(traffic.n)
	clear(traffic.bytes)
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

// users signs n users in without the API (registration is rate limited
// per address and hashes passwords slowly): rows in users and sessions,
// returning one session cookie header per user. Nobody can log in as
// them; cleanup removes them by name.
func users(ctx context.Context, suffix string, n int) ([]http.Header, error) {
	conn, err := pgx.Connect(ctx, os.Getenv("COUCHCAST_DATABASE_URL"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(ctx) }()
	names := make([]string, n)
	hashes := make([][]byte, n)
	out := make([]http.Header, n)
	for i := range n {
		names[i] = fmt.Sprintf("load_%s_%d", suffix, i)
		token := rand.Text() // the server stores the cookie value's SHA-256
		sum := sha256.Sum256([]byte(token))
		hashes[i] = sum[:]
		out[i] = http.Header{"Cookie": {"couchcast_session=" + token}}
	}
	_, err = conn.Exec(ctx, `
		WITH u AS (
			INSERT INTO users (username, password_hash)
			SELECT name, '!' FROM unnest($1::text[]) AS name
			RETURNING id, username)
		INSERT INTO sessions (token_hash, user_id, user_agent, expires_at)
		SELECT h.hash, u.id, 'loadtest', now() + interval '1 day'
		FROM unnest($1::text[], $2::bytea[]) AS h(name, hash) JOIN u ON u.username = h.name`, names, hashes)
	return out, err
}

func post(ctx context.Context, target string, h http.Header, path, body string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, target+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", h.Get("Cookie"))
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

// cleanup removes what the run created: its rooms (queue and chat go with
// them), its users and its media rows.
func cleanup(suffix string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, os.Getenv("COUCHCAST_DATABASE_URL"))
	if err != nil {
		log.Printf("cleanup: %v", err)
		return
	}
	defer func() { _ = conn.Close(ctx) }()
	for _, q := range []string{
		`DELETE FROM rooms WHERE slug LIKE 'load-' || $1 || '%'`,
		`DELETE FROM users WHERE username LIKE 'load\_' || $1 || '%'`,
		`DELETE FROM media WHERE source_key LIKE 'youtube:l' || $1 || '%'`,
	} {
		if _, err := conn.Exec(ctx, q, suffix); err != nil {
			log.Printf("cleanup: %v", err)
		}
	}
}

// readyMedia stores n ready media rows (no files: a room only needs the
// duration to run the clock) and returns the links that find them.
func readyMedia(ctx context.Context, suffix string, n int) ([]string, error) {
	conn, err := pgx.Connect(ctx, os.Getenv("COUCHCAST_DATABASE_URL"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(ctx) }()
	urls := make([]string, n)
	for i := range n {
		id := fmt.Sprintf("l%s%04d", suffix, i) // 11 characters, a YouTube id
		urls[i] = "https://www.youtube.com/watch?v=" + id
		if _, err := conn.Exec(ctx, `
			INSERT INTO media (source_key, source_url, title, duration_ms, status, progress, s3_prefix)
			VALUES ($1, $2, $3, 3600000, 'ready', 1, 'media/load/')`, "youtube:"+id, urls[i], fmt.Sprintf("Load test %d", i)); err != nil {
			return nil, err
		}
	}
	return urls, nil
}

// viewer is one client: it reads until its connection ends and, when
// redial is set, connects again (a reconnect asked for with reconnect is
// timed from the close to the new welcome).
type viewer struct {
	url    string
	h      http.Header
	redial bool
	onMsg  func(v *viewer, typ string, data []byte)

	conn     atomic.Pointer[websocket.Conn]
	clk      clock
	ready    chan struct{} // closed on the first welcome
	once     sync.Once
	closedAt atomic.Int64 // when reconnect closed the connection
	closedBy atomic.Int64 // closes the server started
}

func newViewer(url string, h http.Header, redial bool, onMsg func(*viewer, string, []byte)) *viewer {
	if onMsg == nil {
		onMsg = func(*viewer, string, []byte) {}
	}
	return &viewer{url: url, h: h, redial: redial, onMsg: onMsg, clk: clock{rate: 1}, ready: make(chan struct{})}
}

func (v *viewer) run(ctx context.Context, reconnects *latencies) {
	for ctx.Err() == nil {
		c, err := dial(ctx, v.url, v.h)
		if err != nil {
			log.Printf("dial: %v", err)
			if !v.redial {
				return
			}
			time.Sleep(time.Second)
			continue
		}
		v.conn.Store(c)
		hb, stop := context.WithCancel(ctx)
		go heartbeat(hb, c, &v.clk)
		for {
			typ, data, err := read(ctx, c, &v.clk)
			if err != nil {
				if websocket.CloseStatus(err) != -1 {
					v.closedBy.Add(1)
				}
				break
			}
			if typ == "welcome" {
				if t := v.closedAt.Swap(0); t != 0 && reconnects != nil {
					reconnects.add(time.Since(time.Unix(0, t)))
				}
				v.once.Do(func() { close(v.ready) })
			}
			v.onMsg(v, typ, data)
		}
		stop()
		if !v.redial {
			return
		}
	}
}

// reconnect drops the connection; run dials again.
func (v *viewer) reconnect() {
	v.closedAt.Store(time.Now().UnixNano())
	if c := v.conn.Load(); c != nil {
		_ = c.CloseNow()
	}
}

func (v *viewer) send(ctx context.Context, m msg) error {
	c := v.conn.Load()
	if c == nil {
		return fmt.Errorf("not connected")
	}
	return wsjson.Write(ctx, c, m)
}

// connect starts the viewers one after another, each once the previous
// one is welcomed, and returns how long that took.
func connect(ctx context.Context, vs []*viewer, wg *sync.WaitGroup, reconnects *latencies) (time.Duration, error) {
	start := time.Now()
	for i, v := range vs {
		wg.Go(func() { v.run(ctx, reconnects) })
		select {
		case <-v.ready:
		case <-time.After(10 * time.Second):
			return 0, fmt.Errorf("viewer %d was not welcomed", i)
		}
	}
	return time.Since(start), nil
}
