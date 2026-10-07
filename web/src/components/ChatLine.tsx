import { For, Show, type Component } from "solid-js";

import LinkCard from "~/components/LinkCard";
import { mentions, parseMessage } from "~/lib/chatText";
import { formatTime } from "~/lib/format";
import { rooms } from "~/lib/rooms";
import { toast } from "~/lib/toast";
import { avatarClass } from "~/lib/types";
import type { ChatMessage } from "~/protocol";
import type { RoomStore } from "~/store/room";

type Props = {
  room: RoomStore;
  m: ChatMessage;
  // Layout and time-dependent facts Chat works out for the line.
  cont: boolean;
  stale: boolean;
  canEdit: boolean;
  // Video links unfurl into cards (recent lines, signed-in viewers).
  cardable: boolean;
  onEdit: (m: ChatMessage) => void;
  onReply: (m: ChatMessage) => void;
  // Scrolls to another line (a quote's original).
  onShow: (id: number) => void;
};

const time = (ms: number) => new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });

// ChatLine renders one message: quote, author, body (mentions, timecodes,
// links and cards), the edited mark and the author's and moderators' tools.
const ChatLine: Component<Props> = (props) => {
  const m = () => props.m;
  const me = () => props.room.state.me;
  const canModerate = () => props.room.isModerator();
  const canAdd = () => canModerate() || (me() !== null && (props.room.state.snapshot?.room.settings.viewersCanAdd ?? false));
  const duration = () => props.room.current()?.media.durationMs ?? 0;
  const isPinned = () => props.room.state.snapshot?.room.pinned?.id === m().id;

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

  return (
    <li
      class="chat-line"
      data-id={m().id}
      classList={{
        stale: props.stale,
        system: m().system ?? false,
        cont: props.cont && !m().replyTo,
        me: !m().system && me() !== null && mentions(m().body, me()!),
        pinned: isPinned(),
      }}
    >
      <Show when={m().replyTo}>
        {(q) => (
          <button type="button" class="chat-quote" onClick={() => props.onShow(q().id)} title="Show the original">
            <span class="chat-quote-user">{q().username}</span>
            <span class="chat-quote-body">{q().body}</span>
          </button>
        )}
      </Show>
      <span class="chat-time muted">{time(m().createdMs)}</span>
      <Show when={!m().system}>
        <span class={`chat-user ${avatarClass(m().username ?? "", m().color)}`}>{m().username}</span>
      </Show>
      <span class="chat-body">
        <For each={parseMessage(m().body)}>
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
            ) : p.video && props.cardable ? (
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
      <Show when={m().editedMs}>
        {(at) => (
          <span class="chat-edited muted" title={`Edited at ${time(at())}`}>
            (edited)
          </span>
        )}
      </Show>
      <Show when={!m().system && me()}>
        <span class="chat-tools">
          <Show when={props.canEdit}>
            <button type="button" class="link" title="Edit" onClick={() => props.onEdit(m())}>
              edit
            </button>
          </Show>
          <Show when={canModerate() || m().username === me()}>
            <button type="button" class="link danger-text" title="Delete" aria-label="Delete" onClick={() => props.room.commands.chatDelete(m().id)}>
              ✕
            </button>
          </Show>
          <Show when={canModerate() && m().username && m().username !== me()}>
            <button type="button" class="link" title={`Mute ${m().username} for 5 minutes`} onClick={() => void muteAuthor(m().username!)}>
              mute
            </button>
          </Show>
          <Show when={canModerate()}>
            <button type="button" class="link" title={isPinned() ? "Unpin" : "Pin above the chat"} onClick={() => (isPinned() ? props.room.commands.chatUnpin() : props.room.commands.chatPin(m().id))}>
              {isPinned() ? "unpin" : "pin"}
            </button>
          </Show>
          <button type="button" class="link" title="Reply" onClick={() => props.onReply(m())}>
            reply
          </button>
        </span>
      </Show>
    </li>
  );
};

export default ChatLine;
