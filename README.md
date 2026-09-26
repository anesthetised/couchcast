# couchcast

Watch videos together, in sync. Create a room, queue links from YouTube and
other sources, pick your own quality, and let the server keep everyone on
the same frame.

[![CI](https://github.com/anesthetised/couchcast/actions/workflows/ci.yml/badge.svg)](https://github.com/anesthetised/couchcast/actions/workflows/ci.yml)
[![Go coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/anesthetised/couchcast/badges/coverage.json)](https://github.com/anesthetised/couchcast/actions/workflows/ci.yml)

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
just up                # Postgres + SeaweedFS (S3)
just migrate           # apply schema migrations
just dev               # web server, ingest worker, Vite dev server
```

Open http://localhost:5173. The SeaweedFS filer UI (stored
files) is at http://localhost:8888; S3 itself is on :9000 with
`S3_ACCESS_KEY` / `S3_SECRET_KEY` from `.env`.

Tests: `just test` (Go with the race detector, against a test database
and SeaweedFS), `just test-web` (Vitest unit tests for the web app),
`just lint`, `just check` (TypeScript) and `just e2e` (Playwright in the
browser against a separate `couchcast_e2e` database, including an axe
accessibility scan). `just cover` prints Go coverage, as the badge.
`just load` runs a load test against the dev stack; results and limits
are in [docs/load.md](docs/load.md).

Production uses the same compose file without the development override:

```sh
just build && just prod-migrate && just prod-up
```

### Deploying with HTTPS

Phones need HTTPS for notifications and for installing the app, and
cookies are `Secure` by default. The `https` compose profile puts
[Caddy](https://caddyserver.com) in front of the web server and gets the
certificate automatically.

1. Point a DNS name at the server and open ports 80 and 443.
2. In `.env` (start from `.env.example`) set at least:

   ```sh
   COUCHCAST_DOMAIN=watch.example.com
   COUCHCAST_MEDIA_TOKEN_SECRET=...        # openssl rand -hex 32
   COUCHCAST_TRUST_PROXY=true              # Caddy sets X-Forwarded-For
   WEB_PORT=127.0.0.1:8080                 # the web server only via Caddy
   POSTGRES_PASSWORD=... S3_SECRET_KEY=...
   # optional: COUCHCAST_VAPID_* from `docker run --rm <image> vapid`
   ```

3. Build, migrate, start:

   ```sh
   just build && just prod-migrate && just prod-https
   just admin-grant <your-username>   # after registering
   ```

To update: `git pull`, then the same three commands; migrations are never
applied on boot. `just prod-down` stops everything; data lives in the
`pgdata`, `s3data` and `caddydata` volumes.

### Backups

`just backup` writes `backups/<UTC time>/` with a Postgres dump
(`postgres.dump`) and an archive of the object storage volume
(`s3data.tar.gz`); SeaweedFS is paused for the seconds the archive takes,
so playback stalls briefly. `just restore backups/<time>` puts both back
after a confirmation, stopping the web server and the worker meanwhile.

Media is a cache that can be downloaded again; the database is what must
not be lost. Run a backup from cron and copy the directory off the
machine, for example nightly:

```sh
0 4 * * * cd /srv/couchcast && just backup && rsync -a --remove-source-files backups/ backup-host:couchcast/
```

`just backup-check` proves the recipes on a throwaway stack (CI runs it on
every push); a real backup counts once it has been restored somewhere: bring the stack up
under another project name (`COMPOSE_PROJECT_NAME=couchcast-check
just prod-up`), run `just --yes restore <dir>` there, and look around.

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
- **Private addresses.** Links whose host is or resolves to a loopback,
  private, CGNAT, link-local (cloud metadata) or unique-local address are
  refused, and the worker fetches posters only from public addresses. To
  queue files from your own LAN, set `COUCHCAST_ALLOW_PRIVATE_SOURCES=true`
  on both `serve` and `ingest` — only on an instance where everyone who can
  add videos is trusted. yt-dlp resolves names and follows redirects on its
  own, so the check is not airtight: the compose file therefore gives the
  ingest worker networks of its own with only PostgreSQL, S3 and the
  internet; keep it that way on other setups.

A room that is playing stays loaded, keeps advancing through its queue and
is resumed after a server restart. By default it pauses two minutes after
the last viewer leaves ("Pause when everyone leaves" in the room options);
turned off, it plays on for nobody. The directory marks playing rooms as
**live**; the viewer count is shown separately.

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
