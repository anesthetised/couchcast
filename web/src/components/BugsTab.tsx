import { createSignal, For, Show, type Component, type JSX } from "solid-js";

import { BUG_CATEGORIES, bugs, type BugReport } from "~/lib/bugs";
import { formatAgo, formatTime } from "~/lib/format";
import { toast } from "~/lib/toast";

// BugsTab is the admin view of problem reports: a list of open or
// resolved reports, each expanding into the collected diagnostics.
const BugsTab: Component = () => {
  const [status, setStatus] = createSignal<"open" | "resolved">("open");
  const [reports, setReports] = createSignal<BugReport[]>([]);
  const [nextBefore, setNextBefore] = createSignal("");
  const [loading, setLoading] = createSignal(false);
  const [openId, setOpenId] = createSignal<string | null>(null);
  let seq = 0;

  const load = async (before = "") => {
    const mine = ++seq;
    setLoading(true);
    try {
      const page = await bugs.list(status(), before || undefined);
      if (mine !== seq) return;
      setReports(before ? [...reports(), ...page.reports] : page.reports);
      setNextBefore(page.nextBefore);
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "error");
    } finally {
      if (mine === seq) setLoading(false);
    }
  };
  void load();

  const switchTo = (s: "open" | "resolved") => {
    setStatus(s);
    setOpenId(null);
    void load();
  };
  const resolved = (id: string) => setReports(reports().filter((r) => r.id !== id));

  return (
    <section class="card">
      <div class="bugs-head">
        <h2>Problem reports</h2>
        <div class="chips" role="group" aria-label="Status">
          <button type="button" class="chip" aria-pressed={status() === "open"} onClick={() => switchTo("open")}>
            Open
          </button>
          <button type="button" class="chip" aria-pressed={status() === "resolved"} onClick={() => switchTo("resolved")}>
            Resolved
          </button>
        </div>
      </div>
      <ul class="list bug-list">
        <For each={reports()} fallback={<li class="muted">{loading() ? "Loading…" : status() === "open" ? "No open reports." : "Nothing resolved yet."}</li>}>
          {(r) => (
            <li class={`bug-item ${openId() === r.id ? "open" : ""}`}>
              <button type="button" class="bug-row" onClick={() => setOpenId(openId() === r.id ? null : r.id)} aria-expanded={openId() === r.id}>
                <span class={`badge bug-${r.category}`}>{categoryLabel(r.category)}</span>
                <span class="bug-row-main">
                  <strong>{firstLine(r.description) || "(no description)"}</strong>
                  <span class="muted small">
                    {[r.author, deviceSummary(r.client), r.roomSlug ? `/r/${r.roomSlug}` : "", r.mediaTitle].filter(Boolean).join(" · ")}
                  </span>
                </span>
                <span class="muted small" title={new Date(r.createdAt).toLocaleString()}>
                  {formatAgo(new Date(r.createdAt).getTime())}
                </span>
              </button>
              <Show when={openId() === r.id}>
                <BugDetail report={r} onResolved={() => resolved(r.id)} />
              </Show>
            </li>
          )}
        </For>
      </ul>
      <Show when={nextBefore()}>
        <button type="button" class="ghost" disabled={loading()} onClick={() => void load(nextBefore())}>
          Load older
        </button>
      </Show>
    </section>
  );
};

const BugDetail: Component<{ report: BugReport; onResolved: () => void }> = (props) => {
  const r = props.report;
  const c = r.client as Client;
  const s = r.server as Server;
  const [note, setNote] = createSignal("");
  const [busy, setBusy] = createSignal(false);

  const resolve = async () => {
    setBusy(true);
    try {
      await bugs.resolve(r.id, note().trim());
      toast("Resolved.");
      props.onResolved();
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "error");
    } finally {
      setBusy(false);
    }
  };
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(JSON.stringify(r, null, 2));
      toast("Copied.");
    } catch {
      toast("Clipboard is not available here.", "error");
    }
  };

  const media = c.media;
  const shaka = media?.shaka;

  return (
    <div class="bug-detail">
      <Show when={r.description}>
        <p class="bug-description">{r.description}</p>
      </Show>
      <Show when={r.hasFrame}>
        <a href={bugs.frameUrl(r.id)} target="_blank" rel="noopener">
          <img class="bug-frame" src={bugs.frameUrl(r.id)} alt="Frame attached to the report" loading="lazy" />
        </a>
      </Show>

      <div class="bug-facts">
        <Facts title="Device">
          <Fact k="Browser" v={browser(c)} />
          <Fact k="Platform" v={c.device?.platform} />
          <Fact k="Screen" v={c.device?.screen && `${c.device.screen.width}×${c.device.screen.height} @${c.device.screen.dpr}x`} />
          <Fact k="Viewport" v={c.device?.viewport && `${c.device.viewport.width}×${c.device.viewport.height}`} />
          <Fact k="Network" v={c.device?.connection && [c.device.connection.type, c.device.connection.downlinkMbps && `${c.device.connection.downlinkMbps} Mb/s`, c.device.connection.rttMs && `${c.device.connection.rttMs} ms`].filter(Boolean).join(" · ")} />
          <Fact k="Codecs" v={c.device?.codecs && Object.entries(c.device.codecs).filter(([, ok]) => ok).map(([n]) => n).join(", ")} />
          <Fact k="App" v={c.app && `${c.app.version} · ${c.app.route}`} />
        </Facts>
        <Facts title="Session">
          <Fact k="Role" v={c.session?.role ?? (c.session ? "guest" : undefined)} />
          <Fact k="Socket" v={c.session?.socket && `${c.session.socket.status}${c.session.socket.attempts ? ` · ${c.session.socket.attempts} retries` : ""}`} />
          <Fact k="Clock" v={c.session?.clock && `offset ${c.session.clock.offsetMs} ms · RTT ${c.session.clock.rttMs} ms`} />
          <Fact k="Viewers" v={c.session?.viewers} />
        </Facts>
        <Facts title="Media">
          <Fact k="Item" v={media?.item && `${media.item.title || media.item.source} · ${media.item.status}`} />
          <Fact k="Quality" v={media?.quality && `${media.quality.active ?? "?"}p${media.quality.chosen ? ` (chosen ${media.quality.chosen}p)` : " (auto)"}`} />
          <Fact k="Drift" v={media && media.driftMs !== null && media.driftMs !== undefined ? `${media.driftMs} ms` : undefined} />
          <Fact k="Buffered" v={media?.element?.buffered?.map(([a, b]) => `${a}–${b}s`).join(", ")} />
          <Fact k="Frames" v={shaka && `${shaka.droppedFrames} dropped of ${shaka.decodedFrames}`} />
          <Fact k="Bandwidth" v={shaka && `${shaka.estimatedBandwidthKbps} kb/s est. · stream ${shaka.streamBandwidthKbps} kb/s`} />
          <Fact k="Subtitles" v={media?.subtitles?.selected} />
          <Fact k="Error" v={media?.overlayError} />
        </Facts>
        <Facts title="Server">
          <Fact k="Version" v={s.version} />
          <Fact k="Room" v={s.room && `${s.room.slug} · ${s.room.loaded ? "loaded" : "not loaded"} · ${s.room.role || "guest"}`} />
          <Fact k="Live" v={s.room?.live && `${s.room.live.viewers} viewers${s.room.live.buffering?.length ? ` · buffering: ${s.room.live.buffering.join(", ")}` : ""} · ${s.room.live.playback?.playing ? "playing" : "paused"} at ${formatTime(livePosition(s.room.live))}`} />
          <Fact k="Media" v={s.media && `${s.media.status}${s.media.error ? ` · ${s.media.error}` : ""} · ${s.media.renditions?.map((x) => x.height).join("/")}p`} />
          <Fact k="Ingest job" v={s.media?.job && `${s.media.job.status} · ${s.media.job.attempts} ${s.media.job.attempts === 1 ? "attempt" : "attempts"}${s.media.job.lastError ? ` · ${s.media.job.lastError}` : ""}`} />
        </Facts>
      </div>

      <Show when={timeline(c).length > 0}>
        <h3 class="bug-sub">Timeline</h3>
        <ol class="bug-timeline">
          <For each={timeline(c)}>
            {(e) => (
              <li class={`kind-${e.kind}`}>
                <span class="muted">{e.rel}</span>
                <span class="bug-kind">{e.kind}</span>
                <span>{e.text}</span>
              </li>
            )}
          </For>
        </ol>
      </Show>

      <details class="bug-preview">
        <summary class="muted small">Raw JSON</summary>
        <pre>{JSON.stringify({ client: r.client, server: r.server }, null, 2)}</pre>
      </details>

      <div class="bug-actions">
        <button type="button" class="ghost" onClick={() => void copy()}>
          Copy JSON
        </button>
        <Show
          when={!r.resolvedAt}
          fallback={
            <span class="muted small">
              Resolved {formatAgo(new Date(r.resolvedAt!).getTime())}
              {r.note ? ` — ${r.note}` : ""}
            </span>
          }
        >
          <input type="text" maxLength={500} placeholder="Note (optional)" value={note()} onInput={(e) => setNote(e.currentTarget.value)} />
          <button type="button" disabled={busy()} onClick={() => void resolve()}>
            Resolve
          </button>
        </Show>
      </div>
    </div>
  );
};

const Facts: Component<{ title: string; children: JSX.Element }> = (props) => (
  <div class="bug-facts-group">
    <h3 class="bug-sub">{props.title}</h3>
    <dl>{props.children}</dl>
  </div>
);

// Fact renders one row, and nothing when the value is missing.
const Fact: Component<{ k: string; v: unknown }> = (props) => (
  <Show when={props.v !== undefined && props.v !== null && props.v !== ""}>
    <dt>{props.k}</dt>
    <dd>{String(props.v)}</dd>
  </Show>
);

// --- shapes of the collected JSON (all optional: the reporter may have
// left the technical details out, and older clients send less) --------------

type Client = {
  collectedAt?: number;
  app?: { version: string; route: string };
  device?: {
    userAgent?: string;
    brands?: string[];
    platform?: string;
    screen?: { width: number; height: number; dpr: number };
    viewport?: { width: number; height: number };
    connection?: { type?: string; downlinkMbps?: number; rttMs?: number };
    codecs?: Record<string, boolean>;
  };
  session?: { role: string | null; socket?: { status: string; attempts: number }; clock?: { offsetMs: number; rttMs: number }; viewers?: number | null };
  media?: {
    item?: { title: string; source: string; status: string } | null;
    quality?: { chosen: number | null; active: number | null };
    driftMs?: number | null;
    element?: { buffered?: [number, number][] };
    subtitles?: { selected: string | null };
    overlayError?: string | null;
    shaka?: { droppedFrames: number; decodedFrames: number; estimatedBandwidthKbps: number; streamBandwidthKbps: number } | null;
  };
  events?: { t: number; kind: string; text: string }[];
  sync?: { t: number; driftMs: number; rate: number; action: string }[];
};

type Server = {
  version?: string;
  room?: {
    slug: string;
    role: string;
    loaded: boolean;
    live?: { viewers: number; buffering?: string[]; serverMs?: number; playback?: { playing: boolean; positionMs: number; atServerMs: number; rate: number } };
  };
  media?: { status: string; error?: string; renditions?: { height: number }[]; job?: { status: string; attempts: number; lastError?: string } };
};

// livePosition is where the room's clock stood when the report arrived:
// the anchor position plus the time played since, at the room's rate.
function livePosition(live: NonNullable<NonNullable<Server["room"]>["live"]>): number {
  const pb = live.playback;
  if (!pb) return 0;
  if (!pb.playing || !live.serverMs) return pb.positionMs;
  return pb.positionMs + (live.serverMs - pb.atServerMs) * (pb.rate || 1);
}

const categoryLabel = (c: string) => BUG_CATEGORIES.find((x) => x.id === c)?.label ?? c;
const firstLine = (s: string) => s.split("\n")[0]!.slice(0, 120);

// browser prefers the structured brands ("Chromium 152"), skipping the
// deliberate "Not A Brand" entry, and falls back to the user agent.
function browser(c: Client): string | undefined {
  const brand = c.device?.brands?.find((b) => !/not.?a.?brand/i.test(b));
  if (brand) return brand;
  const ua = c.device?.userAgent ?? "";
  const m = ua.match(/(Firefox|Edg|OPR|Chrome|Version)\/(\d+)/);
  if (!m) return ua ? ua.slice(0, 60) : undefined;
  const name = m[1] === "Version" ? "Safari" : m[1] === "Edg" ? "Edge" : m[1] === "OPR" ? "Opera" : m[1]!;
  return `${name} ${m[2]}`;
}

export function deviceSummary(client: Record<string, unknown>): string {
  const c = client as Client;
  const screen = c.device?.viewport ? `${c.device.viewport.width}×${c.device.viewport.height}` : "";
  return [browser(c), c.device?.platform, screen].filter(Boolean).join(" · ");
}

// timeline merges recorded events and sync corrections, relative to the
// moment the diagnostics were collected.
function timeline(c: Client): { rel: string; kind: string; text: string }[] {
  const at = c.collectedAt ?? Date.now();
  const items = [
    ...(c.events ?? []).map((e) => ({ t: e.t, kind: e.kind, text: e.text })),
    ...(c.sync ?? []).filter((s) => s.action !== "idle").map((s) => ({ t: s.t, kind: "sync", text: `${s.action} · drift ${s.driftMs} ms · rate ${s.rate}` })),
  ].sort((a, b) => a.t - b.t);
  return items.map((e) => ({ rel: relative(e.t - at), kind: e.kind, text: e.text }));
}

function relative(ms: number): string {
  const s = Math.abs(ms) / 1000;
  const text = s < 60 ? `${s.toFixed(1)} s` : `${Math.floor(s / 60)} min ${Math.round(s % 60)} s`;
  return ms <= 0 ? `−${text}` : `+${text}`;
}

export default BugsTab;
