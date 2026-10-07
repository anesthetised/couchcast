import { describe, expect, it } from "vitest";

import { playerHotkey } from "~/lib/hotkeys";

const press = (key: string, init: KeyboardEventInit = {}, target?: HTMLElement) => {
  const e = new KeyboardEvent("keydown", { key, ...init });
  if (target) Object.defineProperty(e, "target", { value: target });
  return playerHotkey(e);
};

describe("playerHotkey", () => {
  it("maps keys to actions", () => {
    expect(press(" ")).toBe("togglePlay");
    expect(press("ArrowLeft")).toBe("back");
    expect(press("F")).toBe("fullscreen");
    expect(press("m")).toBe("mute");
    expect(press(",")).toBe("slower");
    expect(press(">")).toBe("faster");
    expect(press("]")).toBe("nextChapter");
    expect(press("?")).toBe("help");
    expect(press("Escape")).toBe("escape");
    expect(press("B", { shiftKey: true })).toBe("bugReport");
    expect(press("b")).toBeNull();
    expect(press("x")).toBeNull();
  });

  it("ignores keys while typing and with modifiers", () => {
    for (const tag of ["input", "textarea", "select"]) expect(press("m", {}, document.createElement(tag))).toBeNull();
    const editable = document.createElement("div");
    Object.defineProperty(editable, "isContentEditable", { value: true });
    expect(press("m", {}, editable)).toBeNull();
    expect(press("f", { ctrlKey: true })).toBeNull();
    expect(press("f", { metaKey: true })).toBeNull();
    expect(press("f", { altKey: true })).toBeNull();
  });
});
