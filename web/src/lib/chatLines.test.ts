import { describe, expect, it } from "vitest";

import { continues, foldRuns, lagText, typingText } from "~/lib/chatLines";
import type { ChatMessage } from "~/protocol";
import { line } from "~/test/fixtures";

const sys = (id: number): ChatMessage => ({ id, body: "event", system: true, createdMs: id });

describe("chatLines", () => {
  it("groups lines by one author within a minute", () => {
    const a = line(1, "hi", { createdMs: 0 });
    expect(continues(undefined, a)).toBe(false);
    expect(continues(a, line(2, "again", { createdMs: 59_999 }))).toBe(true);
    expect(continues(a, line(2, "later", { createdMs: 60_000 }))).toBe(false);
    expect(continues(a, line(2, "eve", { username: "eve", createdMs: 1 }))).toBe(false);
    expect(continues(sys(1), sys(2))).toBe(false);
  });

  it("folds runs of three or more system lines but the last", () => {
    const runs = foldRuns([line(1, "hi"), sys(2), sys(3), sys(4), line(5, "yo"), sys(6), sys(7)]);
    expect([...runs.entries()]).toEqual([
      [2, { start: 2, hidden: 2 }],
      [3, { start: 2, hidden: 2 }],
    ]);
    expect(foldRuns([sys(1), sys(2), sys(3)]).size).toBe(2); // a run at the very end too
  });

  it("describes lag and typing", () => {
    expect(lagText(2500)).toBe("2.5 s behind");
    expect(lagText(-1000)).toBe("1 s ahead");
    expect(typingText(["bob"])).toBe("bob is typing…");
    expect(typingText(["bob", "eve"])).toBe("bob and eve are typing…");
    expect(typingText(["bob", "eve", "ann", "joe"])).toBe("bob, eve and 2 more are typing…");
  });
});
