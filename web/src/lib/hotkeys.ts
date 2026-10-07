// The player's keyboard map. playerHotkey only decides which action a key
// means; the Player checks permissions and carries it out.

export type HotkeyAction =
  | "help"
  | "escape"
  | "togglePlay"
  | "back"
  | "forward"
  | "fullscreen"
  | "theater"
  | "bugReport"
  | "mute"
  | "next"
  | "subtitles"
  | "slower"
  | "faster"
  | "prevChapter"
  | "nextChapter";

const keys: Record<string, HotkeyAction> = {
  "?": "help",
  Escape: "escape",
  " ": "togglePlay",
  ArrowLeft: "back",
  ArrowRight: "forward",
  f: "fullscreen",
  F: "fullscreen",
  t: "theater",
  T: "theater",
  m: "mute",
  M: "mute",
  n: "next",
  N: "next",
  c: "subtitles",
  C: "subtitles",
  "<": "slower",
  ",": "slower",
  ">": "faster",
  ".": "faster",
  "[": "prevChapter",
  "]": "nextChapter",
};

// playerHotkey maps a keydown to an action; null while typing (so the chat
// stays usable), with Cmd/Ctrl/Alt held, or for unbound keys.
export function playerHotkey(e: KeyboardEvent): HotkeyAction | null {
  const t = e.target as HTMLElement | null;
  if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable)) return null;
  if (e.metaKey || e.ctrlKey || e.altKey) return null;
  if (e.key === "B" && e.shiftKey) return "bugReport";
  return keys[e.key] ?? null;
}
