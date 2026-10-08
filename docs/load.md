# Load test

`just load` (`tools/loadtest`) runs one of three profiles against the
running dev stack. Each is a workload of its own; a number from one says
nothing about the others. Every profile also reports the server's CPU
(`process_cpu_seconds_total`), the load tool's own CPU (it shares the
machine, and once it saturates the latencies are its own), committed
database transactions and rows read (`pg_stat_database`: the whole
database, the tool's few setup queries included) and the bytes the
server sent to clients by message type. Clients are synthetic: nothing
here decodes video, fetches media or measures playback drift (#127).
Each run removes the users, rooms and media rows it created.

## Profile `crowd` (default)

One public room of anonymous WebSocket viewers that behave like the web app
(ping and report every 5 s, reporting the position a player on the room
clock would have), adds viewers that never read, and has the host seek
every 200 ms. It measures how long each playback change takes to reach
each viewer, whether the server drops the viewers that stopped reading
without touching the others, and the server's heap and goroutines.

```sh
just load -viewers 2000 -stuck 10        # the usual run
just load -viewers 1000 -stuck 0 -storm  # everyone buffers at once
```

### Results (2026-09-26)

Server and clients on one machine: Apple silicon laptop, Docker Desktop
VM with 10 CPUs and 16 GB, the dev build of the web server. Latency is
from the host sending `seek` to a viewer receiving the `playback`.

| Viewers | Delivered | p50 | p95 | p99 | max | Heap with the crowd | Connect |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 300 | 18 000 / 18 000 | 8 ms | 13 ms | 15 ms | 17 ms | 27 MB | 0.7 s |
| 1 000 | 60 000 / 60 000 | 11 ms | 20 ms | 23 ms | 25 ms | 108 MB | 0.8 s |
| 2 000 | 120 000 / 120 000 | 13 ms | 24 ms | 32 ms | 39 ms | 70 MB | 1.4 s |

In every run the 10 viewers that stopped reading were disconnected
("slow client") once their 256-message buffer filled, and no reading
viewer was; each connection costs two goroutines.

Rerun on 2026-10-08 (same machine, `1e2205f` plus the load tool
changes): 2 000 viewers, 120 000 / 120 000 delivered, p50 12 ms, p95
26 ms, p99 41 ms, max 55 ms, connected in 2.4 s, the 10 stuck viewers
dropped and no reader. (Since the hub closes slow clients with
1013 rather than 1008, the tool counts any close the server starts.)

#### The buffering storm

When every viewer reports buffering at the same moment (the start of a
big session does this), each presence change used to broadcast a full
room snapshot to every viewer at once: N changes × N viewers × a
snapshot that itself lists N members. With 1 000 viewers the send
buffers overflowed and the server disconnected 956 viewers and the host;
joining 2 000 viewers took 17 s for the same reason. Presence changes
(joins, leaves, buffering, lag) are now coalesced into at most one
snapshot per 250 ms (`room.Deps.PresenceEvery`); playback, queue and
chat still go out at once. After the change a storm of 1 000 or 2 000
viewers disconnects nobody (p99 23 ms and 30 ms).

## Profile `votes`

```sh
just load -profile votes -viewers 2000 -duration 2m
```

One public room in vote mode with viewers allowed to add, filled with
signed-in viewers (users and sessions are written straight to the
database: registration is rate limited and hashes slowly). The host
queues 20 ready videos (rows without files, as in `crowd`), then for the
duration: every second a random viewer toggles its vote on a random
waiting item, half a second later the host seeks, every 5 s a random
viewer adds a video, and 2 % of the viewers per minute drop their
connection and come back. A vote is timed from sending it to the voter's
own snapshot showing it (`voted`); a reconnect from the close to the new
`welcome`. Only those snapshots are decoded, so the load tool stays
below the server's CPU.

### Results (2026-10-08)

Same machine as above (Apple M1 Max, Docker Desktop VM with 10 CPUs
and 16 GB, dev build of the web server, `1e2205f`), 2 minutes each.

| Viewers | Connect | Seek p50 / p99 / max | Vote p50 / p99 | Reconnect p50 / p99 | Server CPU | Load tool CPU | Sent | `room.state` | Heap |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 300 | 0.8 s | 7 / 28 / 39 ms | 25 / 42 ms | 20 / 93 ms | 0.12 | 0.11 | 12 MB/s | 30 kB | 31 MB |
| 1 000 | 4.6 s | 10 / 25 / 77 ms | 62 / 99 ms | 14 / 82 ms | 0.56 | 0.40 | 101 MB/s | 64 kB | 68 MB |
| 2 000 | 20.2 s | 10 / 163 / 210 ms | 133 / 201 ms | 135 / 208 ms | 1.69 | 1.15 | 485 MB/s | 110 kB | 139 MB |

Seeks delivered: 35 988 of 36 000, 119 960 of 120 000 and 237 961 of
240 000 (viewers away reconnecting miss some); the database saw 18–28
transactions per second throughout. What grows is the snapshot:
anonymous viewers get a `guests` count, signed-in ones the member list,
so each `room.state` carries all 2 000 members (110 kB) and goes to each
viewer about twice a second (votes, adds and coalesced presence),
2.2 snapshots × 2 000 viewers × 110 kB. Connecting 2 000 signed-in
viewers takes 20 s against 2.4 s for anonymous ones, and every join is a
chat line for everyone (2 million `chat.message` in the 2 000 run, at
0.1 kB each a minor part of the bytes).

A first run decoded every message into maps and the load tool, not the
server, became the bottleneck (seek p50 18 s with the server at 1.2
cores); the tool now reports its own CPU so this shows.

## Profile `rooms`

```sh
just load -profile rooms -rooms 20 -viewers 100 -duration 2m
```

Many public rooms of anonymous viewers, one signed-in host each; the
hosts seek in turn so that one seek per room per second spreads over
the second. After the duration every viewer drops its connection at the
same moment and comes back (a network blip or a proxy restart looks
like this), then everyone buffers at once and recovers (as in `-storm`).

### Results (2026-10-08)

Same machine and build, 20 rooms × 100 viewers, 2 minutes:

- Steady: 240 000 / 240 000 seeks delivered, p50 2.7 ms, p95 7.6 ms,
  p99 12 ms, max 61 ms; server CPU 0.13 cores, 104 database
  transactions per second, 0.4 MB/s sent; 4 068 goroutines.
- All 2 000 viewers reconnecting at once: everyone back within 405 ms,
  reconnect to `welcome` p50 245 ms, p99 264 ms; nobody closed by the
  server. The storm that follows costs 0.17 cores and 1.3 MB/s.

Spread over rooms, the same 2 000 viewers cost an order of magnitude
less than in one room: playback messages are small and snapshots of a
100-viewer room are about 1 kB.

## Limits and what to watch

- One web process holds every live room; there is no sharding across
  instances. Around 2 000 anonymous viewers in a room, or 2 000 viewers
  over 20 rooms, is comfortable on this machine.
- Signed-in viewers in one room are the expensive case: every snapshot
  lists every member, so its size and the bytes sent grow with the
  square of the room (485 MB/s at 2 000, p99 seek 163 ms, connect
  20 s). Presence deltas and a capped member list (#104) and encoding
  shared broadcasts once (#103) aim at this.
- A vote holds the room lock over its database writes (the toggle and
  the new ranks), so a playback command sent at the same moment waits
  for them; with the seek sent alongside the vote, a 50-viewer run
  showed p95 105 ms against 16 ms half a second apart.
- A viewer that cannot keep up is dropped rather than slowing anyone
  down: 256 queued messages or a 10 s write stall. The close says 1013
  (try again later), so the browser reconnects and starts from a fresh
  snapshot.
- Clients get at most 20 commands per second (burst 40).
- Coalescing delays presence by up to 250 ms; nothing else is delayed.
