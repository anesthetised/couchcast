import { describe, expect, it } from "vitest";

import { looksLikeVideo, mentionQuery, mentions, parseMessage, parseTimecode } from "~/lib/chatText";

describe("parseMessage", () => {
  it("keeps plain text whole", () => {
    expect(parseMessage("just words")).toEqual([{ kind: "text", text: "just words" }]);
  });

  it("splits links and strips trailing punctuation", () => {
    expect(parseMessage("see https://youtu.be/abc, then")).toEqual([
      { kind: "text", text: "see " },
      { kind: "link", url: "https://youtu.be/abc", video: true },
      { kind: "text", text: "," },
      { kind: "text", text: " then" },
    ]);
  });

  it("finds mentions only as whole words", () => {
    expect(parseMessage("hi @alice!")).toEqual([
      { kind: "text", text: "hi " },
      { kind: "mention", name: "alice" },
      { kind: "text", text: "!" },
    ]);
    // An address is not a mention, a two-letter name is too short.
    expect(parseMessage("mail bob@example.com").some((p) => p.kind === "mention")).toBe(false);
    expect(parseMessage("@al").some((p) => p.kind === "mention")).toBe(false);
  });

  it("turns timecodes into milliseconds", () => {
    expect(parseMessage("look at 1:23 and 1:02:03")).toEqual([
      { kind: "text", text: "look at " },
      { kind: "time", text: "1:23", ms: 83_000 },
      { kind: "text", text: " and " },
      { kind: "time", text: "1:02:03", ms: 3_723_000 },
    ]);
  });

  it("leaves impossible timecodes and ratios as text", () => {
    expect(parseMessage("score 3:75").every((p) => p.kind === "text")).toBe(true);
    expect(parseMessage("at 12:30:00:00").every((p) => p.kind === "text")).toBe(true);
  });
});

describe("parseTimecode", () => {
  it.each([
    ["0:05", 5_000],
    ["10:00", 600_000],
    ["2:00:01", 7_201_000],
  ])("%s → %d ms", (s, ms) => {
    expect(parseTimecode(s)).toBe(ms);
  });

  it("rejects out-of-range fields", () => {
    expect(parseTimecode("1:60")).toBeNull();
    expect(parseTimecode("1:61:00")).toBeNull();
  });
});

describe("looksLikeVideo", () => {
  it.each([
    ["https://www.youtube.com/watch?v=x", true],
    ["https://m.youtube.com/watch?v=x", true],
    ["https://vimeo.com/1", true],
    ["https://cdn.example.com/clip.mp4?sig=1", true],
    ["https://example.com/page", false],
    ["not a url", false],
  ])("%s → %s", (url, want) => {
    expect(looksLikeVideo(url)).toBe(want);
  });
});

describe("mentions", () => {
  it("matches the name case-insensitively", () => {
    expect(mentions("hey @Alice", "alice")).toBe(true);
    expect(mentions("hey @alicia", "alice")).toBe(false);
  });
});

describe("mentionQuery", () => {
  it("returns the prefix being typed at the caret", () => {
    expect(mentionQuery("hi @al", 6)).toEqual({ start: 3, prefix: "al" });
    expect(mentionQuery("@", 1)).toEqual({ start: 0, prefix: "" });
  });

  it("ignores addresses and finished words", () => {
    expect(mentionQuery("bob@ex", 6)).toBeNull();
    expect(mentionQuery("@al done", 8)).toBeNull();
  });
});
