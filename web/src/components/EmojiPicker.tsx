import { createSignal, For, onCleanup, onMount, Show, type Component } from "solid-js";

import { GROUPS, recentEmoji, searchEmoji, type Emoji } from "~/lib/emoji";

type Props = { onPick: (char: string) => void; onClose: () => void };

// EmojiPicker is the popover behind the ☺ button in the chat composer:
// a search box, recently used, then the curated groups.
const EmojiPicker: Component<Props> = (props) => {
  let root!: HTMLDivElement;
  let search!: HTMLInputElement;
  const [query, setQuery] = createSignal("");
  const results = () => searchEmoji(query(), 48);
  const recent = () => recentEmoji();

  onMount(() => {
    search.focus();
    const onDown = (e: MouseEvent) => {
      if (!root.contains(e.target as Node)) props.onClose();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") props.onClose();
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    onCleanup(() => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    });
  });

  const Grid: Component<{ items: Emoji[] | string[] }> = (p) => (
    <div class="emoji-grid">
      <For each={p.items}>
        {(item) => {
          const char = typeof item === "string" ? item : item.char;
          const name = typeof item === "string" ? "" : item.names[0]!;
          return (
            <button type="button" title={name ? `:${name}:` : undefined} onClick={() => props.onPick(char)}>
              {char}
            </button>
          );
        }}
      </For>
    </div>
  );

  return (
    <div class="emoji-picker" ref={root} role="dialog" aria-label="Emoji">
      <input
        ref={search}
        type="search"
        placeholder="Search"
        value={query()}
        onInput={(e) => setQuery(e.currentTarget.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            const first = results()[0];
            if (first) props.onPick(first.char);
          }
        }}
        aria-label="Search emoji"
      />
      <div class="emoji-scroll">
        <Show
          when={query().trim()}
          fallback={
            <>
              <Show when={recent().length > 0}>
                <h4>Recent</h4>
                <Grid items={recent()} />
              </Show>
              <For each={GROUPS}>
                {(g) => (
                  <>
                    <h4>{g.name}</h4>
                    <Grid items={g.emoji} />
                  </>
                )}
              </For>
            </>
          }
        >
          <Show when={results().length > 0} fallback={<p class="muted small">Nothing matches.</p>}>
            <Grid items={results()} />
          </Show>
        </Show>
      </div>
    </div>
  );
};

export default EmojiPicker;
