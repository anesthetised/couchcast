import { createSignal, For, onCleanup, Show, type Component } from "solid-js";

import { api } from "~/lib/api";

type Props = {
  value: string[];
  onChange: (names: string[]) => void;
  placeholder?: string;
  exclude?: string[]; // never suggest these (e.g. the current user)
};

// UsernamePicker is a chip input with prefix autocomplete against
// GET /api/v1/users. A leading "@" is accepted as in chat mentions. Enter,
// comma or a click adds a chip; Backspace on an empty field removes the
// last one.
// bare strips the "@" people type out of habit from chat mentions.
const bare = (raw: string) => raw.trim().replace(/^@+/, "");

const UsernamePicker: Component<Props> = (props) => {
  const [input, setInput] = createSignal("");
  const [suggestions, setSuggestions] = createSignal<string[]>([]);
  const [active, setActive] = createSignal(-1);
  const [open, setOpen] = createSignal(false);
  let timer: number | null = null;
  let seq = 0;

  const has = (name: string) => props.value.some((v) => v.toLowerCase() === name.toLowerCase());

  const add = (raw: string) => {
    const name = bare(raw.replace(/,$/, ""));
    if (!name || has(name)) {
      setInput("");
      return;
    }
    props.onChange([...props.value, name]);
    setInput("");
    setSuggestions([]);
    setOpen(false);
  };

  const remove = (name: string) => props.onChange(props.value.filter((v) => v !== name));

  const lookup = (raw: string) => {
    if (timer !== null) window.clearTimeout(timer);
    const q = bare(raw);
    if (q.length < 2) {
      setSuggestions([]);
      return;
    }
    const mine = ++seq;
    timer = window.setTimeout(async () => {
      try {
        const names = await api<string[]>(`/api/v1/users?q=${encodeURIComponent(q)}`);
        if (mine !== seq) return;
        const excluded = new Set((props.exclude ?? []).map((n) => n.toLowerCase()));
        setSuggestions(names.filter((n) => !has(n) && !excluded.has(n.toLowerCase())));
        setActive(-1);
        setOpen(true);
      } catch {
        setSuggestions([]);
      }
    }, 200);
  };
  onCleanup(() => {
    if (timer !== null) window.clearTimeout(timer);
  });

  const onKey = (e: KeyboardEvent) => {
    const list = suggestions();
    switch (e.key) {
      case "ArrowDown":
        if (list.length) {
          e.preventDefault();
          setActive((active() + 1) % list.length);
        }
        break;
      case "ArrowUp":
        if (list.length) {
          e.preventDefault();
          setActive((active() - 1 + list.length) % list.length);
        }
        break;
      case "Enter":
      case ",":
        e.preventDefault();
        add(active() >= 0 ? (list[active()] ?? input()) : input());
        break;
      case "Escape":
        setOpen(false);
        break;
      case "Backspace":
        if (input() === "" && props.value.length) remove(props.value[props.value.length - 1]!);
        break;
    }
  };

  return (
    <div class="picker">
      <div class="picker-field" onClick={(e) => (e.currentTarget.querySelector("input") as HTMLInputElement | null)?.focus()}>
        <For each={props.value}>
          {(name) => (
            <span class="chip small">
              {name}
              <button type="button" class="chip-x" aria-label={`Remove ${name}`} onClick={() => remove(name)}>
                ×
              </button>
            </span>
          )}
        </For>
        <input
          type="text"
          value={input()}
          placeholder={props.value.length ? "" : (props.placeholder ?? "username")}
          autocomplete="off"
          onInput={(e) => {
            setInput(e.currentTarget.value);
            lookup(e.currentTarget.value);
          }}
          onKeyDown={onKey}
          onFocus={() => suggestions().length && setOpen(true)}
          onBlur={() => window.setTimeout(() => setOpen(false), 120)}
        />
      </div>
      <Show when={open() && suggestions().length > 0}>
        <ul class="picker-menu" role="listbox">
          <For each={suggestions()}>
            {(name, i) => (
              <li role="option" aria-selected={i() === active()} classList={{ active: i() === active() }} onMouseDown={() => add(name)}>
                {name}
              </li>
            )}
          </For>
        </ul>
      </Show>
    </div>
  );
};

export default UsernamePicker;
