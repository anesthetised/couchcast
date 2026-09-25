import { beforeEach, describe, expect, it } from "vitest";

import { expandShortcodes, GROUPS, recentEmoji, rememberEmoji, searchEmoji, shortcodeQuery } from "~/lib/emoji";

describe("the emoji table", () => {
  it("has unique shortcodes", () => {
    const names = GROUPS.flatMap((g) => g.emoji.flatMap((e) => e.names));
    expect(new Set(names).size).toBe(names.length);
  });
});

describe("searchEmoji", () => {
  it("puts prefix matches first and honours the limit", () => {
    const found = searchEmoji("smile");
    expect(found[0]!.names[0]).toBe("smile");
    expect(searchEmoji(":joy")[0]!.char).toBe("😂");
    expect(searchEmoji("a", 3)).toHaveLength(3);
    expect(searchEmoji("  ")).toEqual([]);
  });
});

describe("shortcodeQuery", () => {
  it("opens after two letters following a colon", () => {
    expect(shortcodeQuery("popcorn :po", 11)).toEqual({ start: 8, prefix: "po" });
    expect(shortcodeQuery(":p", 2)).toBeNull();
  });

  it("does not open inside timecodes", () => {
    expect(shortcodeQuery("at 1:23", 7)).toBeNull();
  });
});

describe("expandShortcodes", () => {
  it("replaces known codes and keeps the rest", () => {
    expect(expandShortcodes(":thumbsup: nice :nope: at 1:23:45")).toBe("👍 nice :nope: at 1:23:45");
    expect(expandShortcodes("a :+1:")).toBe("a 👍");
  });
});

describe("recent emoji", () => {
  beforeEach(() => localStorage.clear());

  it("keeps the most recent first without duplicates", () => {
    rememberEmoji("👍");
    rememberEmoji("😂");
    rememberEmoji("👍");
    expect(recentEmoji()).toEqual(["👍", "😂"]);
  });

  it("caps the list and survives junk in storage", () => {
    for (let i = 0; i < 20; i++) rememberEmoji(String(i));
    expect(recentEmoji()).toHaveLength(16);
    localStorage.setItem("couchcast.emoji.recent", "{not json");
    expect(recentEmoji()).toEqual([]);
  });
});
