import { createRoot, createSignal } from "solid-js";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createReadingPosition } from "~/lib/readingPosition";
import type { ChatMessage } from "~/protocol";
import { line } from "~/test/fixtures";

const tick = () => new Promise((r) => setTimeout(r, 0));

describe("createReadingPosition", () => {
  let visibility: DocumentVisibilityState;
  let list: HTMLElement;
  let height: number;

  beforeEach(() => {
    visibility = "visible";
    vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
    height = 1000;
    list = document.createElement("ul");
    Object.defineProperty(list, "scrollHeight", { get: () => height });
    Object.defineProperty(list, "clientHeight", { value: 200 });
  });
  afterEach(() => vi.restoreAllMocks());

  function mount(initial: ChatMessage[]) {
    return createRoot((dispose) => {
      const [messages, setMessages] = createSignal(initial);
      const reading = createReadingPosition(() => list, messages);
      const add = (...m: ChatMessage[]) => setMessages((all) => [...all, ...m]);
      return { reading, add, dispose };
    });
  }
  const hide = () => ((visibility = "hidden"), document.dispatchEvent(new Event("visibilitychange")));
  const show = () => ((visibility = "visible"), document.dispatchEvent(new Event("visibilitychange")));
  const scrollUp = (r: ReturnType<typeof mount>["reading"]) => ((list.scrollTop = 0), r.onScroll());

  it("lands the backlog at the bottom and follows new lines there", async () => {
    const { add, reading, dispose } = mount([line(1, "a")]);
    await tick();
    expect(list.scrollTop).toBe(1000);
    height = 1200;
    add(line(2, "b"));
    await tick();
    expect(list.scrollTop).toBe(1200);
    expect(reading.unseen()).toBe(0);
    dispose();
  });

  it("counts new lines while the reader is scrolled up", async () => {
    const { add, reading, dispose } = mount([line(1, "a")]);
    await tick();
    scrollUp(reading);
    expect(reading.atBottom()).toBe(false);
    add(line(2, "b"), line(3, "c"));
    await tick();
    expect(reading.unseen()).toBe(2);
    expect(list.scrollTop).toBe(0); // the reader stays where they are
    reading.scrollToBottom();
    expect(reading.unseen()).toBe(0);
    expect(reading.atBottom()).toBe(true);
    dispose();
  });

  it("marks where a hidden tab left off and catches up when it returns", async () => {
    const { add, reading, dispose } = mount([line(1, "a"), line(2, "b")]);
    await tick();
    hide();
    add(line(3, "c"));
    await tick();
    expect(reading.unseen()).toBe(1);
    show();
    expect(reading.divider()).toBe(2); // the divider goes after the last line seen
    expect(reading.unseen()).toBe(0);
    reading.clearDivider();
    expect(reading.divider()).toBeNull();
    dispose();
  });

  it("puts no divider when nothing arrived while hidden", async () => {
    const { reading, dispose } = mount([line(1, "a")]);
    await tick();
    hide();
    show();
    expect(reading.divider()).toBeNull();
    dispose();
  });

  it("stops listening once disposed", async () => {
    const removed = vi.spyOn(document, "removeEventListener");
    const { reading, dispose } = mount([line(1, "a")]);
    dispose();
    expect(removed).toHaveBeenCalledWith("visibilitychange", expect.any(Function));
    show();
    expect(reading.divider()).toBeNull();
  });
});
