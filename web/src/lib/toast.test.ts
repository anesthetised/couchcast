import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

async function load() {
  vi.resetModules();
  return import("~/lib/toast");
}

describe("toast", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("shows at most four and dismisses info after 3 s, errors after 6 s", async () => {
    const t = await load();
    for (let i = 1; i <= 5; i++) t.toast(`info ${i}`);
    expect(t.toasts().map((x) => x.text)).toEqual(["info 2", "info 3", "info 4", "info 5"]);
    t.toast("broken", "error");
    vi.advanceTimersByTime(3_000);
    expect(t.toasts().map((x) => x.text)).toEqual(["broken"]);
    vi.advanceTimersByTime(3_000);
    expect(t.toasts()).toEqual([]);
  });

  it("logs errors for bug reports", async () => {
    const t = await load();
    const d = await import("~/lib/diagnostics");
    t.toast("could not add", "error");
    expect(d.collect().events.some((e) => e.kind === "toast" && e.text === "could not add")).toBe(true);
  });

  it("dismisses on demand", async () => {
    const t = await load();
    t.toast("hello");
    t.dismiss(t.toasts()[0]!.id);
    expect(t.toasts()).toEqual([]);
  });
});
