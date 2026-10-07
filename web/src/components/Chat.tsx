import { createEffect, createMemo, createSignal, For, onCleanup, Show, type Component } from "solid-js";

import ChatComposer from "~/components/ChatComposer";
import ChatLine from "~/components/ChatLine";
import { continues, foldRuns, lagText, typingText } from "~/lib/chatLines";
import { toast } from "~/lib/toast";
import type { ChatMessage } from "~/protocol";
import { createReadingPosition } from "~/lib/readingPosition";
import { avatarClass } from "~/lib/types";
import type { RoomStore } from "~/store/room";

type Props = { room: RoomStore };

// Video links in recent messages unfurl into cards (signed-in only: the
// probe endpoint needs a session); older ones stay plain links.
const CARD_WINDOW_MS = 10 * 60_000;
// Messages older than this are marked stale so the fullscreen ghost
// overlay can fade them out.
const STALE_AFTER_MS = 60_000;
// Authors edit their lines this long; the server holds the same window
// and has the last word.
const EDIT_WINDOW_MS = 5 * 60_000;

// Chat shows the backlog plus live messages. Anonymous viewers read only;
// authors edit (for a few minutes) and delete their own lines, moderators
// delete anyone's and pin one. The reading position lives in
// createReadingPosition, layout rules in lib/chatLines, a message row in
// ChatLine and the field in ChatComposer; this component ties them up.
const Chat: Component<Props> = (props) => {
  let list!: HTMLUListElement;
  const messages = () => props.room.state.messages;
  const me = () => props.room.state.me;
  // Nothing can be sent once the session is over for this viewer.
  const canWrite = () => me() !== null && !props.room.ended();
  const canModerate = () => props.room.isModerator();
  const members = () => props.room.state.snapshot?.members ?? [];
  const guests = () => props.room.state.snapshot?.guests ?? 0;

  // The clock ticks coarsely on purpose: it only ages lines.
  const [now, setNow] = createSignal(Date.now());
  const clock = window.setInterval(() => setNow(Date.now()), 5_000);
  onCleanup(() => window.clearInterval(clock));

  const reading = createReadingPosition(() => list, messages);

  // --- folded system runs and grouping ----------------------------------------
  const runs = createMemo(() => foldRuns(messages()));
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
  // Computed per line from its predecessor so <For> keeps DOM nodes stable.
  const prevOf = (i: number): ChatMessage | undefined => messages()[i - 1];
  const afterDivider = (i: number) => {
    const d = reading.divider();
    return d !== null && prevOf(i)?.id === d;
  };

  // --- replies and editing -------------------------------------------------------
  const [replyTo, setReplyTo] = createSignal<ChatMessage | null>(null);
  const [editing, setEditing] = createSignal<ChatMessage | null>(null);
  const canEdit = (m: ChatMessage) => !m.system && me() !== null && m.username === me() && now() - m.createdMs < EDIT_WINDOW_MS;
  const startReply = (m: ChatMessage) => {
    setEditing(null);
    setReplyTo(m);
  };
  const startEdit = (m: ChatMessage) => {
    setReplyTo(null);
    setEditing(m);
  };
  // Up in an empty field edits one's latest line, as in most chat apps.
  const editLast = (): boolean => {
    const all = messages();
    for (let i = all.length - 1; i >= 0; i--) {
      if (canEdit(all[i]!)) {
        startEdit(all[i]!);
        return true;
      }
    }
    return false;
  };
  // A line deleted meanwhile ends the edit.
  createEffect(() => {
    const e = editing();
    if (e && !messages().some((m) => m.id === e.id)) setEditing(null);
  });

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
        <ul class="chat-list" ref={list} onScroll={reading.onScroll} aria-live="polite" aria-relevant="additions">
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
                  <ChatLine
                    room={props.room}
                    m={m}
                    cont={continues(prevOf(i()), m)}
                    stale={now() - m.createdMs > STALE_AFTER_MS}
                    canEdit={canEdit(m)}
                    cardable={me() !== null && now() - m.createdMs < CARD_WINDOW_MS}
                    onEdit={startEdit}
                    onReply={startReply}
                    onShow={scrollToMessage}
                  />
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
        <Show when={reading.unseen() > 0 && !reading.atBottom()}>
          <button type="button" class="chat-new" onClick={reading.scrollToBottom}>
            ↓ {reading.unseen()} new
          </button>
        </Show>
      </div>
      <Show when={canWrite()} fallback={<Show when={!props.room.ended()}><p class="muted small"><a href="/login">Log in</a> to chat.</p></Show>}>
        <ChatComposer
          room={props.room}
          replyTo={replyTo()}
          editing={editing()}
          onCancelReply={() => setReplyTo(null)}
          onCancelEdit={() => setEditing(null)}
          onEditLast={editLast}
          onSent={() => {
            setReplyTo(null);
            reading.clearDivider();
            reading.scrollToBottom();
          }}
        />
      </Show>
    </section>
  );
};

export default Chat;
