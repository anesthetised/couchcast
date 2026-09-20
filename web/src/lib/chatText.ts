// Chat text helpers: split a message into plain text, links and mentions.

export type Part =
  | { kind: "text"; text: string }
  | { kind: "link"; url: string; video: boolean }
  | { kind: "mention"; name: string }
  | { kind: "time"; text: string; ms: number };

// Links, @mentions and timecodes (m:ss or h:mm:ss) as whole words.
const TOKEN = /(https?:\/\/[^\s<>"']+)|(^|[^\w@])@([A-Za-z0-9_]{3,32})|(^|[^\w:])((?:\d{1,2}:)?\d{1,2}:\d{2})(?![\w:])/g;
const VIDEO_HOSTS = /(^|\.)(youtube\.com|youtu\.be|vimeo\.com|twitch\.tv|dailymotion\.com)$/i;
const VIDEO_EXT = /\.(mp4|webm|mkv|mov|m3u8|mpd)(\?|$)/i;

// looksLikeVideo is a heuristic for offering "add to queue" on a link; the
// ingest worker is the judge of what it can actually play.
export function looksLikeVideo(url: string): boolean {
  try {
    const u = new URL(url);
    return VIDEO_HOSTS.test(u.hostname) || VIDEO_EXT.test(u.pathname);
  } catch {
    return false;
  }
}

export function parseMessage(body: string): Part[] {
  const parts: Part[] = [];
  let last = 0;
  for (const m of body.matchAll(TOKEN)) {
    const start = m.index ?? 0;
    if (m[1]) {
      // Trailing punctuation is almost never part of the link.
      let url = m[1];
      let tail = "";
      const stripped = url.match(/^(.*?)([.,;:!?)\]]+)$/);
      if (stripped) {
        url = stripped[1]!;
        tail = stripped[2]!;
      }
      if (start > last) parts.push({ kind: "text", text: body.slice(last, start) });
      parts.push({ kind: "link", url, video: looksLikeVideo(url) });
      if (tail) parts.push({ kind: "text", text: tail });
      last = start + m[0].length;
    } else if (m[3]) {
      const lead = m[2] ?? "";
      const at = start + lead.length;
      if (at > last) parts.push({ kind: "text", text: body.slice(last, at) });
      parts.push({ kind: "mention", name: m[3] });
      last = start + m[0].length;
    } else if (m[5]) {
      const lead = m[4] ?? "";
      const at = start + lead.length;
      const ms = parseTimecode(m[5]);
      if (ms === null) continue;
      if (at > last) parts.push({ kind: "text", text: body.slice(last, at) });
      parts.push({ kind: "time", text: m[5], ms });
      last = start + m[0].length;
    }
  }
  if (last < body.length) parts.push({ kind: "text", text: body.slice(last) });
  return parts;
}

// parseTimecode turns "1:23" or "1:02:03" into milliseconds; null when the
// minute or second fields are out of range.
export function parseTimecode(s: string): number | null {
  const parts = s.split(":").map(Number);
  if (parts.some((n) => Number.isNaN(n))) return null;
  const [h, m, sec] = parts.length === 3 ? parts : [0, parts[0]!, parts[1]!];
  if (m! > 59 || sec! > 59) return null;
  return ((h! * 60 + m!) * 60 + sec!) * 1000;
}

export function mentions(body: string, username: string): boolean {
  const me = username.toLowerCase();
  return parseMessage(body).some((p) => p.kind === "mention" && p.name.toLowerCase() === me);
}

// mentionQuery returns the "@prefix" being typed at the caret, if any.
export function mentionQuery(text: string, caret: number): { start: number; prefix: string } | null {
  const before = text.slice(0, caret);
  const m = before.match(/(?:^|[^\w@])@([A-Za-z0-9_]*)$/);
  if (!m) return null;
  return { start: caret - m[1]!.length - 1, prefix: m[1]! };
}
