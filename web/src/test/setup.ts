import { cleanup } from "@solidjs/testing-library";
import { afterEach } from "vitest";

// Rendered components are removed after every test.
afterEach(cleanup);

// jsdom lacks a few browser APIs the app reads; tests get inert versions.
if (!window.matchMedia) {
  window.matchMedia = (query: string) =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }) as MediaQueryList;
}
