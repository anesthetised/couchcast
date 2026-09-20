import { createSignal } from "solid-js";

// Recently visited rooms live in localStorage only: a convenience for the
// home page, never authoritative (a room may be gone or closed to us).

export type RecentRoom = { slug: string; name: string; at: number };

const KEY = "couchcast.recent";
const MAX = 5;

function read(): RecentRoom[] {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? "[]") as unknown;
    return Array.isArray(v) ? (v as RecentRoom[]).filter((r) => typeof r.slug === "string" && typeof r.name === "string") : [];
  } catch {
    return [];
  }
}

function write(list: RecentRoom[]) {
  try {
    localStorage.setItem(KEY, JSON.stringify(list));
  } catch {
    // storage unavailable
  }
}

const [recent, setRecent] = createSignal<RecentRoom[]>(read());

export function recordVisit(slug: string, name: string) {
  const list = [{ slug, name, at: Date.now() }, ...recent().filter((r) => r.slug !== slug)].slice(0, MAX);
  setRecent(list);
  write(list);
}

export function forgetVisit(slug: string) {
  const list = recent().filter((r) => r.slug !== slug);
  setRecent(list);
  write(list);
}

export { recent };
