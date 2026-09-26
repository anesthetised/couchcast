import { fireEvent, render, screen } from "@solidjs/testing-library";
import { createSignal } from "solid-js";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import UsernamePicker from "~/components/UsernamePicker";

let fetchMock: ReturnType<typeof vi.fn>;
let names: () => string[];

function mount(initial: string[] = [], exclude?: string[]) {
  render(() => {
    const [value, setValue] = createSignal(initial);
    names = value;
    return <UsernamePicker value={value()} onChange={setValue} exclude={exclude} />;
  });
  return screen.getByRole("textbox") as HTMLInputElement;
}

const type = (input: HTMLInputElement, text: string) => fireEvent.input(input, { target: { value: text } });
const queries = () => fetchMock.mock.calls.map(([url]) => String(url));

describe("UsernamePicker", () => {
  beforeEach(() => {
    fetchMock = vi.fn(async (url: string) => {
      const q = new URL(url, "http://x").searchParams.get("q") ?? "";
      const all = ["alice", "alex", "albert", "bob"];
      return new Response(JSON.stringify(all.filter((n) => n.startsWith(q))), { status: 200, headers: { "Content-Type": "application/json" } });
    });
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("suggests users for an @-prefixed query of two characters", async () => {
    const input = mount(["alex"], ["albert"]);
    type(input, "@al");
    const option = await screen.findByRole("option", { name: "alice" });
    expect(queries()).toEqual(["/api/v1/users?q=al"]);
    // Chosen and excluded names are not offered again.
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual(["alice"]);
    fireEvent.mouseDown(option);
    expect(names()).toEqual(["alex", "alice"]);
    expect(input.value).toBe("");
  });

  it("waits for two characters after the @", async () => {
    const input = mount();
    type(input, "@");
    type(input, "@a");
    await new Promise((r) => setTimeout(r, 300));
    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  it("picks with the arrow keys", async () => {
    const input = mount();
    type(input, "al");
    await screen.findByRole("option", { name: "albert" });
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(screen.getByRole("option", { name: "alex" }).getAttribute("aria-selected")).toBe("true");
    fireEvent.keyDown(input, { key: "Enter" });
    expect(names()).toEqual(["alex"]);
  });

  it("adds typed names without the @ and removes them again", () => {
    const input = mount();
    type(input, "@bob");
    fireEvent.keyDown(input, { key: "Enter" });
    type(input, "@@carol");
    fireEvent.keyDown(input, { key: "," });
    type(input, "@BOB");
    fireEvent.keyDown(input, { key: "Enter" });
    expect(names()).toEqual(["bob", "carol"]);

    type(input, "");
    fireEvent.keyDown(input, { key: "Backspace" });
    expect(names()).toEqual(["bob"]);
    fireEvent.click(screen.getByRole("button", { name: "Remove bob" }));
    expect(names()).toEqual([]);
  });
});
