import { Show, type Component } from "solid-js";

import type { RoomStore } from "~/store/room";

type Props = { room: RoomStore };

// RoomOptions lets moderators flip playback settings live.
const RoomOptions: Component<Props> = (props) => {
  const settings = () => props.room.state.snapshot?.room.settings;

  return (
    <Show when={props.room.isModerator() && settings()}>
      {(s) => (
        <section class="card options">
          <h2>Options</h2>
          <label class="radio">
            <input type="checkbox" checked={s().voteMode} onChange={(e) => props.room.commands.settings({ voteMode: e.currentTarget.checked })} />
            Vote mode (skip votes, queue ordered by upvotes)
          </label>
          <label class="radio">
            Skip threshold
            <select value={String(s().skipThreshold)} onChange={(e) => props.room.commands.settings({ skipThreshold: Number(e.currentTarget.value) })}>
              <option value="0.25">25% of viewers</option>
              <option value="0.5">50% of viewers</option>
              <option value="0.75">75% of viewers</option>
              <option value="1">everyone</option>
            </select>
          </label>
          <label class="radio">
            <input type="checkbox" checked={s().viewersCanAdd} onChange={(e) => props.room.commands.settings({ viewersCanAdd: e.currentTarget.checked })} />
            Viewers can add videos
          </label>
        </section>
      )}
    </Show>
  );
};

export default RoomOptions;
