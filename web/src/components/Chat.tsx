import { createEffect, createMemo, createSignal, For, on, onCleanup, onMount, Show, type Component } from "solid-js";

import { mentionQuery, mentions, parseMessage } from "~/lib/chatText";
import type { ChatMessage } from "~/protocol";
import type { RoomStore } from "~/store/room";

type Props = { room: RoomStore };

// Consecutive messages by one author inside this window share a header.
const GROUP_WINDOW_MS = 60_000;
const NEAR_BOTTOM_PX = 80;

// Chat shows the backlog plus live messages. Anonymous viewers read only;
// moderators can delete lines.
const Chat: Component<Props> = (props) => {
  let list!: HTMLUListElement;
  let input!: HTMLInputElement;
  const [body, setBody] = createSignal("");
  const messages = () => props.room.state.messages;
  const me = () => props.room.state.me;
  const canWrite = () => me() !== null;
  const canModerate = () => props.room.isModerator();
  const canAdd = () => canModerate() || (me() !== null && (props.room.state.snapshot?.room.settings.viewersCanAdd ?? false));
  const members = () => props.room.state.snapshot?.members ?? [];
  const guests = () => props.room.state.snapshot?.guests ?? 0;

  // Messages older than this are marked stale so the fullscreen ghost
  // overlay can fade them out; the clock ticks coarsely on purpose.
  const STALE_AFTER_MS = 60_000;
  const [now, setNow] = createSignal(Date.now());
  const clock = window.setInterval(() => setNow(Date.now()), 5_000);
  onCleanup(() => window.clearInterval(clock));
  const isStale = (createdMs: number) => now() - createdMs > STALE_AFTER_MS;

  // --- reading position ----------------------------------------------------
  // atBottom follows the scroll; while the reader is away (scrolled up or
  // the tab hidden) new lines are counted for the pill, and the divider
  // marks where they left off when the tab comes back.
  const [atBottom, setAtBottom] = createSignal(true);
  const [unseen, setUnseen] = createSignal(0);
  const [divider, setDivider] = createSignal<number | null>(null);
  let lastSeenId: number | null = null;

  const attended = () => atBottom() && document.visibilityState === "visible";
  const scrollToBottom = () => {
    list.scrollTop = list.scrollHeight;
    setAtBottom(true);
    setUnseen(0);
  };
  const onScroll = () => {
    const near = list.scrollHeight - list.scrollTop - list.clientHeight < NEAR_BOTTOM_PX;
    setAtBottom(near);
    if (near) setUnseen(0);
  };

  createEffect(
    on(
      () => messages().length,
      (len, prev) => {
        const last = messages()[len - 1];
        // The backlog (first fill) always lands at the bottom, even in a
        // background tab; after that only an attentive reader follows.
        if (prev === undefined || prev === 0 || attended()) {
          list.scrollTop = list.scrollHeight;
          lastSeenId = last?.id ?? lastSeenId;
        } else if (len > prev) {
          setUnseen((n) => n + (len - prev));
        }
      },
    ),
  );

  onMount(() => {
    const onVisible = () => {
      if (document.visibilityState !== "visible") return;
      const last = messages()[messages().length - 1];
      if (last && lastSeenId && last.id !== lastSeenId) setDivider(lastSeenId);
      if (atBottom()) {
        list.scrollTop = list.scrollHeight;
        lastSeenId = last?.id ?? lastSeenId;
        setUnseen(0);
      }
    };
    document.addEventListener("visibilitychange", onVisible);
    onCleanup(() => document.removeEventListener("visibilitychange", onVisible));
  });

  // --- grouping --------------------------------------------------------------
  // Computed per line from its predecessor so <For> keeps DOM nodes stable.
  const prevOf = (i: number): ChatMessage | undefined => messages()[i - 1];
  const isCont = (m: ChatMessage, i: number) => {
    const p = prevOf(i);
    return p !== undefined && !m.system && !p.system && p.username === m.username && m.createdMs - p.createdMs < GROUP_WINDOW_MS;
  };
  const afterDivider = (i: number) => {
    const d = divider();
    return d !== null && prevOf(i)?.id === d;
  };

  // --- mentions --------------------------------------------------------------
  const [mention, setMention] = createSignal<{ start: number; prefix: string } | null>(null);
  const [active, setActive] = createSignal(0);
  const candidates = createMemo(() => {
    const q = mention();
    if (!q) return [];
    const p = q.prefix.toLowerCase();
    const mine = me()?.toLowerCase();
    // Present members first, then anyone who spoke in the backlog.
    const names = new Set<string>(members().map((m) => m.username));
    for (const m of messages()) if (m.username) names.add(m.username);
    return [...names].filter((n) => n.toLowerCase() !== mine && n.toLowerCase().startsWith(p)).slice(0, 6);
  });

  const refreshMention = () => {
    setMention(mentionQuery(input.value, input.selectionStart ?? input.value.length));
    setActive(0);
  };

  const pick = (name: string) => {
    const q = mention();
    if (!q) return;
    const text = input.value;
    const after = text.slice(input.selectionStart ?? text.length);
    const next = `${text.slice(0, q.start)}@${name} ${after}`;
    setBody(next);
    setMention(null);
    queueMicrotask(() => {
      const caret = q.start + name.length + 2;
      input.setSelectionRange(caret, caret);
      input.focus();
    });
  };

  const onKey = (e: KeyboardEvent) => {
    const list = candidates();
    if (!list.length) return;
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        setActive((active() + 1) % list.length);
        break;
      case "ArrowUp":
        e.preventDefault();
        setActive((active() - 1 + list.length) % list.length);
        break;
      case "Enter":
      case "Tab":
        e.preventDefault();
        pick(list[active()]!);
        break;
      case "Escape":
        setMention(null);
        break;
    }
  };

  const submit = (e: SubmitEvent) => {
    e.preventDefault();
    const text = body().trim();
    if (!text) return;
    props.room.commands.chat(text);
    setBody("");
    setMention(null);
    setDivider(null);
    scrollToBottom();
  };

  const time = (ms: number) => new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });

  return (
    <section class="chat">
      <header class="chat-head">
        <h2 class="section-title">Chat</h2>
        <div class="presence" title={members().map((m) => m.username).join(", ")}>
          <For each={members().slice(0, 6)}>
            {(m) => (
              <span class={`avatar ${m.buffering ? "buffering" : ""}`} title={`${m.username}${m.role && m.role !== "member" ? ` · ${m.role}` : ""}${m.buffering ? " · buffering" : ""}`}>
                {m.username.slice(0, 1)}
              </span>
            )}
          </For>
          <Show when={members().length > 6 || guests() > 0}>
            <span class="muted small">
              +{Math.max(0, members().length - 6) + guests()}
            </span>
          </Show>
        </div>
      </header>
      <div class="chat-scroll">
        <ul class="chat-list" ref={list} onScroll={onScroll} aria-live="polite" aria-relevant="additions">
          <For each={messages()} fallback={<li class="muted small">No messages yet.</li>}>
            {(m, i) => (
              <>
                <Show when={afterDivider(i())}>
                  <li class="chat-divider" role="separator">
                    <span>new messages</span>
                  </li>
                </Show>
                <li
                  class="chat-line"
                  classList={{
                    stale: isStale(m.createdMs),
                    system: m.system ?? false,
                    cont: isCont(m, i()),
                    me: !m.system && me() !== null && mentions(m.body, me()!),
                  }}
                >
                  <span class="chat-time muted">{time(m.createdMs)}</span>
                  <Show when={!m.system}>
                    <span class="chat-user">{m.username}</span>
                  </Show>
                  <span class="chat-body">
                    <For each={parseMessage(m.body)}>
                      {(p) =>
                        p.kind === "text" ? (
                          p.text
                        ) : p.kind === "mention" ? (
                          <span class="mention">@{p.name}</span>
                        ) : (
                          <>
                            <a href={p.url} target="_blank" rel="noopener noreferrer">
                              {p.url}
                            </a>
                            <Show when={p.video && canAdd()}>
                              <button type="button" class="link chat-queue" title="Add to queue" onClick={() => props.room.commands.add(p.url)}>
                                + queue
                              </button>
                            </Show>
                          </>
                        )
                      }
                    </For>
                  </span>
                  <Show when={canModerate() && !m.system}>
                    <button type="button" class="link danger-text chat-delete" title="Delete" onClick={() => props.room.commands.chatDelete(m.id)}>
                      ✕
                    </button>
                  </Show>
                </li>
              </>
            )}
          </For>
        </ul>
        <Show when={unseen() > 0 && !atBottom()}>
          <button type="button" class="chat-new" onClick={scrollToBottom}>
            ↓ {unseen()} new
          </button>
        </Show>
      </div>
      <Show when={canWrite()} fallback={<p class="muted small"><a href="/login">Log in</a> to chat.</p>}>
        <form class="chat-form" onSubmit={submit}>
          <Show when={candidates().length > 0}>
            <ul class="picker-menu chat-mentions" role="listbox">
              <For each={candidates()}>
                {(name, i) => (
                  <li role="option" aria-selected={i() === active()} classList={{ active: i() === active() }} onMouseDown={(e) => (e.preventDefault(), pick(name))}>
                    {name}
                  </li>
                )}
              </For>
            </ul>
          </Show>
          <input
            ref={input}
            type="text"
            maxLength={2000}
            placeholder="Say something"
            autocomplete="off"
            value={body()}
            onInput={(e) => {
              setBody(e.currentTarget.value);
              refreshMention();
            }}
            onKeyDown={onKey}
            onBlur={() => window.setTimeout(() => setMention(null), 120)}
          />
          <button type="submit">Send</button>
        </form>
      </Show>
    </section>
  );
};

export default Chat;
