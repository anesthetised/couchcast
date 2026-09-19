# couchcast

Watch videos together, in sync. Create a room, queue links from YouTube and
other sources, pick your own quality, and let the server keep everyone on
the same frame.

> Status: early development. See the
> [phase issues](https://github.com/anesthetised/couchcast/issues?q=label%3Aphase)
> for the roadmap.

## How it works

- **Ingest** — a separate worker process downloads the selected renditions
  with `yt-dlp`, remuxes them into DASH segments with `ffmpeg -c copy` (no
  transcoding) and uploads the result to S3-compatible storage.
- **Delivery** — the web server proxies segments from S3 behind short-lived
  HMAC tokens; the browser plays them with Shaka Player and can switch
  quality at any time.
- **Sync** — the server holds the authoritative playback clock and pushes it
  over WebSocket; clients estimate their clock offset and nudge
  `playbackRate` to converge, seeking only on large drift.
- **Rooms** — public or private (invite by username), owner and moderators
  control playback, optional vote mode for skipping and reordering, chat,
  per-room bans, site administrators with a reports panel.

No WebRTC: the video is prerecorded, so synchronised DASH playback is
simpler, cheaper and more robust than real-time media transport.

## Running

Requirements: Docker and [`just`](https://github.com/casey/just).

```sh
cp .env.example .env   # set COUCHCAST_MEDIA_TOKEN_SECRET for production
just up                # Postgres + MinIO
just migrate           # apply schema migrations
just dev               # web server, ingest worker, Vite dev server
```

Open http://localhost:5173. The MinIO console is at http://localhost:9001.

Production uses the same compose file without the development override:

```sh
just build && just prod-migrate && just prod-up
```

## Administration

Grant the first administrator from the shell:

```sh
just admin-grant <username>
```

Administrators get an **Admin** link in the header with statistics, media
reports (dismiss, or delete the video and block its source), user bans,
room deletion, the blocklist and the audit log.

## Layout

```
cmd/couchcast/   entrypoint: serve | ingest | admin
internal/        Go packages (config, apihttp, metrics, ...)
web/             SolidJS frontend, embedded into the binary at build time
db/              schema.sql (source of truth) and Atlas migrations
deploy/          files mounted into infrastructure containers
```

## Operations

- **Two processes, one image.** `couchcast serve` (web) and
  `couchcast ingest` (worker) share PostgreSQL and S3 and nothing else, so
  the worker can run on a machine with a residential IP while the web
  server sits on a VPS. Several workers can run at once.
- **Migrations** are applied explicitly (`just migrate` / `just prod-migrate`),
  never on boot. `db/schema.sql` is the source of truth; Atlas generates
  `db/migrations`.
- **Cache budget.** `COUCHCAST_MAX_CACHE_BYTES` caps packaged media in S3;
  every 30 minutes the least recently watched items that no room has
  queued are evicted. Chat older than `COUCHCAST_CHAT_RETENTION_DAYS` is
  purged hourly.
- **Metrics** are exposed at `/metrics` on the web server and on
  `COUCHCAST_INGEST_METRICS_ADDR` on the worker (Prometheus format).
- **YouTube** may require cookies or a PO-token provider on some networks;
  pass extra flags through `COUCHCAST_YTDLP_EXTRA_ARGS`.

## Known limitations

- The web server is a single instance: room state lives in memory. Run
  as many ingest workers as you like, but exactly one `serve`.
- Media segments are protected by short-lived HMAC tokens bound to a media
  id, not to a viewer: anyone who obtains a token can fetch that media
  until it expires (one hour).
- Supported browsers in v1: desktop Chrome/Firefox/Edge and Android Chrome.
  Safari/iOS needs an HLS output that is not implemented yet.
- Playback state is not sharded; a room with hundreds of viewers is fine,
  thousands of rooms with live viewers is not what this is built for.

## License

MIT
