# AGENTS.md

Guidance for AI coding agents working in this repository.

## Project

couchcast is a watch-together service: users create rooms, queue videos
from YouTube and other sources, and watch them in sync. The backend is Go
(`cmd/couchcast`, `internal/`), the frontend is SolidJS + TypeScript
(`web/`), state lives in PostgreSQL and media files in S3-compatible storage
(MinIO in development). One binary provides three commands: `serve` (web
server), `ingest` (download/package worker) and `admin` (CLI).

Video is delivered as DASH (packaged by ffmpeg without transcoding) and
played by Shaka Player; playback synchronisation runs over WebSocket with
the server as the authoritative clock. There is no WebRTC.

## Setup & commands

Everything runs in Docker; only Docker and `just` are needed locally. All
commands run from the repo root via `just` (see `justfile`):

- `just up` — start Postgres and MinIO (creates the bucket)
- `just migrate` — apply migrations with Atlas (never done on boot)
- `just migrate-diff <name>` — generate a migration from `db/schema.sql`
- `just dev` — run web server (hot reload), ingest worker and Vite dev server
  (http://localhost:5173 proxies `/api`, `/media`, `/healthz` to :8080)
- `just server` / `just ingest` / `just web` — the same, one service at a time
- `just test` — `go test -p 1 ./...` in the dev container (integration
  tests share `COUCHCAST_TEST_DATABASE_URL`, hence serial; skipped when unset)
- `just lint` — golangci-lint; `just check` — TypeScript type check
- `just build` — production image; `just prod-up` — run the base compose file

Run `just test` and `just lint` before opening a PR.

## Architecture

- `db/schema.sql` is the source of truth for the database schema. Edit it,
  then run `just migrate-diff <name>`; never edit `db/migrations/*` by hand.
- `internal/config` reads `COUCHCAST_*` environment variables; each command
  validates only the sections it uses.
- `internal/apihttp` owns the chi router: JSON API under `/api/v1`,
  `/healthz`, `/metrics`, `/media/*` and the SPA fallback. Route registration
  lives in `New` so the whole URL space is visible in one place.
- `internal/metrics` owns the Prometheus registry shared by all components.
- `internal/auth` owns password hashing (argon2id), cookie sessions and the
  middleware that puts the user into the request context (`auth.UserFrom`).
  Handlers never read cookies themselves.
- `internal/repository` is plain SQL over pgx; tests there run against
  `COUCHCAST_TEST_DATABASE_URL` and replay `db/migrations` on start.
- `internal/apihttp` handler tests use an in-memory fake store
  (`testing_test.go`) and drive the real router with cookies.
- `web/embed.go` embeds `web/dist` into the binary; in development `dist/`
  holds only a placeholder and Vite serves the app.
- Permissions are a single function (`internal/access`, phase 3); do not
  scatter role checks across handlers.
- The ingest worker (`couchcast ingest`) talks to the web server only
  through Postgres and S3: `internal/jobs` is the queue (`SKIP LOCKED`
  claims, `LISTEN/NOTIFY` wake-ups, backoff, stale-lock recovery),
  `internal/ingest` runs probe → download → package → upload and publishes
  progress on the `media_progress` channel.
- `internal/source` abstracts extractors; `source/ytdlp` shells out to
  yt-dlp (fixture in `testdata/`). `source.SelectFormats` picks one codec
  family and the best format per ladder height — never transcode.
- `internal/packager` builds the ffmpeg `-c copy -f dash` command;
  `internal/mediastore` uploads to S3, signs HMAC media tokens and proxies
  `/media/{id}/{file}?t=` with Range support.
- `internal/room` is the live room: one mutex-guarded `Room` per loaded
  room holds the authoritative clock (`positionMs` at `positionAt`, plus a
  `seq`), the queue, presence and votes; every mutation ends in a broadcast.
  `Manager` loads rooms lazily, persists positions every 5 s, unloads idle
  rooms and relays `media_progress` notifications.
- `internal/hub` owns WebSocket connections (`coder/websocket`): decodes
  `internal/protocol` messages, re-resolves the actor's role/ban on every
  mutating command, and fans broadcasts out through a bounded send buffer.
- Frontend sync lives in `web/src/lib`: `clock.ts` (ping/pong offset,
  median of samples), `sync.ts` (deadband 50 ms, `playbackRate` nudge up
  to 1 s, seek beyond), `player.ts` (Shaka + token request filter).
  `store/room.ts` wraps the socket in a Solid store.
- Developer helpers: `couchcast media enqueue <url>`, `media show <id>`,
  `media retry <id>`, `media token <id>` (run via `just sh` or
  `{{compose}} run --rm web go run ./cmd/couchcast media ...`).

## Conventions

- Code, comments, commit messages, issues and docs are in English.
- Conventional Commits (`feat(room): ...`, `fix(ingest): ...`, `chore: ...`),
  referencing the phase issue (`#3`) when applicable.
- Go: standard library first, few dependencies; raw SQL with `pgx`
  (`const q = ...`), consumer-side interfaces, `log/slog`, `testify` in tests.
  Reuse helpers from `github.com/anesthetised/toolkit` before adding deps.
- Frontend: plain CSS (dark theme in `web/src/styles.css`), no UI framework;
  wire types in `web/src/protocol.ts` mirror `internal/protocol`.
- Keep `AGENTS.md` and `README.md` current when commands or layout change.
