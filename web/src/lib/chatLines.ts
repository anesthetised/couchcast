import type { ChatMessage } from "~/protocol";

// Pure rules of how chat lines are laid out: grouping, folded system runs
// and the small texts around them. Chat renders them.

// Consecutive messages by one author inside this window share a header.
export const GROUP_WINDOW_MS = 60_000;

// continues says whether m shares the header of the line before it.
export function continues(prev: ChatMessage | undefined, m: ChatMessage): boolean {
  return prev !== undefined && !m.system && !prev.system && prev.username === m.username && m.createdMs - prev.createdMs < GROUP_WINDOW_MS;
}

export type Run = { start: number; hidden: number };

// foldRuns maps every foldable line to its run. Three or more system lines
// in a row fold into "N earlier events"; the last line of the run stays
// visible so a fresh event is never hidden.
export function foldRuns(list: ChatMessage[]): Map<number, Run> {
  const info = new Map<number, Run>();
  let i = 0;
  while (i < list.length) {
    if (!list[i]!.system) {
      i++;
      continue;
    }
    let j = i;
    while (j < list.length && list[j]!.system) j++;
    if (j - i >= 3) for (let k = i; k < j - 1; k++) info.set(list[k]!.id, { start: list[i]!.id, hidden: j - i - 1 });
    i = j;
  }
  return info;
}

// lagText says how far a viewer is from the room clock.
export function lagText(ms: number): string {
  const s = (Math.abs(ms) / 1000).toFixed(1).replace(/\.0$/, "");
  return ms > 0 ? `${s} s behind` : `${s} s ahead`;
}

export function typingText(names: string[]): string {
  if (names.length === 1) return `${names[0]} is typing…`;
  if (names.length === 2) return `${names[0]} and ${names[1]} are typing…`;
  return `${names[0]}, ${names[1]} and ${names.length - 2} more are typing…`;
}
