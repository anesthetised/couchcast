import { describe, expect, it } from "vitest";

import { AVATAR_COLORS, avatarClass, isModerator } from "~/lib/types";

describe("avatarClass", () => {
  it("uses the chosen colour", () => {
    expect(avatarClass("alice", "teal")).toBe("c-teal");
  });

  it("derives a stable palette colour from the name, ignoring case", () => {
    const c = avatarClass("Alice");
    expect(c).toBe(avatarClass("alice"));
    expect(AVATAR_COLORS.map((x) => `c-${x}`)).toContain(c);
  });
});

describe("isModerator", () => {
  it("covers owners and moderators", () => {
    expect(isModerator("owner")).toBe(true);
    expect(isModerator("moderator")).toBe(true);
    expect(isModerator("member")).toBe(false);
    expect(isModerator(undefined)).toBe(false);
  });
});
