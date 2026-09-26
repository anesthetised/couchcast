# AGENTS.md

Guidance for AI coding agents working in this repository.

## Project

couchcast is a watch-together service: users create rooms, queue videos
from YouTube and other sources, and watch them in sync. The backend is Go
(`cmd/couchcast`, `internal/`), the frontend is SolidJS + TypeScript
(`web/`), state lives in PostgreSQL and media files in S3-compatible storage
(SeaweedFS in compose; the web server and the ingest worker create the
bucket on start). One binary provides three commands: `serve` (web
server), `ingest` (download/package worker) and `admin` (CLI).

Video is delivered as DASH (packaged by ffmpeg without transcoding) and
played by Shaka Player; playback synchronisation runs over WebSocket with
the server as the authoritative clock. There is no WebRTC.

## Setup & commands

Everything runs in Docker; only Docker and `just` are needed locally. All
commands run from the repo root via `just` (see `justfile`):

- `just up` — start Postgres and SeaweedFS
- `just migrate` — apply migrations with Atlas (never done on boot)
- `just migrate-diff <name>` — generate a migration from `db/schema.sql`
- `just dev` — run web server (hot reload), ingest worker and Vite dev server
  (http://localhost:5173 proxies `/api`, `/media`, `/healthz` to :8080)
- `just server` / `just ingest` / `just web` — the same, one service at a time
- `just test` — `go test -p 1 -race ./...` in the dev container, like CI
  (the dev image carries gcc for the race detector; integration tests
  share `COUCHCAST_TEST_DATABASE_URL`, hence serial; skipped when unset;
  object storage tests use throwaway buckets on the SeaweedFS named by
  `COUCHCAST_TEST_S3_ENDPOINT`, also skipped when unset)
- `just cover` — Go coverage across packages (`-coverpkg`), as the badge
- `just lint` — golangci-lint; `just check` — TypeScript type check
- `just test-web [args]` — Vitest unit tests (`web/src/**/*.test.ts[x]`,
  jsdom): `lib/`, the room store and components. Tests drive the real
  room store through `test/fakeWebSocket.ts` with `test/fixtures.ts`
  snapshots; `--coverage` writes `web/coverage/`. Pages and flows that
  need the server belong in `e2e/`.
- `just e2e [args]` — Playwright browser tests (`e2e/`) against a fresh
  `couchcast_e2e` database with their own server and Vite instance
  (compose profile `e2e`, `compose.e2e.yaml`); never touches dev data.
  Tests sign up their own users (`support/app.ts`), create rooms through
  the API and use `sql()` for what no UI does (the admin role).
  `readyVideo()` inserts a ready media row without files (enough for queue
  and room behaviour); `PLAYABLE_URL` is one real clip that `e2e/seed`
  (run by the recipe inside e2e-web) renders with ffmpeg, packages and
  uploads once, for tests that need the player to play (`sync.spec.ts`).
  Chromium runs with autoplay allowed; compare player times with
  `expect.poll`, the display ticks every 250 ms. `a11y.spec.ts` runs axe
  (WCAG 2.1 A/AA, serious and critical findings fail) over the main
  pages and the room's dialogs, and checks that nothing scrolls sideways
  at 375 px. Failures
  leave traces and screenshots in `e2e/results/`, the HTML report in
  `e2e/report/`. Use it to verify signed-in flows.
- `just build` — production image; `just prod-up` — run the base compose file;
  `just prod-https` — the same behind Caddy with automatic TLS
  (profile `https`, `deploy/Caddyfile`, needs `COUCHCAST_DOMAIN`; set
  `COUCHCAST_TRUST_PROXY=true` and `WEB_PORT=127.0.0.1:8080` with it)

Run `just test` and `just lint` before opening a PR. CI
(`.github/workflows/ci.yml`) runs gofmt, go vet, golangci-lint, the Go tests
against Postgres, the TypeScript check, the Vitest suite and the build, Atlas migration checks
(validate, lint, apply, and that `db/schema.sql` matches `db/migrations`),
the Playwright suite through `just e2e` (the report is uploaded when it
fails) and a production image build.

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
- PWA: `web/public/manifest.webmanifest`, the icons (rendered by `go run
  ./tools/icons web/public/icons`, never hand-drawn) and `web/public/sw.js`
  (cache-first for `/assets` and `/icons`, network-first navigations with
  the last shell as the offline fallback, nothing else touched) ship with
  the bundle; `lib/install.ts` registers the worker in production builds
  only and shows "Install app" in the top bar when the browser offers
  `beforeinstallprompt`. The Go handler serves `sw.js` and the manifest
  with `no-cache`.
- Permissions are a single function (`internal/access`, phase 3); do not
  scatter role checks across handlers.
- The ingest worker (`couchcast ingest`) talks to the web server only
  through Postgres and S3: `internal/jobs` is the queue (`SKIP LOCKED`
  claims, `LISTEN/NOTIFY` wake-ups, backoff, stale-lock recovery),
  `internal/ingest` runs probe → download → package → upload and publishes
  progress on the `media_progress` channel. `progressReporter` also derives
  `media.speed_bps` / `media.eta_ms` from the recent progress samples
  (download: bytes from the selected formats; packaging: ffmpeg
  `-progress` out_time against the duration), shown in the queue and on
  the preparing overlay.
- `internal/netguard` keeps fetches of user-supplied links out of the
  private network: `IsPublicIP` (loopback, RFC 1918, CGNAT, link-local,
  unique-local, multicast, 0/8, reserved, IPv4-mapped/NAT64/6to4 forms),
  `CheckURL` (resolves the host, also legacy spellings like `127.1`; any
  non-public answer fails) and `Control`/`Transport` (a dialer check on
  the address actually dialled, so redirects and DNS rebinding are caught
  for our own HTTP clients). `ingest.SourcePolicy` applies it: `Service`
  checks links in `EnsureMedia`, `Preview` and `Playlist`
  (`ErrPrivateAddress` / `ErrUnknownHost`, mapped in `room` and `apihttp`
  like `ErrUnsupportedURL`), the worker checks again before probing
  (permanent failure) and fetches thumbnails through the guarded
  transport. `youtube:` keys are minted only for YouTube's own hosts and
  skip the lookup. `COUCHCAST_ALLOW_PRIVATE_SOURCES=true` turns it all off
  for LAN self-hosters. Residual risk: yt-dlp resolves DNS and follows
  redirects itself, so the admission check is not airtight against DNS
  rebinding or a public page that redirects inward; deployments should keep
  the ingest worker off networks it should not reach.
- `internal/source` abstracts extractors; `source/ytdlp` shells out to
  yt-dlp (fixture in `testdata/`). `source.SelectFormats` picks one codec
  family and the best format per ladder height — never transcode.
- Subtitles: the extractor reports uploaded tracks (all, up to ten) or
  automatic captions in the video's own language; the worker fetches them
  in a second yt-dlp run as WebVTT, drops them next to the DASH output as
  `sub-<lang>.vtt` (same media prefix, same token) and records
  `media.subtitles`. The web player attaches them with
  `addTextTrackAsync`; a failure to fetch subtitles never fails the ingest.
  Every track is stored through `internal/webvtt.Normalize`: YouTube's
  roll-up automatic captions (repeated lines, word timestamps, 10 ms
  bridges, `align:start position:0%`) become centred pop-on cues of up to
  two lines. The player keeps native cues (they survive picture-in-picture)
  and styles them with `::cue`.
- The poster is copied at ingest (`fetchThumbnail`, ≤ 8 MB, jpeg/png/webp)
  to `thumb.<ext>` in the media prefix and `media.thumbnail_url` becomes
  `/media/<id>/thumb.<ext>` once uploaded; the media proxy serves
  `thumb.*` without a token and publicly cacheable, and the Open Graph
  injector makes the path absolute. Until then (or if the fetch fails) the
  source URL stays.
- Timeline previews (`internal/storyboard`): after packaging, ffmpeg
  takes one frame per interval (5–60 s, ~300 frames at most) from the
  smallest rendition decoding keyframes only, Go tiles them 10×10 into
  `sb-<n>.jpg` next to the DASH output (same token), and
  `media.storyboard` describes the grid (`MediaInfo.storyboard`). The seek
  tooltip crops the cell for the hovered time. A failure only logs.
- Chapters come from the extractor at probe time (`media.chapters`, at
  least two well-formed entries or none) and ride along in `MediaInfo`;
  the player draws them as ticks on the seek bar, names the current one in
  the controls and offers a list (`[`/`]` step for playback controllers).
- `internal/packager` builds the ffmpeg `-c copy -f dash` command;
  `internal/mediastore` uploads to S3, signs HMAC media tokens and proxies
  `/media/{id}/{file}?t=` with Range support.
- `internal/room` is the live room: one mutex-guarded `Room` per loaded
  room holds the authoritative clock (`positionMs` at `positionAt`, plus a
  `seq`), the queue, presence and votes; every mutation ends in a broadcast.
  `Manager` loads rooms lazily, persists positions every 5 s, unloads idle
  rooms and relays `media_progress` notifications. Finished and skipped
  items are not deleted but marked `played_at` (the room's history, last 20
  in the snapshot as `played`; `queue.replay` re-queues one,
  `queue.clearPlayed` empties it); the `loop` setting re-queues the history
  in play order when the queue runs out (with nobody connected it stops
  paused at the top instead of cycling on); `pauseWhenEmpty` (default on)
  pauses a playing room once nobody has been connected for the rejoin
  grace (`emptyTimer`, armed on leave/kick/load/start, disarmed on join);
  `waitForBuffering` (default on): a viewer buffering for 4 s pauses the
  room (`Snapshot.waiting`, "Waiting for …" overlay, log line); it
  resumes when everyone plays again, or after 30 s without them — then
  that stall is ignored until the viewer plays. A moderator's play or
  pause ends the wait. `Deps.WaitScale` shortens the timers in tests;
  players report their position every 5 s while playing; the room keeps
  each viewer's distance from the clock (`Presence.lagMs`, half-second
  steps, 0 within a second, broadcast only when it changes) and
  moderators see an amber ring and "2.5 s behind" on the avatar;
  `fairQueue` (manual mode) interleaves waiting items by adder, taking
  turns after the current video's adder (`fair.go`, re-applied on every
  add, replay and when switched on); `orderedByRule()` refuses moves and
  shuffles and ignores "play next" while votes or turns decide;
  `play {countdown: true}` — and any play in a room with an announced
  session — sets `Snapshot.countdownMs` 3 s ahead and starts then
  (`countdown`/`countdownTimer`); pause, a new item or a second play
  cancel it; the player shows 3-2-1 from the server clock; `queue.add` with `next` lands
  right after the current item; a video already queued or in the history
  is refused with `duplicate` until the client repeats it with `force`
  (the add form asks). `queue.clear` drops the waiting items (the current
  one keeps playing), `queue.shuffle` reorders them (manual mode). Old
  history is purged hourly. Room events (join/leave,
  add, skip, jump, vote skip) are written to chat as system messages
  (`messages.system`, no author); a leave is logged only after
  `Deps.RejoinGrace` (2 min) without a return, and a rejoin within it is
  silent. `rate.set` (0.5–2×, playback controllers) changes the room's
  speed: the authoritative clock advances at `rate`, it is persisted on
  the room and reset to 1 when the next item starts; the web synchroniser
  nudges relative to it.
- `internal/hub` owns WebSocket connections (`coder/websocket`): decodes
  `internal/protocol` messages, re-resolves the actor's role/ban on every
  mutating command, and fans broadcasts out through a bounded send buffer.
- Frontend sync lives in `web/src/lib`: `clock.ts` (ping/pong offset,
  median of samples), `sync.ts` (deadband 50 ms, `playbackRate` nudge up
  to 1 s, seek beyond), `player.ts` (Shaka + token request filter).
  `store/room.ts` wraps the socket in a Solid store; server errors surface
  through `lib/toast.ts` (`<Toasts>` is mounted once in `App.tsx`). A
  dropped socket shows a reconnecting strip; a `kicked` message, a close
  with reason `room deleted`, or a 404/403 on the REST check before the
  second retry ends the session (`store.ended()`) with a full-stage notice.
  `Player.tsx` owns the hotkeys (Space, ←/→, F, T, M, N, `?` — ignored in inputs),
  persists volume/mute/quality in `localStorage` (`couchcast.*`) and shows
  the sync state as a dot (click for the debug overlay). `chat.typing`
  and `react {emoji}` are ephemeral fan-outs (never stored; reactions are
  rate limited per user); `lib/chatText.ts` also turns timecodes into seek
  buttons for moderators, and video links in recent messages unfurl via
  the probe endpoint (`LinkCard`, one probe per URL per page). When the browser
  rejects `play()` (autoplay policy) `sync.ts` reports it and the player
  shows a tap-to-play gate that resumes inside the gesture.
- Site administration: `couchcast admin grant|revoke <username>` sets the
  role; `/api/v1/admin/*` (behind `auth.RequireAdmin`) serves the `/admin`
  SPA route, whose open tab is `?tab=` (stats when absent). Ban and
  delete ask for a reason; cancelling the prompt cancels the action.
  Deleting media there also blocklists its `source_key` so it
  cannot be re-added, and loaded rooms reload their queues via
  `Manager.MediaDeleted`. A site ban revokes sessions and kicks the user
  from every loaded room.
  `GET /admin/audit` pages backwards (`before=<id>`, `action=` prefix,
  `actor=` username, `room=`); `GET /admin/storage` lists the largest
  ready media with whether a queue still holds them, and
  `POST /admin/media/{id}/evict` / `POST /admin/storage/evict
  {olderThanDays}` drop packages by hand without blocklisting (the
  Storage tab of `/admin`).
- Developer helpers: `couchcast media enqueue <url>`, `media show <id>`,
  `media retry <id>`, `media token <id>` (run via `just sh` or
  `{{compose}} run --rm web go run ./cmd/couchcast media ...`).

## Conventions

- Code, comments, commit messages, issues and docs are in English.
- Conventional Commits (`feat(room): ...`, `fix(ingest): ...`, `chore: ...`),
  referencing the related issue (`#3`). When a commit completes an issue,
  say so in the commit body with `Closes #N`; close the issue with a comment
  `<commit link> — <what was delivered>`.
- Go: standard library first, few dependencies; raw SQL with `pgx`
  (`const q = ...`), consumer-side interfaces, `log/slog`, `testify` in tests.
  Reuse helpers from `github.com/anesthetised/toolkit` before adding deps.
- Frontend bundle: only the directory and the auth pages ship in the entry
  chunk (~65 kB); the other routes are `lazy()` in `App.tsx`, the room
  brings Shaka (~820 kB) with it, cards import `lib/player` on the first
  hover, and on-demand dialogs (emoji, playlist, bug report, hotkeys,
  media report) are lazy components. Keep new heavy code behind a route
  or a `lazy()` import.
- Frontend: plain CSS, no UI framework. `web/src/styles.css` is a small
  design system: tokens in `:root` (surfaces, text, amber accent, statuses,
  radii, shadows, focus ring) and shared components (`button` variants
  `ghost`/`danger`/`link`, `.chip`, `.card`, `.badge`, `.toolbar`, `.hero`,
  `.collapse`). Never hard-code colours in components; add a token.
  Wire types in `web/src/protocol.ts` mirror `internal/protocol`.
- `POST /api/v1/rooms` creates a room and optionally queues a first video
  (validated before creation, enqueued through the live room via
  `apihttp.LiveQueue`), applies initial settings and sends invites; extras
  that fail after creation come back as `warnings`, never as errors. The
  SPA page is `/new`; anonymous visitors round-trip via `/login?next=/new`.
- Rooms carry a `description` (≤ 300 chars, patchable, on cards and in the
  snapshot). A slug change records the old slug in `room_slug_history`;
  `GetRoomBySlug` resolves former slugs too (the SPA replaces the URL with
  the current one), and a new room or a rename back reclaims a slug.
  `rooms.scheduled_at` announces the next session (RFC 3339 on create and
  patch, null clears; must be in the future): a countdown badge in the
  room, "Starts …" on idle cards, the `upcoming` directory filter (soonest
  first) and `GET /me/upcoming` (member rooms starting within 30 min),
  which the auth store polls to fire a reminder ten minutes before. The
  room clears it the first time playback persists as playing. Members leave with `DELETE /rooms/{slug}/members/{me}` (the
  owner first hands over with `POST /rooms/{slug}/owner {username}`, which
  makes the former owner a moderator). `session.end` (moderators) pauses,
  moves the queue to the history and closes every connection with reason
  `session ended`; the web client treats any 1008 close with a reason as
  the end of the session. Reasons: `banned` (room or site ban, the `OnBan`
  and `OnUserBanned` hooks), `removed from room` (`OnRemove`, a member
  taken out of a private room), `room deleted`, `left`, `session ended`.
  The hub's `Close` only signals: its write loop sends what is queued
  (the `kicked` message), then the close frame, so a room never blocks
  on a slow client while holding its lock.
- The SPA handler injects Open Graph tags (`internal/apihttp/meta.go`)
  into index.html for `/r/{slug}` of public rooms — name, description or
  "Playing … · N watching", the current thumbnail — cached 30 s per slug;
  private and unknown rooms get the plain shell. Only the Go server does
  this, so check it with `just build` + `couchcast serve`, not Vite.
- Moderation: `room_mutes` (until a point in time; `PUT/DELETE/GET
  /rooms/{slug}/mutes/{username}`, `CanTarget` like bans) is resolved into
  `access.Actor.MutedUntil` and blocks chat, votes and adds; the
  `slowModeSec` setting spaces non-moderators' messages; `chat.clear`
  soft-deletes every message and broadcasts `chat.cleared`. `chat.delete`
  works on one's own lines for everyone and on any line for moderators.
- `chat.send {body, replyTo}` quotes a visible message of the room
  (`messages.reply_to`, the quote rides in `ChatMessage.replyTo`);
  `chat.pin {id}` / `chat.unpin` (moderators) keep one message above the
  chat (`rooms.pinned_message_id`, `RoomInfo.pinned`, broadcast as
  `chat.pinned`); deleting or clearing unpins.
- `chat.edit {id, body}` lets the author (not moderators) rewrite a line
  for 5 minutes (`editWindow`, checked in SQL against `created_at`); it
  sets `messages.edited_at` (`ChatMessage.editedMs`, "(edited)" in the
  UI), broadcasts `chat.edited {message}` and refreshes a pin of it;
  clients also refresh quotes of it. Mentions are not pushed again. In
  the composer, Up in an empty field edits one's last line.
- Emoji live in `web/src/lib/emoji.ts` (a curated set with shortcodes):
  the composer's ☺ opens `EmojiPicker`, `:smi` autocompletes like `@`
  mentions, and remaining `:name:` codes are expanded on send.
- Invite links (`room_invite_links`, token hash only): moderators mint them
  with `POST /rooms/{slug}/invite-links` (optional expiry and use limit;
  the URL `/join/<token>` is returned once), list and revoke them;
  `GET /api/v1/join/{token}` previews, `POST` joins as a member (existing
  members pass without charging the link).
- Bug reports: `web/src/lib/diagnostics.ts` keeps a ring buffer of recent
  events (player/server/socket errors, error toasts, uncaught errors,
  media status changes; never chat text) and the last sync corrections;
  the room store and the player register probes (`session`, `media`) and
  `collect()` adds the device and bundle version (`__APP_VERSION__`, the
  image build passes `VERSION`). `POST /api/v1/bug-reports` (signed in,
  5/hour) stores it in `bug_reports` with the server's half — version,
  the live room (`room.Manager.Debug`), the media row and its latest
  ingest job — and an optional JPEG frame; `/api/v1/admin/bug-reports`
  lists, shows, serves the frame and resolves. Reports older than 90
  days are purged hourly.
  `BugReportDialog` is mounted once by the room page and opened through
  `lib/bugs.ts` `openBugReport(prefill)` — from the ⚠ control, `Shift+B`,
  the player error overlay and the reconnecting strip (attempt 3+); it
  snapshots diagnostics and the frame when it opens, shows what will be
  sent, and asks anonymous viewers to log in. `lib/focusTrap.ts` is the
  shared modal focus trap.
  The Bugs tab of `/admin` (`components/BugsTab.tsx`) lists open or
  resolved reports with a device summary and expands one into device /
  session / media / server facts, a timeline of events and sync
  corrections relative to the report, the frame, raw JSON, Copy JSON and
  Resolve with a note.
- `GET /api/v1/users?q=` (signed in) is username autocomplete; the
  `UsernamePicker` component wraps it wherever usernames are typed.
- Web Push (optional, `COUCHCAST_VAPID_*`, `couchcast vapid` prints a
  pair): `internal/webpush` is RFC 8291/8292 on the standard library
  (tested against the RFC's worked example) and only posts to known push
  services (`AllowedEndpoint`); `internal/notify` queues deliveries in the
  background and drops subscriptions the service reports gone. Pushes go
  out for invites, mentions (users not in the room, once a minute per
  person, private rooms only to members), "your video is starting" (the
  adder is not in the room) and scheduled sessions ten minutes ahead
  (`rooms.reminded_for`, claimed once per start). Tags match the page's
  in-tab notifications. The bell subscribes the browser
  (`lib/push.ts`, `/api/v1/push/*`); the service worker registers in
  development too.
- Browser notifications (`lib/notify.ts`) are opt-in from the bell in the
  top bar and fire only while the tab is hidden: new invites (the auth
  store polls `/invites` every minute) and the caller's own video starting.
  `lib/title.ts` keeps the tab title (`(unread) ▶ Title — Room`). Below
  640 px the room page keeps the stage sticky and shows Chat / Queue as
  tabs (`?tab=queue`, swipe switches); the player preloads the next ready
  item in the last five seconds (`Player.preload`) for a gapless switch.
  Theater mode (`T`, `couchcast.theater`, desktop only) widens the stage to
  the page and shows the chat as the same overlay panel fullscreen uses.
- Profile: `PATCH /api/v1/me` (avatar colour, a palette key from
  `entity.AvatarColors`) and `POST /api/v1/me/password` (verifies the
  current one, revokes every session, reissues the caller's). The SPA page
  is `/me`; the colour travels with presence and chat lines, and the web
  app derives a stable colour from the name for users who never chose.
- Sessions in the profile: `sessions.id` is the public handle (the token
  hash never leaves the server) and `sessions.user_agent` is stored on
  login (512 bytes max; older sessions show "Unknown browser").
  `GET /api/v1/me/sessions` marks the caller's own one `current`;
  `DELETE /api/v1/me/sessions/{id}` signs another one out (the current
  one is refused — that is logout) and `DELETE /api/v1/me/sessions` all
  but the current. `web/src/lib/ua.ts` shortens the user agent.
- Playlist import: the extractor recognises playlist links
  (`PlaylistURL`, YouTube `list=`; mixes are not offered) and lists them
  flat (`yt-dlp --flat-playlist`, first 50, private/deleted entries
  dropped). The probe marks such links with `playlistUrl` (a bare
  playlist link is not probed as a video); `GET /api/v1/media/playlist?url=`
  feeds `PlaylistPicker`, and `queue.addMany {urls, next}` queues the
  picked entries in order in one transaction — duplicates, unsupported
  links and anything past the queue limit are skipped and counted in the
  room log line; the add budget is charged once.
- `GET /api/v1/media/probe?url=` (signed in, rate limited) previews a link
  for the add form: known sources answer from the database, new ones go
  through `ingest.Service.Preview` → the extractor, bounded by a timeout.
- `GET /api/v1/rooms` is the directory (public rooms plus the caller's
  private rooms; `live`/`private`/`mine` filters, search, pagination,
  `sort=active|viewers|newest|name`, `starred` filter, `lastActiveMs` and
  `starred` per room). Stars are per user (`room_stars`, `PUT/DELETE
  /rooms/{slug}/star`, anyone who may view the room) and come back on the
  room response too.
  `GET /api/v1/me/rooms` is deprecated and unused by the SPA.
- Keep `AGENTS.md` and `README.md` current when commands or layout change.
