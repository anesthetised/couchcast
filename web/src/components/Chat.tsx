import { createEffect, createMemo, createSignal, For, lazy, on, onCleanup, onMount, Show, type Component } from "solid-js";

import LinkCard from "~/components/LinkCard";
import { mentionQuery, mentions, parseMessage } from "~/lib/chatText";
import { expandShortcodes, rememberEmoji, searchEmoji, shortcodeQuery, type Emoji } from "~/lib/emoji";
import { formatTime } from "~/lib/format";
import { rooms } from "~/lib/rooms";
import { toast } from "~/lib/toast";
import type { ChatMessage } from "~/protocol";
import { avatarClass } from "~/lib/types";
import type { RoomStore } from "~/store/room";

// Opened on demand; loaded the first time.
const EmojiPicker = lazy(() => import("~/components/EmojiPicker"));

type Props = { room: RoomStore };

// Consecutive messages by one author inside this window share a header.
const GROUP_WINDOW_MS = 60_000;
const NEAR_BOTTOM_PX = 80;

// Chat shows the backlog plus live messages. Anonymous viewers read only;
// authors delete their own lines, moderators anyone's and pin one.
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
  const duration = () => props.room.current()?.media.durationMs ?? 0;
  // Video links in recent messages unfurl into cards (signed-in only: the
  // probe endpoint needs a session); older ones stay plain links.
  const CARD_WINDOW_MS = 10 * 60_000;
  const cardable = (createdMs: number) => me() !== null && now() - createdMs < CARD_WINDOW_MS;
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

  // --- folded system runs ----------------------------------------------------
  // Three or more system lines in a row fold into "N earlier events"; the
  // last line of the run stays visible so a fresh event is never hidden.
  const runs = createMemo(() => {
    const info = new Map<number, { start: number; hidden: number }>();
    const list = messages();
    let i = 0;
    while (i < list.length) {
      if (!list[i]!.system) {
        i++;
        continue;
      }
      let j = i;
      while (j < list.length && list[j]!.system) j++;
      if (j - i >= 3) for (let k = i; k < j - 1; k++) info.set(list[k]!.id, { start: list[i]!.id, hidden: j - i - 1 });
      i = j;
    }
    return info;
  });
  const [expanded, setExpanded] = createSignal<Set<number>>(new Set());
  const toggleRun = (start: number) => {
    const next = new Set(expanded());
    if (next.has(start)) next.delete(start);
    else next.add(start);
    setExpanded(next);
  };
  const folded = (m: ChatMessage) => {
    const run = runs().get(m.id);
    return run !== undefined && !expanded().has(run.start);
  };
  // foldStart is the run's toggle, rendered before its first line.
  const foldStart = (m: ChatMessage) => {
    const run = runs().get(m.id);
    return run && run.start === m.id ? run : undefined;
  };

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

  // --- emoji ---------------------------------------------------------------
  // ":smi" at the caret suggests emoji the same way "@" suggests names;
  // the ☺ button opens the full picker. Codes left as text are expanded
  // when the message is sent.
  const [shortcode, setShortcode] = createSignal<{ start: number; prefix: string } | null>(null);
  const emojiCandidates = createMemo<Emoji[]>(() => {
    const q = shortcode();
    return q ? searchEmoji(q.prefix, 8) : [];
  });
  const [showPicker, setShowPicker] = createSignal(false);

  const insertAtCaret = (text: string, replaceFrom?: number) => {
    const value = input.value;
    const caret = input.selectionStart ?? value.length;
    const from = replaceFrom ?? caret;
    const next = `${value.slice(0, from)}${text}${value.slice(caret)}`;
    setBody(next);
    queueMicrotask(() => {
      const pos = from + text.length;
      input.setSelectionRange(pos, pos);
      input.focus();
    });
  };
  const pickEmoji = (char: string) => {
    rememberEmoji(char);
    const q = shortcode();
    insertAtCaret(char + " ", q?.start);
    setShortcode(null);
    setShowPicker(false);
  };

  const refreshMention = () => {
    const caret = input.selectionStart ?? input.value.length;
    setMention(mentionQuery(input.value, caret));
    setShortcode(shortcodeQuery(input.value, caret));
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
    const names = candidates();
    const emoji = emojiCandidates();
    const n = names.length || emoji.length;
    if (!n) return;
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        setActive((active() + 1) % n);
        break;
      case "ArrowUp":
        e.preventDefault();
        setActive((active() - 1 + n) % n);
        break;
      case "Enter":
      case "Tab":
        e.preventDefault();
        if (names.length) pick(names[active()]!);
        else pickEmoji(emoji[active()]!.char);
        break;
      case "Escape":
        setMention(null);
        setShortcode(null);
        break;
    }
  };

  // A typing hint at most every few seconds while the field has text.
  const TYPING_EVERY_MS = 3_000;
  let lastTyping = 0;
  const hintTyping = (value: string) => {
    if (!value.trim()) return;
    const t = Date.now();
    if (t - lastTyping < TYPING_EVERY_MS) return;
    lastTyping = t;
    props.room.commands.typing();
  };

  const muteAuthor = async (username: string) => {
    const slug = props.room.state.snapshot?.room.slug;
    if (!slug) return;
    try {
      await rooms.mute(slug, username, 5);
      toast(`Muted ${username} for 5 minutes.`);
    } catch (err) {
      toast(err instanceof Error ? err.message : String(err), "error");
    }
  };

  // --- replies and the pin -----------------------------------------------------
  const [replyTo, setReplyTo] = createSignal<ChatMessage | null>(null);
  const startReply = (m: ChatMessage) => {
    setReplyTo(m);
    input.focus();
  };
  const scrollToMessage = (id: number) => {
    const el = list.querySelector<HTMLElement>(`[data-id="${id}"]`);
    if (!el) return toast("That message is no longer in view.");
    el.scrollIntoView({ block: "center" });
    el.classList.add("flash");
    window.setTimeout(() => el.classList.remove("flash"), 1200);
  };
  const pinned = () => props.room.state.snapshot?.room.pinned ?? null;
  // Hiding the pin is a local choice; a different pin shows again.
  const [hiddenPin, setHiddenPin] = createSignal<number | null>(null);
  const showPin = () => {
    const p = pinned();
    return p !== null && hiddenPin() !== p.id ? p : null;
  };

  const submit = (e: SubmitEvent) => {
    e.preventDefault();
    const text = body().trim();
    if (!text) return;
    lastTyping = 0;
    props.room.commands.chat(expandShortcodes(text), replyTo()?.id);
    setBody("");
    setReplyTo(null);
    setMention(null);
    setShortcode(null);
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
              <span
                class={`avatar ${avatarClass(m.username, m.color)} ${m.buffering ? "buffering" : ""} ${canModerate() && m.lagMs ? "lagging" : ""}`}
                title={`${m.username}${m.role && m.role !== "member" ? ` · ${m.role}` : ""}${m.buffering ? " · buffering" : ""}${canModerate() && m.lagMs ? ` · ${lagText(m.lagMs)}` : ""}`}
              >
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
      <Show when={showPin()}>
        {(p) => (
          <div class="chat-pinned" role="note">
            <button type="button" class="chat-pinned-body" onClick={() => scrollToMessage(p().id)} title="Show in chat">
              <span class="chat-pinned-label">📌 Pinned · {p().username}</span>
              <span class="chat-pinned-text">{p().body}</span>
            </button>
            <Show when={canModerate()}>
              <button type="button" class="link small" onClick={() => props.room.commands.chatUnpin()} title="Unpin for everyone">
                Unpin
              </button>
            </Show>
            <button type="button" class="link small" onClick={() => setHiddenPin(p().id)} title="Hide for me" aria-label="Hide pinned message">
              ✕
            </button>
          </div>
        )}
      </Show>
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
                <Show when={foldStart(m)}>
                  {(run) => (
                    <li class="chat-fold">
                      <button type="button" class="link small" onClick={() => toggleRun(run().start)} aria-expanded={expanded().has(run().start)}>
                        {expanded().has(run().start) ? "▾ hide events" : `▸ ${run().hidden} earlier events`}
                      </button>
                    </li>
                  )}
                </Show>
                <Show when={!folded(m)}>
                  <li
                    class="chat-line"
                    data-id={m.id}
                    classList={{
                      stale: isStale(m.createdMs),
                      system: m.system ?? false,
                      cont: isCont(m, i()) && !m.replyTo,
                      me: !m.system && me() !== null && mentions(m.body, me()!),
                      pinned: pinned()?.id === m.id,
                    }}
                  >
                    <Show when={m.replyTo}>
                      {(q) => (
                        <button type="button" class="chat-quote" onClick={() => scrollToMessage(q().id)} title="Show the original">
                          <span class="chat-quote-user">{q().username}</span>
                          <span class="chat-quote-body">{q().body}</span>
                        </button>
                      )}
                    </Show>
                    <span class="chat-time muted">{time(m.createdMs)}</span>
                    <Show when={!m.system}>
                      <span class={`chat-user ${avatarClass(m.username ?? "", m.color)}`}>{m.username}</span>
                    </Show>
                    <span class="chat-body">
                      <For each={parseMessage(m.body)}>
                        {(p) =>
                          p.kind === "text" ? (
                            p.text
                          ) : p.kind === "mention" ? (
                            <span class="mention">@{p.name}</span>
                          ) : p.kind === "time" ? (
                            <Show when={duration() > 0 && p.ms <= duration()} fallback={p.text}>
                              <Show
                                when={canModerate()}
                                fallback={
                                  <span class="timecode" title="Timecode">
                                    {p.text}
                                  </span>
                                }
                              >
                                <button type="button" class="link timecode" title={`Seek to ${formatTime(p.ms)}`} onClick={() => props.room.commands.seek(p.ms)}>
                                  {p.text}
                                </button>
                              </Show>
                            </Show>
                          ) : p.video && cardable(m.createdMs) ? (
                            <LinkCard url={p.url} canAdd={canAdd()} onAdd={(u) => props.room.commands.add(u)} />
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
                    <Show when={!m.system && me()}>
                      <span class="chat-tools">
                        <Show when={canModerate() || m.username === me()}>
                          <button type="button" class="link danger-text" title="Delete" onClick={() => props.room.commands.chatDelete(m.id)}>
                            ✕
                          </button>
                        </Show>
                        <Show when={canModerate() && m.username && m.username !== me()}>
                          <button type="button" class="link" title={`Mute ${m.username} for 5 minutes`} onClick={() => void muteAuthor(m.username!)}>
                            mute
                          </button>
                        </Show>
                        <Show when={canModerate()}>
                          <button type="button" class="link" title={pinned()?.id === m.id ? "Unpin" : "Pin above the chat"} onClick={() => (pinned()?.id === m.id ? props.room.commands.chatUnpin() : props.room.commands.chatPin(m.id))}>
                            {pinned()?.id === m.id ? "unpin" : "pin"}
                          </button>
                        </Show>
                        <button type="button" class="link" title="Reply" onClick={() => startReply(m)}>
                          reply
                        </button>
                      </span>
                    </Show>
                  </li>
                </Show>
              </>
            )}
          </For>
        </ul>
        <Show when={props.room.typing().length > 0}>
          <p class="chat-typing muted small" aria-live="polite">
            {typingText(props.room.typing())}
          </p>
        </Show>
        <Show when={unseen() > 0 && !atBottom()}>
          <button type="button" class="chat-new" onClick={scrollToBottom}>
            ↓ {unseen()} new
          </button>
        </Show>
      </div>
      <Show when={canWrite()} fallback={<p class="muted small"><a href="/login">Log in</a> to chat.</p>}>
        <form class="chat-form" onSubmit={submit}>
          <Show when={replyTo()}>
            {(r) => (
              <div class="chat-replying">
                <span class="muted small">Replying to</span> <span class="chat-quote-user">{r().username}</span>
                <span class="chat-quote-body">{r().body}</span>
                <button type="button" class="link small" onClick={() => setReplyTo(null)} aria-label="Cancel reply">
                  ✕
                </button>
              </div>
            )}
          </Show>
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
              hintTyping(e.currentTarget.value);
            }}
            onKeyDown={(e) => {
              if (e.key === "Escape" && replyTo() && !candidates().length && !emojiCandidates().length) setReplyTo(null);
              onKey(e);
            }}
            onBlur={() => window.setTimeout(() => (setMention(null), setShortcode(null)), 120)}
          />
          <Show when={emojiCandidates().length > 0 && !candidates().length}>
            <ul class="picker-menu chat-mentions chat-emoji-menu" role="listbox">
              <For each={emojiCandidates()}>
                {(em, i) => (
                  <li role="option" aria-selected={i() === active()} classList={{ active: i() === active() }} onMouseDown={(e) => (e.preventDefault(), pickEmoji(em.char))}>
                    <span class="emoji">{em.char}</span> :{em.names[0]}:
                  </li>
                )}
              </For>
            </ul>
          </Show>
          <div class="emoji-anchor">
            <button type="button" class={`icon ${showPicker() ? "" : "dim"}`} onClick={() => setShowPicker(!showPicker())} title="Emoji" aria-expanded={showPicker()} aria-label="Emoji">
              ☺
            </button>
          </div>
          <Show when={showPicker()}>
            <EmojiPicker onPick={pickEmoji} onClose={() => setShowPicker(false)} />
          </Show>
          <button type="submit">Send</button>
        </form>
      </Show>
    </section>
  );
};

// lagText says how far a viewer is from the room clock.
function lagText(ms: number): string {
  const s = (Math.abs(ms) / 1000).toFixed(1).replace(/\.0$/, "");
  return ms > 0 ? `${s} s behind` : `${s} s ahead`;
}

function typingText(names: string[]): string {
  if (names.length === 1) return `${names[0]} is typing…`;
  if (names.length === 2) return `${names[0]} and ${names[1]} are typing…`;
  return `${names[0]}, ${names[1]} and ${names.length - 2} more are typing…`;
}

export default Chat;
