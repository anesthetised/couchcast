import { useSearchParams } from "@solidjs/router";
import { createEffect, createResource, createSignal, For, on, onCleanup, Show, type Component } from "solid-js";

import RoomCard from "~/components/RoomCard";
import { rooms } from "~/lib/rooms";
import type { Directory } from "~/lib/types";

const PER_PAGE = 24;
const REFRESH_MS = 30_000;
const SEARCH_DEBOUNCE_MS = 300;

// PublicRooms is the directory on the home page. Search, the live filter
// and the page live in the URL so links are shareable and back works.
const PublicRooms: Component = () => {
  const [params, setParams] = useSearchParams<{ q?: string; live?: string; page?: string }>();

  const q = () => params.q ?? "";
  const live = () => params.live === "1";
  const page = () => Math.max(1, Number(params.page) || 1);

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

  const [receivedAt, setReceivedAt] = createSignal(Date.now());
  const [dir, { refetch }] = createResource(
    () => ({ q: q(), live: live(), page: page() }),
    async (p) => {
      const d = await rooms.public({ ...p, perPage: PER_PAGE });
      setReceivedAt(Date.now());
      return d;
    },
  );
  const serverOffsetMs = () => (dir()?.serverNowMs ?? receivedAt()) - receivedAt();
  const pages = () => Math.max(1, Math.ceil((dir()?.total ?? 0) / PER_PAGE));

  // Keep the page fresh while it is visible.
  const tick = window.setInterval(() => {
    if (document.visibilityState === "visible") void refetch();
  }, REFRESH_MS);
  onCleanup(() => window.clearInterval(tick));

  // Clamp an out-of-range page from the URL.
  createEffect(
    on(dir, (d: Directory | undefined) => {
      if (d && d.rooms.length === 0 && d.total > 0 && page() > 1) setParams({ page: undefined });
    }),
  );

  return (
    <section class="directory">
      <header class="directory-head">
        <h2>Public rooms</h2>
        <div class="directory-controls">
          <input type="search" placeholder="Search rooms" value={input()} onInput={(e) => onSearchInput(e.currentTarget.value)} />
          <label class="radio">
            <input type="checkbox" checked={live()} onChange={(e) => setParams({ live: e.currentTarget.checked ? "1" : undefined, page: undefined })} />
            Live only
          </label>
        </div>
      </header>

      <Show when={dir()} fallback={<p class="muted">Loading…</p>}>
        {(d) => (
          <>
            <Show when={d().rooms.length > 0} fallback={<p class="muted">{live() || q() ? "No rooms match." : "No public rooms yet."}</p>}>
              <div class="room-grid">
                <For each={d().rooms}>{(room) => <RoomCard room={room} serverOffsetMs={serverOffsetMs()} />}</For>
              </div>
            </Show>
            <Show when={pages() > 1}>
              <nav class="pager">
                <button type="button" class="link" disabled={page() <= 1} onClick={() => setParams({ page: page() - 1 > 1 ? String(page() - 1) : undefined })}>
                  ‹ Prev
                </button>
                <span class="muted small">
                  page {page()} of {pages()} · {d().total} rooms
                </span>
                <button type="button" class="link" disabled={page() >= pages()} onClick={() => setParams({ page: String(page() + 1) })}>
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

export default PublicRooms;
