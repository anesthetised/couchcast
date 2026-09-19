import { createEffect, createSignal, For, on, Show, type Component } from "solid-js";

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
    <section class="card chat">
      <h2>Chat</h2>
      <ul class="chat-list" ref={list}>
        <For each={messages()} fallback={<li class="muted small">No messages yet.</li>}>
          {(m) => (
            <li class="chat-line">
              <span class="chat-time muted">{time(m.createdMs)}</span>
              <span class="chat-user">{m.username}</span>
              <span class="chat-body">{m.body}</span>
              <Show when={canModerate()}>
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
