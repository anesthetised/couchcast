import { createEffect, createMemo, createSignal, For, lazy, on, onCleanup, Show, type Component } from "solid-js";

import { mentionQuery } from "~/lib/chatText";
import { expandShortcodes, rememberEmoji, searchEmoji, shortcodeQuery, type Emoji } from "~/lib/emoji";
import type { ChatMessage } from "~/protocol";
import type { RoomStore } from "~/store/room";

// Opened on demand; loaded the first time.
const EmojiPicker = lazy(() => import("~/components/EmojiPicker"));

// A typing hint at most every few seconds while the field has text.
const TYPING_EVERY_MS = 3_000;

type Props = {
  room: RoomStore;
  // The line being answered or edited; Chat starts both from a message.
  replyTo: ChatMessage | null;
  editing: ChatMessage | null;
  onCancelReply: () => void;
  onCancelEdit: () => void;
  // Up in an empty field: edit one's latest line; false when there is none.
  onEditLast: () => boolean;
  onSent: () => void;
};

// ChatComposer is the message field: @name and :emoji: completion, the
// emoji picker, typing hints, and the reply and edit bars. While editing
// it doubles as the editor: the line's text moves into it and Send saves.
const ChatComposer: Component<Props> = (props) => {
  let input!: HTMLInputElement;
  const [body, setBody] = createSignal("");
  const members = () => props.room.state.snapshot?.members ?? [];
  const me = () => props.room.state.me;

  const focusAt = (pos: number) =>
    queueMicrotask(() => {
      input.setSelectionRange(pos, pos);
      input.focus();
    });

  // The edited line's text moves in; ending the edit empties the field.
  createEffect(
    on(
      () => props.editing,
      (m, prev) => {
        if (m) {
          setBody(m.body);
          focusAt(m.body.length);
        } else if (prev) setBody("");
      },
    ),
  );
  createEffect(
    on(
      () => props.replyTo,
      (m) => m && input.focus(),
      { defer: true },
    ),
  );

  // --- mentions --------------------------------------------------------------
  const [mention, setMention] = createSignal<{ start: number; prefix: string } | null>(null);
  const [active, setActive] = createSignal(0);
  const candidates = createMemo(() => {
    const q = mention();
    if (!q) return [];
    const p = q.prefix.toLowerCase();
    const mine = me()?.toLowerCase();
    // Present members first, then anyone who spoke in the backlog.
    const names = new Set<string>(members().map((m) => m.username));
    for (const m of props.room.state.messages) if (m.username) names.add(m.username);
    return [...names].filter((n) => n.toLowerCase() !== mine && n.toLowerCase().startsWith(p)).slice(0, 6);
  });

  // --- emoji ---------------------------------------------------------------
  // ":smi" at the caret suggests emoji the same way "@" suggests names;
  // the ☺ button opens the full picker. Codes left as text are expanded
  // when the message is sent.
  const [shortcode, setShortcode] = createSignal<{ start: number; prefix: string } | null>(null);
  const emojiCandidates = createMemo<Emoji[]>(() => {
    const q = shortcode();
    return q ? searchEmoji(q.prefix, 8) : [];
  });
  const [showPicker, setShowPicker] = createSignal(false);
  const closeMenus = () => {
    setMention(null);
    setShortcode(null);
  };

  const insertAtCaret = (text: string, replaceFrom?: number) => {
    const value = input.value;
    const caret = input.selectionStart ?? value.length;
    const from = replaceFrom ?? caret;
    setBody(`${value.slice(0, from)}${text}${value.slice(caret)}`);
    focusAt(from + text.length);
  };
  const pickEmoji = (char: string) => {
    rememberEmoji(char);
    insertAtCaret(char + " ", shortcode()?.start);
    setShortcode(null);
    setShowPicker(false);
  };

  const refreshMenus = () => {
    const caret = input.selectionStart ?? input.value.length;
    setMention(mentionQuery(input.value, caret));
    setShortcode(shortcodeQuery(input.value, caret));
    setActive(0);
  };

  const pick = (name: string) => {
    const q = mention();
    if (!q) return;
    const text = input.value;
    const after = text.slice(input.selectionStart ?? text.length);
    setBody(`${text.slice(0, q.start)}@${name} ${after}`);
    setMention(null);
    focusAt(q.start + name.length + 2);
  };

  // Arrow keys, Enter/Tab and Escape steer an open completion menu.
  const onMenuKey = (e: KeyboardEvent) => {
    const names = candidates();
    const emoji = emojiCandidates();
    const n = names.length || emoji.length;
    if (!n) return;
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        setActive((active() + 1) % n);
        break;
      case "ArrowUp":
        e.preventDefault();
        setActive((active() - 1 + n) % n);
        break;
      case "Enter":
      case "Tab":
        e.preventDefault();
        if (names.length) pick(names[active()]!);
        else pickEmoji(emoji[active()]!.char);
        break;
      case "Escape":
        closeMenus();
        break;
    }
  };

  // Menus close a moment after the field loses focus, so a click on an
  // entry still lands.
  let blurTimer: number | undefined;
  onCleanup(() => window.clearTimeout(blurTimer));

  let lastTyping = 0;
  const hintTyping = (value: string) => {
    if (!value.trim()) return;
    const t = Date.now();
    if (t - lastTyping < TYPING_EVERY_MS) return;
    lastTyping = t;
    props.room.commands.typing();
  };

  const submit = (e: SubmitEvent) => {
    e.preventDefault();
    const text = body().trim();
    if (!text) return;
    closeMenus();
    const edited = props.editing;
    if (edited) {
      const next = expandShortcodes(text);
      if (next !== edited.body) props.room.commands.chatEdit(edited.id, next);
      props.onCancelEdit();
      return;
    }
    lastTyping = 0;
    props.room.commands.chat(expandShortcodes(text), props.replyTo?.id);
    setBody("");
    props.onSent();
  };

  return (
    <form class="chat-form" onSubmit={submit}>
      <Show when={props.editing}>
        {(m) => (
          <div class="chat-replying">
            <span class="muted small">Editing</span>
            <span class="chat-quote-body">{m().body}</span>
            <button type="button" class="link small" onClick={() => props.onCancelEdit()} aria-label="Cancel editing">
              ✕
            </button>
          </div>
        )}
      </Show>
      <Show when={props.replyTo}>
        {(r) => (
          <div class="chat-replying">
            <span class="muted small">Replying to</span> <span class="chat-quote-user">{r().username}</span>
            <span class="chat-quote-body">{r().body}</span>
            <button type="button" class="link small" onClick={() => props.onCancelReply()} aria-label="Cancel reply">
              ✕
            </button>
          </div>
        )}
      </Show>
      <Show when={candidates().length > 0}>
        <ul class="picker-menu chat-mentions" role="listbox">
          <For each={candidates()}>
            {(name, i) => (
              <li role="option" aria-selected={i() === active()} classList={{ active: i() === active() }} onMouseDown={(e) => (e.preventDefault(), pick(name))}>
                {name}
              </li>
            )}
          </For>
        </ul>
      </Show>
      <input
        ref={input}
        type="text"
        maxLength={2000}
        placeholder="Say something"
        autocomplete="off"
        value={body()}
        onInput={(e) => {
          setBody(e.currentTarget.value);
          refreshMenus();
          if (!props.editing) hintTyping(e.currentTarget.value);
        }}
        onKeyDown={(e) => {
          const menu = candidates().length > 0 || emojiCandidates().length > 0;
          if (e.key === "Escape" && !menu) {
            if (props.editing) props.onCancelEdit();
            else if (props.replyTo) props.onCancelReply();
          }
          if (e.key === "ArrowUp" && !menu && !body() && props.onEditLast()) e.preventDefault();
          onMenuKey(e);
        }}
        onBlur={() => (blurTimer = window.setTimeout(closeMenus, 120))}
      />
      <Show when={emojiCandidates().length > 0 && !candidates().length}>
        <ul class="picker-menu chat-mentions chat-emoji-menu" role="listbox">
          <For each={emojiCandidates()}>
            {(em, i) => (
              <li role="option" aria-selected={i() === active()} classList={{ active: i() === active() }} onMouseDown={(e) => (e.preventDefault(), pickEmoji(em.char))}>
                <span class="emoji">{em.char}</span> :{em.names[0]}:
              </li>
            )}
          </For>
        </ul>
      </Show>
      <div class="emoji-anchor">
        <button type="button" class={`icon ${showPicker() ? "" : "dim"}`} onClick={() => setShowPicker(!showPicker())} title="Emoji" aria-expanded={showPicker()} aria-label="Emoji">
          ☺
        </button>
      </div>
      <Show when={showPicker()}>
        <EmojiPicker onPick={pickEmoji} onClose={() => setShowPicker(false)} />
      </Show>
      <button type="submit">{props.editing ? "Save" : "Send"}</button>
    </form>
  );
};

export default ChatComposer;
