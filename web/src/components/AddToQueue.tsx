import { createSignal, Show, type Component } from "solid-js";

import type { RoomStore } from "~/store/room";

type Props = { room: RoomStore };

const AddToQueue: Component<Props> = (props) => {
  const [url, setUrl] = createSignal("");
  const canAdd = () =>
    props.room.isModerator() ||
    (props.room.state.me !== null && (props.room.state.snapshot?.room.settings.viewersCanAdd ?? false));

  const submit = (e: SubmitEvent) => {
    e.preventDefault();
    const u = url().trim();
    if (!u) return;
    props.room.commands.add(u);
    setUrl("");
  };

  return (
    <Show when={canAdd()} fallback={<p class="muted small">{props.room.state.me ? "Only moderators can add videos here." : "Log in to add videos."}</p>}>
      <form class="add-form" onSubmit={submit}>
        <input type="url" placeholder="Paste a YouTube or video link" required value={url()} onInput={(e) => setUrl(e.currentTarget.value)} />
        <button type="submit">Add</button>
      </form>
    </Show>
  );
};

export default AddToQueue;
