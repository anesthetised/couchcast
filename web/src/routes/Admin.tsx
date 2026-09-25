import { useSearchParams } from "@solidjs/router";
import { createResource, createSignal, For, Show, type Component } from "solid-js";

import BugsTab from "~/components/BugsTab";
import { admin, type AuditEntry, type BlocklistEntry, type ReportedMedia, type Stats, type Storage, type AdminUser } from "~/lib/admin";
import { formatAgo } from "~/lib/format";
import { toast } from "~/lib/toast";
import type { Room } from "~/lib/types";
import { auth } from "~/store/auth";

type Tab = "stats" | "bugs" | "reports" | "users" | "rooms" | "blocklist" | "storage" | "audit";

const tabs: { id: Tab; label: string }[] = [
  { id: "stats", label: "Stats" },
  { id: "bugs", label: "Bugs" },
  { id: "reports", label: "Reports" },
  { id: "users", label: "Users" },
  { id: "rooms", label: "Rooms" },
  { id: "blocklist", label: "Blocklist" },
  { id: "storage", label: "Storage" },
  { id: "audit", label: "Audit" },
];

// Admin panel. Business statistics come from Postgres; technical metrics
// live in /metrics for Prometheus and are deliberately not duplicated.
const Admin: Component = () => {
  // The open tab lives in the URL (?tab=users) so reloads and links keep it.
  const [params, setParams] = useSearchParams<{ tab?: string }>();
  const tab = (): Tab => (tabs.some((t) => t.id === params.tab) ? (params.tab as Tab) : "stats");
  const setTab = (t: Tab) => setParams({ tab: t === "stats" ? undefined : t });
  const [error, setError] = createSignal<string | null>(null);

  const run = async (fn: () => Promise<unknown>, after?: () => void) => {
    setError(null);
    try {
      await fn();
      after?.();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <Show when={auth.user()?.role === "admin"} fallback={<section class="card error">Administrator role required.</section>}>
      <div class="stack">
        <nav class="tabs">
          <For each={tabs}>
            {(t) => (
              <button type="button" class={`tab ${tab() === t.id ? "active" : ""}`} aria-pressed={tab() === t.id} onClick={() => setTab(t.id)}>
                {t.label}
              </button>
            )}
          </For>
        </nav>
        <Show when={error()}>{(e) => <p class="card error">{e()}</p>}</Show>
        <Show when={tab() === "stats"}>
          <StatsTab />
        </Show>
        <Show when={tab() === "bugs"}>
          <BugsTab />
        </Show>
        <Show when={tab() === "reports"}>
          <ReportsTab run={run} />
        </Show>
        <Show when={tab() === "users"}>
          <UsersTab run={run} />
        </Show>
        <Show when={tab() === "rooms"}>
          <RoomsTab run={run} />
        </Show>
        <Show when={tab() === "blocklist"}>
          <BlocklistTab run={run} />
        </Show>
        <Show when={tab() === "storage"}>
          <StorageTab run={run} />
        </Show>
        <Show when={tab() === "audit"}>
          <AuditTab />
        </Show>
      </div>
    </Show>
  );
};

type Runner = (fn: () => Promise<unknown>, after?: () => void) => Promise<void>;

const fmtBytes = (n: number) => {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
};

const StatsTab: Component = () => {
  const [stats] = createResource<Stats>(admin.stats);
  return (
    <Show when={stats()} fallback={<p class="muted">Loading…</p>}>
      {(s) => (
        <div class="stat-grid">
          <Stat label="Users" value={`${s().users}`} sub={`${s().bannedUsers} banned`} />
          <Stat label="Rooms" value={`${s().rooms}`} sub={`${s().privateRooms} private · ${s().roomsLoaded} live`} />
          <Stat label="Media cache" value={fmtBytes(s().mediaBytes)} sub={Object.entries(s().mediaByStatus).map(([k, v]) => `${v} ${k}`).join(" · ") || "empty"} />
          <Stat label="Ingest jobs" value={`${s().pendingJobs + s().runningJobs}`} sub={`${s().pendingJobs} pending · ${s().runningJobs} running · ${s().failedJobs} failed`} />
          <Stat label="Open reports" value={`${s().openReports}`} />
        </div>
      )}
    </Show>
  );
};

const Stat: Component<{ label: string; value: string; sub?: string }> = (props) => (
  <div class="card stat">
    <div class="muted small">{props.label}</div>
    <div class="stat-value">{props.value}</div>
    <Show when={props.sub}>
      <div class="muted small">{props.sub}</div>
    </Show>
  </div>
);

const ReportsTab: Component<{ run: Runner }> = (props) => {
  const [list, { refetch }] = createResource<ReportedMedia[]>(admin.reports);
  return (
    <section class="card">
      <h2>Reported media</h2>
      <For each={list() ?? []} fallback={<p class="muted">No open reports.</p>}>
        {(rm) => (
          <div class="report">
            <div class="row">
              <div>
                <strong>{rm.media.title || rm.media.sourceUrl}</strong>
                <div class="muted small">
                  {rm.count} report{rm.count === 1 ? "" : "s"} · {rm.media.sourceKey} · {rm.media.status} · {fmtBytes(rm.media.sizeBytes)}
                </div>
                <div class="muted small">
                  <Show when={rm.placements.length > 0} fallback={<>not queued anywhere right now</>}>
                    queued in{" "}
                    <For each={rm.placements}>
                      {(p, i) => (
                        <>
                          {i() > 0 ? ", " : ""}
                          <a href={`/r/${p.roomSlug}`}>{p.roomName}</a>
                          <Show when={p.addedBy}> (added by {p.addedBy})</Show>
                        </>
                      )}
                    </For>
                  </Show>
                </div>
              </div>
              <span class="actions">
                <button type="button" class="link" onClick={() => void props.run(() => admin.dismiss(rm.media.id), () => void refetch())}>
                  Dismiss
                </button>
                <button type="button" class="danger" onClick={() => {
                  const reason = prompt("Reason for removal (added to the blocklist):", "reported");
                  if (reason === null) return; // cancelled
                  void props.run(() => admin.deleteMedia(rm.media.id, reason), () => void refetch());
                }}>
                  Delete & block
                </button>
              </span>
            </div>
            <ul class="list compact">
              <For each={rm.reports}>
                {(r) => (
                  <li class="row small">
                    <span>
                      <strong>{r.reporter}</strong> · {r.reason} <span class="muted">{r.comment}</span>
                      <Show when={r.roomSlug}>
                        <span class="muted">
                          {" "}· in <a href={`/r/${r.roomSlug}`}>{r.roomName || r.roomSlug}</a>
                          <Show when={r.addedBy}>, added by {r.addedBy}</Show>
                        </span>
                      </Show>
                    </span>
                    <span class="muted">{new Date(r.createdAt).toLocaleString()}</span>
                  </li>
                )}
              </For>
            </ul>
          </div>
        )}
      </For>
    </section>
  );
};

const UsersTab: Component<{ run: Runner }> = (props) => {
  const [q, setQ] = createSignal("");
  const [list, { refetch }] = createResource<AdminUser[], string>(q, admin.users);
  return (
    <section class="card">
      <h2>Users</h2>
      <input type="search" placeholder="Search by username" aria-label="Search users" value={q()} onInput={(e) => setQ(e.currentTarget.value)} />
      <ul class="list">
        <For each={list() ?? []}>
          {(u) => (
            <li class="row">
              <span>
                {u.username} <span class="muted small">{u.role} · {u.roomCount} rooms</span>
                <Show when={u.banned}>
                  <span class="badge status-failed"> banned{u.bannedReason ? `: ${u.bannedReason}` : ""}</span>
                </Show>
              </span>
              <Show when={u.role !== "admin"}>
                <Show
                  when={u.banned}
                  fallback={
                    <button type="button" class="link danger-text" onClick={() => {
                      const reason = prompt(`Ban ${u.username}? Reason:`, "");
                      if (reason === null) return; // cancelled
                      void props.run(() => admin.ban(u.id, reason), () => void refetch());
                    }}>
                      Ban
                    </button>
                  }
                >
                  <button type="button" class="link" onClick={() => void props.run(() => admin.unban(u.id), () => void refetch())}>
                    Unban
                  </button>
                </Show>
              </Show>
            </li>
          )}
        </For>
      </ul>
    </section>
  );
};

const RoomsTab: Component<{ run: Runner }> = (props) => {
  const [q, setQ] = createSignal("");
  const [list, { refetch }] = createResource<Room[], string>(q, admin.rooms);
  return (
    <section class="card">
      <h2>Rooms</h2>
      <input type="search" placeholder="Search by slug or name" aria-label="Search rooms" value={q()} onInput={(e) => setQ(e.currentTarget.value)} />
      <ul class="list">
        <For each={list() ?? []}>
          {(r) => (
            <li class="row">
              <span>
                <a href={`/r/${r.slug}`}>{r.name}</a> <span class="muted small">/r/{r.slug} · {r.visibility} · owner {r.owner}</span>
              </span>
              <button type="button" class="link danger-text" onClick={() => {
                if (confirm(`Delete room ${r.slug}?`)) void props.run(() => admin.deleteRoom(r.slug), () => void refetch());
              }}>
                Delete
              </button>
            </li>
          )}
        </For>
      </ul>
    </section>
  );
};

const BlocklistTab: Component<{ run: Runner }> = (props) => {
  const [list, { refetch }] = createResource<BlocklistEntry[]>(admin.blocklist);
  const [key, setKey] = createSignal("");
  const [reason, setReason] = createSignal("");
  return (
    <section class="card">
      <h2>Blocked sources</h2>
      <ul class="list">
        <For each={list() ?? []} fallback={<li class="muted">Nothing blocked.</li>}>
          {(b) => (
            <li class="row">
              <span>
                <code>{b.sourceKey}</code> <span class="muted small">{b.reason} · {b.createdBy}</span>
              </span>
              <button type="button" class="link" onClick={() => void props.run(() => admin.unblock(b.sourceKey), () => void refetch())}>
                Unblock
              </button>
            </li>
          )}
        </For>
      </ul>
      <form class="form" onSubmit={(e) => { e.preventDefault(); void props.run(() => admin.block(key(), reason()), () => { setKey(""); setReason(""); void refetch(); }); }}>
        <label>
          <span>
            Source key <span class="muted">(e.g. youtube:dQw4w9WgXcQ)</span>
          </span>
          <input type="text" required value={key()} onInput={(e) => setKey(e.currentTarget.value)} />
        </label>
        <label>
          Reason
          <input type="text" value={reason()} onInput={(e) => setReason(e.currentTarget.value)} />
        </label>
        <button type="submit" class="danger">Block</button>
      </form>
    </section>
  );
};

// StorageTab: what the packaged media takes, the largest items, and the
// two eviction paths (one item, or everything stale). Eviction only drops
// the package — the source is not blocklisted and can be queued again.
const StorageTab: Component<{ run: Runner }> = (props) => {
  const [storage, { refetch }] = createResource<Storage>(admin.storage);
  const [days, setDays] = createSignal(30);
  const [sweeping, setSweeping] = createSignal(false);
  const used = () => {
    const s = storage();
    return s && s.budgetBytes > 0 ? Math.min(1, s.totalBytes / s.budgetBytes) : 0;
  };
  const evict = (m: { id: string; title: string; sizeBytes: number }) => {
    if (!confirm(`Evict “${m.title || m.id}” (${fmtBytes(m.sizeBytes)})? It will be ingested again if someone queues it.`)) return;
    void props.run(() => admin.evictMedia(m.id), () => void refetch());
  };
  const sweep = async () => {
    if (!confirm(`Evict every unqueued video not watched for ${days()} days?`)) return;
    setSweeping(true);
    await props.run(
      async () => {
        const r = await admin.evictStale(days());
        toast(r.removed ? `Evicted ${r.removed} video${r.removed === 1 ? "" : "s"}, ${fmtBytes(r.bytes)} freed.` : "Nothing that old.");
      },
      () => void refetch(),
    );
    setSweeping(false);
  };
  return (
    <Show when={storage()} fallback={<p class="muted">Loading…</p>}>
      {(s) => (
        <>
          <section class="card storage-summary">
            <div>
              <div class="muted small">Packaged media</div>
              <div class="stat-value">
                {fmtBytes(s().totalBytes)}
                <Show when={s().budgetBytes > 0}>
                  <span class="muted"> / {fmtBytes(s().budgetBytes)}</span>
                </Show>
              </div>
              <Show when={s().budgetBytes > 0}>
                <progress class="storage-bar" max="1" value={used()} />
              </Show>
              <p class="muted small">The janitor evicts the least recently watched, unqueued videos when the budget is exceeded; these controls do it by hand.</p>
            </div>
            <form
              class="storage-sweep"
              onSubmit={(e) => {
                e.preventDefault();
                void sweep();
              }}
            >
              <label>
                Not watched for
                <span class="storage-days">
                  <input type="number" min="1" max="3650" value={days()} onInput={(e) => setDays(Math.max(1, Number(e.currentTarget.value) || 1))} /> days
                </span>
              </label>
              <button type="submit" class="danger" disabled={sweeping()}>
                Evict stale
              </button>
            </form>
          </section>
          <section class="card">
            <h2>Largest videos</h2>
            <ul class="list compact">
              <For each={s().media} fallback={<li class="muted">Nothing packaged.</li>}>
                {(m) => (
                  <li class="row small">
                    <span class="storage-item">
                      <strong>{m.title || m.sourceUrl}</strong>
                      <span class="muted">
                        {fmtBytes(m.sizeBytes)} · watched {formatAgo(new Date(m.lastAccessedAt).getTime())}
                        <Show when={m.queued}> · queued</Show>
                      </span>
                    </span>
                    <button type="button" class="link danger-text" disabled={m.queued} title={m.queued ? "Still in a queue" : "Drop the package"} onClick={() => evict(m)}>
                      Evict
                    </button>
                  </li>
                )}
              </For>
            </ul>
          </section>
        </>
      )}
    </Show>
  );
};

// AuditTab pages backwards through the log; the filters are an action
// prefix ("room." for every room action) and an actor's username.
const AuditTab: Component = () => {
  const [action, setAction] = createSignal("");
  const [actor, setActor] = createSignal("");
  const [entries, setEntries] = createSignal<AuditEntry[]>([]);
  const [nextBefore, setNextBefore] = createSignal(0);
  const [loading, setLoading] = createSignal(false);
  let seq = 0;

  const load = async (before: number) => {
    const mine = ++seq;
    setLoading(true);
    try {
      const page = await admin.audit({ action: action().trim(), actor: actor().trim(), before });
      if (mine !== seq) return;
      setEntries(before ? [...entries(), ...page.entries] : page.entries);
      setNextBefore(page.nextBefore);
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "error");
    } finally {
      if (mine === seq) setLoading(false);
    }
  };
  void load(0);
  let debounce: number | null = null;
  const refilter = () => {
    if (debounce !== null) window.clearTimeout(debounce);
    debounce = window.setTimeout(() => void load(0), 300);
  };

  return (
    <section class="card">
      <h2>Audit log</h2>
      <div class="audit-filters">
        <input type="search" placeholder="Action, e.g. room. or user.ban" value={action()} onInput={(e) => (setAction(e.currentTarget.value), refilter())} aria-label="Action prefix" />
        <input type="search" placeholder="Actor username" value={actor()} onInput={(e) => (setActor(e.currentTarget.value), refilter())} aria-label="Actor" />
      </div>
      <ul class="list compact">
        <For each={entries()} fallback={<li class="muted">{loading() ? "Loading…" : "Nothing matches."}</li>}>
          {(a) => (
            <li class="row small">
              <span>
                <strong>{a.actor || "system"}</strong> {a.action} <span class="muted">{a.targetType} {a.targetId}</span>
                <Show when={Object.keys(a.meta).length > 0}>
                  <code class="muted"> {JSON.stringify(a.meta)}</code>
                </Show>
              </span>
              <span class="muted">{new Date(a.createdAt).toLocaleString()}</span>
            </li>
          )}
        </For>
      </ul>
      <Show when={nextBefore() > 0}>
        <button type="button" class="ghost" disabled={loading()} onClick={() => void load(nextBefore())}>
          Load older
        </button>
      </Show>
    </section>
  );
};

export default Admin;
