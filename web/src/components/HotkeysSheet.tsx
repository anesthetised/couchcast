import { For, type Component } from "solid-js";

type Props = { moderator: boolean; onClose: () => void };

const KEYS: { key: string; what: string; mod?: boolean }[] = [
  { key: "Space", what: "Play / pause", mod: true },
  { key: "← →", what: "Seek 5 seconds", mod: true },
  { key: "N", what: "Next video", mod: true },
  { key: "F", what: "Fullscreen" },
  { key: "M", what: "Mute" },
  { key: "Wheel", what: "Volume (over the video)" },
  { key: "?", what: "This sheet" },
];

// HotkeysSheet lists the player shortcuts; the player owns the key
// handling and only shows this on "?".
const HotkeysSheet: Component<Props> = (props) => (
  <div class="modal-backdrop" onClick={props.onClose}>
    <div class="card modal hotkeys" role="dialog" aria-modal="true" aria-labelledby="hotkeys-title" onClick={(e) => e.stopPropagation()}>
      <h2 id="hotkeys-title">Keyboard shortcuts</h2>
      <dl>
        <For each={KEYS}>
          {(k) => (
            <div class={k.mod && !props.moderator ? "muted" : ""}>
              <dt>
                <kbd>{k.key}</kbd>
              </dt>
              <dd>
                {k.what}
                {k.mod ? <span class="muted small"> · moderators</span> : null}
              </dd>
            </div>
          )}
        </For>
      </dl>
      <div class="actions">
        <button type="button" class="link" onClick={props.onClose}>
          Close
        </button>
      </div>
    </div>
  </div>
);

export default HotkeysSheet;
