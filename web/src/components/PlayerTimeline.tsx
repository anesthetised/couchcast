import { createSignal, For, Show, type Component } from "solid-js";

import { formatTime } from "~/lib/format";
import { chapterAt, previewStyle, tipLeft } from "~/lib/timeline";
import type { MediaInfo } from "~/protocol";

type Props = {
  media: MediaInfo | undefined;
  nowMs: number;
  canControl: boolean;
  onSeek: (ms: number) => void;
};

// PlayerTimeline is the seek bar: position, chapter ticks and the hover
// tip with the storyboard frame and chapter name.
const PlayerTimeline: Component<Props> = (props) => {
  let seekBar!: HTMLInputElement;
  const [tip, setTip] = createSignal<{ ms: number; x: number; w: number } | null>(null);
  const duration = () => props.media?.durationMs ?? 0;
  const chapters = () => props.media?.chapters ?? [];

  const onHover = (e: MouseEvent) => {
    const d = duration();
    if (!d) return setTip(null);
    const r = seekBar.getBoundingClientRect();
    const frac = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width));
    setTip({ ms: frac * d, x: e.clientX - r.left, w: r.width });
  };

  return (
    <div class="seek-wrap" onMouseMove={onHover} onMouseLeave={() => setTip(null)}>
      <input
        ref={seekBar}
        type="range"
        class="seek"
        min="0"
        max={duration() || 0}
        value={Math.min(props.nowMs, duration() || 0)}
        disabled={!props.canControl || !duration()}
        onChange={(e) => props.canControl && props.onSeek(Number(e.currentTarget.value))}
        aria-label="Position"
      />
      <Show when={duration() > 0 && chapters().length > 0}>
        <div class="chapter-marks" aria-hidden="true">
          <For each={chapters().slice(1)}>{(c) => <span style={{ left: `${(c.startMs / duration()) * 100}%` }} />}</For>
        </div>
      </Show>
      <Show when={tip()}>
        {(t) => (
          <span class="seek-tip" classList={{ "has-preview": previewStyle(props.media, t().ms) !== null }} style={{ left: `${tipLeft(props.media, t().x, t().w)}px` }}>
            <Show when={previewStyle(props.media, t().ms)}>{(style) => <span class="seek-preview" style={style()} />}</Show>
            <Show when={chapterAt(chapters(), t().ms)}>{(c) => <span class="seek-tip-chapter">{c().title}</span>}</Show>
            {formatTime(t().ms)}
          </span>
        )}
      </Show>
    </div>
  );
};

export default PlayerTimeline;
