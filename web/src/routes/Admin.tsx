import { createResource, createSignal, For, Show, type Component } from "solid-js";

import { admin, type AuditEntry, type BlocklistEntry, type ReportedMedia, type Stats, type AdminUser } from "~/lib/admin";
import type { Room } from "~/lib/types";
import { auth } from "~/store/auth";

type Tab = "stats" | "reports" | "users" | "rooms" | "blocklist" | "audit";

const tabs: { id: Tab; label: string }[] = [
  { id: "stats", label: "Stats" },
  { id: "reports", label: "Reports" },
  { id: "users", label: "Users" },
  { id: "rooms", label: "Rooms" },
  { id: "blocklist", label: "Blocklist" },
  { id: "audit", label: "Audit" },
];

// Admin panel. Business statistics come from Postgres; technical metrics
// live in /metrics for Prometheus and are deliberately not duplicated.
const Admin: Component = () => {
  const [tab, setTab] = createSignal<Tab>("stats");
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
              <button type="button" class={`tab ${tab() === t.id ? "active" : ""}`} onClick={() => setTab(t.id)}>
                {t.label}
              </button>
            )}
          </For>
        </nav>
        <Show when={error()}>{(e) => <p class="card error">{e()}</p>}</Show>
        <Show when={tab() === "stats"}>
          <StatsTab />
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
                  const reason = prompt("Reason for removal (added to the blocklist):", "reported") ?? "";
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
      <input type="text" placeholder="Search by username" value={q()} onInput={(e) => setQ(e.currentTarget.value)} />
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
                      const reason = prompt(`Ban ${u.username}? Reason:`, "") ?? "";
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
      <input type="text" placeholder="Search by slug or name" value={q()} onInput={(e) => setQ(e.currentTarget.value)} />
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
          Source key <span class="muted">(e.g. youtube:dQw4w9WgXcQ)</span>
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

const AuditTab: Component = () => {
  const [list] = createResource<AuditEntry[]>(admin.audit);
  return (
    <section class="card">
      <h2>Audit log</h2>
      <ul class="list compact">
        <For each={list() ?? []} fallback={<li class="muted">Empty.</li>}>
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
    </section>
  );
};

export default Admin;
