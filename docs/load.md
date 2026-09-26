# Load test

`just load` (`tools/loadtest`) fills one public room on the running dev
stack with anonymous WebSocket viewers that behave like the web app
(ping and report every 5 s, reporting the position a player on the room
clock would have), adds viewers that never read, and has the host seek
every 200 ms. It measures how long each playback change takes to reach
each viewer, whether the server drops the viewers that stopped reading
without touching the others, and the server's heap and goroutines. It
cleans up the user, room and media row it created.

```sh
just load -viewers 2000 -stuck 10        # the usual run
just load -viewers 1000 -stuck 0 -storm  # everyone buffers at once
```

## Results (2026-09-26)

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

### The buffering storm

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

## Limits and what to watch

- One web process holds every live room; there is no sharding across
  instances. Around 2 000 viewers in a room is comfortable on this
  machine; the next costs are CPU for per-viewer JSON encoding and the
  snapshot size, which grows with the member list.
- A viewer that cannot keep up is dropped rather than slowing anyone
  down: 256 queued messages or a 10 s write stall.
- Clients get at most 20 commands per second (burst 40).
- Coalescing delays presence by up to 250 ms; nothing else is delayed.
