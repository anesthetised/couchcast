import { createEffect, createSignal, For, on, onCleanup, Show, type Component } from "solid-js";

import type { RoomStore } from "~/store/room";

type Props = { room: RoomStore };

// Chat shows the backlog plus live messages. Anonymous viewers read only;
// moderators can delete lines.
const Chat: Component<Props> = (props) => {
  let list!: HTMLUListElement;
  const [body, setBody] = createSignal("");
  const messages = () => props.room.state.messages;
  const canWrite = () => props.room.state.me !== null;
  const canModerate = () => props.room.isModerator();
  const members = () => props.room.state.snapshot?.members ?? [];
  const guests = () => props.room.state.snapshot?.guests ?? 0;

  // Messages older than this are marked stale so the fullscreen ghost
  // overlay can fade them out; the clock ticks coarsely on purpose.
  const STALE_AFTER_MS = 60_000;
  const [now, setNow] = createSignal(Date.now());
  const clock = window.setInterval(() => setNow(Date.now()), 5_000);
  onCleanup(() => window.clearInterval(clock));
  const isStale = (createdMs: number) => now() - createdMs > STALE_AFTER_MS;

  // Stick to the bottom unless the reader scrolled up.
  createEffect(
    on(
      () => messages().length,
      () => {
        const nearBottom = list.scrollHeight - list.scrollTop - list.clientHeight < 80;
        if (nearBottom) list.scrollTop = list.scrollHeight;
      },
    ),
  );

  const submit = (e: SubmitEvent) => {
    e.preventDefault();
    const text = body().trim();
    if (!text) return;
    props.room.commands.chat(text);
    setBody("");
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
      <ul class="chat-list" ref={list} aria-live="polite" aria-relevant="additions">
        <For each={messages()} fallback={<li class="muted small">No messages yet.</li>}>
          {(m) => (
            <li class={`chat-line ${isStale(m.createdMs) ? "stale" : ""} ${m.system ? "system" : ""}`}>
              <span class="chat-time muted">{time(m.createdMs)}</span>
              <Show when={!m.system}>
                <span class="chat-user">{m.username}</span>
              </Show>
              <span class="chat-body">{m.body}</span>
              <Show when={canModerate() && !m.system}>
                <button type="button" class="link danger-text chat-delete" title="Delete" onClick={() => props.room.commands.chatDelete(m.id)}>
                  ✕
                </button>
              </Show>
            </li>
          )}
        </For>
      </ul>
      <Show when={canWrite()} fallback={<p class="muted small"><a href="/login">Log in</a> to chat.</p>}>
        <form class="chat-form" onSubmit={submit}>
          <input type="text" maxLength={2000} placeholder="Say something" value={body()} onInput={(e) => setBody(e.currentTarget.value)} />
          <button type="submit">Send</button>
        </form>
      </Show>
    </section>
  );
};

export default Chat;
