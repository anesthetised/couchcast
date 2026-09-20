import { useSearchParams } from "@solidjs/router";
import { createEffect, createResource, createSignal, For, on, onCleanup, Show, type Component } from "solid-js";

import RoomCard from "~/components/RoomCard";
import { rooms } from "~/lib/rooms";
import type { Directory as DirectoryData } from "~/lib/types";
import { auth } from "~/store/auth";

const CARD_MIN_WIDTH = 240; // keep in sync with .room-grid minmax
const GRID_GAP = 16;
const ROWS_PER_PAGE = 5;
const MAX_PER_PAGE = 48;
const REFRESH_MS = 30_000;
const SEARCH_DEBOUNCE_MS = 300;

type Params = { q?: string; live?: string; private?: string; mine?: string; sort?: string; page?: string };
const SORTS = [
  ["active", "Active"],
  ["viewers", "Most watched"],
  ["newest", "Newest"],
  ["name", "Name"],
] as const;

// Directory lists every room the visitor may open: public rooms plus the
// private rooms they belong to. Search, the filter chips and the page live
// in the URL so links are shareable and back works.
const Directory: Component = () => {
  const [params, setParams] = useSearchParams<Params>();

  const q = () => params.q ?? "";
  const flag = (name: "live" | "private" | "mine") => params[name] === "1";
  const page = () => Math.max(1, Number(params.page) || 1);
  const sort = () => (SORTS.some(([k]) => k === params.sort) ? params.sort! : "active");
  const signedIn = () => auth.user() !== null;

  const toggle = (name: "live" | "private" | "mine") =>
    setParams({ ...params, [name]: flag(name) ? undefined : "1", page: undefined });

  // Local input state is debounced into the URL.
  const [input, setInput] = createSignal(q());
  let debounce: number | null = null;
  const onSearchInput = (value: string) => {
    setInput(value);
    if (debounce !== null) window.clearTimeout(debounce);
    debounce = window.setTimeout(() => setParams({ q: value.trim() || undefined, page: undefined }), SEARCH_DEBOUNCE_MS);
  };
  onCleanup(() => {
    if (debounce !== null) window.clearTimeout(debounce);
  });

  // Page size follows the grid: whole rows only, so the last row is never
  // ragged. Columns are derived from the container width the same way the
  // CSS auto-fill does.
  const [columns, setColumns] = createSignal(4);
  const observer = new ResizeObserver((entries) => {
    const width = entries[0]?.contentRect.width ?? 0;
    setColumns(Math.max(1, Math.floor((width + GRID_GAP) / (CARD_MIN_WIDTH + GRID_GAP))));
  });
  onCleanup(() => observer.disconnect());
  const perPage = () => Math.min(MAX_PER_PAGE, columns() * ROWS_PER_PAGE);

  const [receivedAt, setReceivedAt] = createSignal(Date.now());
  const [dir, { refetch }] = createResource(
    () => ({
      q: q(),
      live: flag("live"),
      private: signedIn() && flag("private"),
      mine: signedIn() && flag("mine"),
      sort: sort(),
      page: page(),
      perPage: perPage(),
      // Re-fetch when the session changes: private rooms appear/disappear.
      user: auth.user()?.id ?? null,
    }),
    async (p) => {
      const d = await rooms.directory(p);
      setReceivedAt(Date.now());
      return d;
    },
  );
  const serverOffsetMs = () => (dir()?.serverNowMs ?? receivedAt()) - receivedAt();
  const pages = () => Math.max(1, Math.ceil((dir()?.total ?? 0) / perPage()));

  // Keep the page fresh while it is visible.
  const tick = window.setInterval(() => {
    if (document.visibilityState === "visible") void refetch();
  }, REFRESH_MS);
  onCleanup(() => window.clearInterval(tick));

  // Clamp an out-of-range page from the URL.
  createEffect(
    on(dir, (d: DirectoryData | undefined) => {
      if (d && d.rooms.length === 0 && d.total > 0 && page() > 1) setParams({ page: undefined });
    }),
  );

  const filtered = () => Boolean(q() || flag("live") || flag("private") || flag("mine"));

  return (
    <section class="directory" ref={(el) => observer.observe(el)}>
      <div class="toolbar">
        <div class="search">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
            <circle cx="11" cy="11" r="7" />
            <path d="m20 20-3.5-3.5" />
          </svg>
          <input type="search" placeholder="Search rooms" value={input()} onInput={(e) => onSearchInput(e.currentTarget.value)} aria-label="Search rooms" />
        </div>
        <div class="chips" role="group" aria-label="Filters">
          <button type="button" class="chip" aria-pressed={flag("live")} onClick={() => toggle("live")}>
            <span class="dot" />
            Live
          </button>
          <Show when={signedIn()}>
            <button type="button" class="chip" aria-pressed={flag("private")} onClick={() => toggle("private")}>
              Private
            </button>
            <button type="button" class="chip" aria-pressed={flag("mine")} onClick={() => toggle("mine")}>
              Mine
            </button>
          </Show>
          <select class="sort" value={sort()} onChange={(e) => setParams({ ...params, sort: e.currentTarget.value === "active" ? undefined : e.currentTarget.value, page: undefined })} aria-label="Sort rooms">
            <For each={SORTS}>{([k, label]) => <option value={k}>{label}</option>}</For>
          </select>
        </div>
      </div>

      <Show when={dir()} fallback={<SkeletonGrid count={Math.min(perPage(), columns() * 2)} />}>
        {(d) => (
          <>
            <Show
              when={d().rooms.length > 0}
              fallback={<div class="empty">{filtered() ? "No rooms match these filters." : "No rooms yet — create the first one."}</div>}
            >
              <div class="room-grid">
                <For each={d().rooms}>{(room) => <RoomCard room={room} serverOffsetMs={serverOffsetMs()} />}</For>
              </div>
            </Show>
            <Show when={pages() > 1}>
              <nav class="pager">
                <button type="button" class="ghost" disabled={page() <= 1} onClick={() => setParams({ page: page() - 1 > 1 ? String(page() - 1) : undefined })}>
                  ‹ Prev
                </button>
                <span class="muted small">
                  page {page()} of {pages()} · {d().total} rooms
                </span>
                <button type="button" class="ghost" disabled={page() >= pages()} onClick={() => setParams({ page: String(page() + 1) })}>
                  Next ›
                </button>
              </nav>
            </Show>
          </>
        )}
      </Show>
    </section>
  );
};

// SkeletonGrid holds the grid's shape while the first page loads: flat
// tonal blocks, no shimmer.
const SkeletonGrid: Component<{ count: number }> = (props) => (
  <div class="room-grid" aria-hidden="true">
    <For each={Array.from({ length: props.count })}>
      {() => (
        <div class="room-card skeleton">
          <div class="room-card-media" />
          <div class="room-card-body">
            <div class="skeleton-line" />
            <div class="skeleton-line short" />
          </div>
        </div>
      )}
    </For>
  </div>
);

export default Directory;
