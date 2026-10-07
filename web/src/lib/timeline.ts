import type { Chapter, MediaInfo } from "~/protocol";

// Pure rules of the player's timeline: chapters, storyboard previews and
// speed steps. The Player renders them; nothing here touches the DOM.

export const RATES = [0.5, 0.75, 1, 1.25, 1.5, 2];

// chapterAt returns the chapter playing at ms (chapters sorted by start).
export function chapterAt(chapters: Chapter[], ms: number): Chapter | null {
  let found: Chapter | null = null;
  for (const c of chapters) {
    if (c.startMs <= ms) found = c;
    else break;
  }
  return found;
}

// chapterTarget is where [ or ] goes from ms. Backwards more than three
// seconds into a chapter restarts it, like a track on a CD player.
export function chapterTarget(chapters: Chapter[], ms: number, dir: 1 | -1): Chapter | null {
  if (!chapters.length) return null;
  const cur = chapterAt(chapters, ms);
  const idx = cur ? chapters.indexOf(cur) : -1;
  const target = dir < 0 && idx >= 0 && ms - chapters[idx]!.startMs > 3000 ? idx : idx + dir;
  return chapters[Math.max(0, Math.min(chapters.length - 1, target))] ?? null;
}

// nextRate is the speed one step from rate, or null at either end.
export function nextRate(rate: number, dir: 1 | -1): number | null {
  const i = RATES.indexOf(rate);
  const next = RATES[Math.max(0, Math.min(RATES.length - 1, (i < 0 ? RATES.indexOf(1) : i) + dir))];
  return next === undefined || next === rate ? null : next;
}

// previewStyle crops the storyboard cell for ms, as CSS for a box of the
// frame's size; null when the media has no storyboard.
export function previewStyle(m: MediaInfo | undefined, ms: number): Record<string, string> | null {
  const sb = m?.storyboard;
  if (!sb || !m.manifest || !m.token) return null;
  const i = Math.min(sb.count - 1, Math.max(0, Math.floor(ms / sb.intervalMs)));
  const perSheet = sb.cols * sb.rows;
  const cell = i % perSheet;
  const base = m.manifest.slice(0, m.manifest.lastIndexOf("/") + 1);
  return {
    width: `${sb.width}px`,
    height: `${sb.height}px`,
    "background-image": `url("${base}sb-${Math.floor(i / perSheet)}.jpg?t=${m.token}")`,
    "background-position": `-${(cell % sb.cols) * sb.width}px -${Math.floor(cell / sb.cols) * sb.height}px`,
  };
}

// tipLeft centres the seek tip on the pointer (x of a bar w wide) but keeps
// it inside the bar, so a wide preview near either end is not cut off.
export function tipLeft(m: MediaInfo | undefined, x: number, w: number): number {
  const sb = m?.storyboard;
  const half = sb ? sb.width / 2 + 4 : 24;
  return w > 2 * half ? Math.min(Math.max(x, half), w - half) : x;
}
