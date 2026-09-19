import { Show, type Component } from "solid-js";

import type { RoomStore } from "~/store/room";

type Props = { room: RoomStore };

// VotePanel shows the skip vote while vote mode is on.
const VotePanel: Component<Props> = (props) => {
  const snap = () => props.room.state.snapshot;
  const voteMode = () => snap()?.room.settings.voteMode ?? false;

  return (
    <Show when={voteMode() && props.room.current()}>
      <div class="vote-panel">
        <span class="muted small">Vote mode: upvote items in the queue to move them up.</span>
        <Show when={props.room.state.me} fallback={<span class="muted small">Log in to vote.</span>}>
          <button type="button" class={snap()?.skipVoted ? "voted" : ""} onClick={() => props.room.commands.skipVote()}>
            {snap()?.skipVoted ? "Cancel skip vote" : "Vote to skip"} ({snap()?.skipVotes ?? 0}/{snap()?.skipNeeded ?? 0})
          </button>
        </Show>
      </div>
    </Show>
  );
};

export default VotePanel;
