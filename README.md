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

## Layout

```
cmd/couchcast/   entrypoint: serve | ingest | admin
internal/        Go packages (config, apihttp, metrics, ...)
web/             SolidJS frontend, embedded into the binary at build time
db/              schema.sql (source of truth) and Atlas migrations
deploy/          files mounted into infrastructure containers
```

## Known limitations

- The web server is a single instance: room state lives in memory.
- Supported browsers in v1: desktop Chrome/Firefox/Edge and Android Chrome.
  Safari/iOS needs an HLS output that is not implemented yet.

## License

MIT
