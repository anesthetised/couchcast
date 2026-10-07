import { createEffect, createSignal, on, onCleanup } from "solid-js";

import type { ChatMessage } from "~/protocol";

const NEAR_BOTTOM_PX = 80;

// createReadingPosition follows where the reader is in a scrolling list of
// messages. While they are away (scrolled up or the tab hidden) new lines
// are counted for the "N new" pill, and the divider marks where they left
// off when the tab comes back. The backlog (first fill) always lands at
// the bottom, even in a background tab; after that only an attentive
// reader follows new lines. Call it while a component is created.
export function createReadingPosition(list: () => HTMLElement, messages: () => ChatMessage[]) {
  const [atBottom, setAtBottom] = createSignal(true);
  const [unseen, setUnseen] = createSignal(0);
  const [divider, setDivider] = createSignal<number | null>(null);
  let lastSeenId: number | null = null;

  const attended = () => atBottom() && document.visibilityState === "visible";
  const scrollToBottom = () => {
    const el = list();
    el.scrollTop = el.scrollHeight;
    setAtBottom(true);
    setUnseen(0);
  };
  const onScroll = () => {
    const el = list();
    const near = el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_BOTTOM_PX;
    setAtBottom(near);
    if (near) setUnseen(0);
  };

  createEffect(
    on(
      () => messages().length,
      (len, prev) => {
        const last = messages()[len - 1];
        if (prev === undefined || prev === 0 || attended()) {
          const el = list();
          el.scrollTop = el.scrollHeight;
          lastSeenId = last?.id ?? lastSeenId;
        } else if (len > prev) {
          setUnseen((n) => n + (len - prev));
        }
      },
    ),
  );

  const onVisible = () => {
    if (document.visibilityState !== "visible") return;
    const all = messages();
    const last = all[all.length - 1];
    if (last && lastSeenId && last.id !== lastSeenId) setDivider(lastSeenId);
    if (atBottom()) {
      const el = list();
      el.scrollTop = el.scrollHeight;
      lastSeenId = last?.id ?? lastSeenId;
      setUnseen(0);
    }
  };
  document.addEventListener("visibilitychange", onVisible);
  onCleanup(() => document.removeEventListener("visibilitychange", onVisible));

  return { atBottom, unseen, divider, clearDivider: () => setDivider(null), scrollToBottom, onScroll };
}
