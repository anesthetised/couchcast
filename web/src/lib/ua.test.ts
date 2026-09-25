import { describe, expect, it } from "vitest";

import { describeUA, uaBrowser } from "~/lib/ua";

const UA = {
  chromeMac:
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
  edgeWin:
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0",
  firefoxLinux: "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0",
  safariIPhone:
    "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
  operaAndroid:
    "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Mobile Safari/537.36 OPR/85.0.0.0",
  chromebook: "Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
};

describe("describeUA", () => {
  it.each([
    [UA.chromeMac, "Chrome 140 · macOS"],
    [UA.edgeWin, "Edge 140 · Windows"],
    [UA.firefoxLinux, "Firefox 140 · Linux"],
    [UA.safariIPhone, "Safari 18 · iOS"],
    [UA.operaAndroid, "Opera 85 · Android"],
    [UA.chromebook, "Chrome 140 · ChromeOS"],
  ])("%s", (ua, want) => {
    expect(describeUA(ua)).toBe(want);
  });

  it("drops what it cannot tell", () => {
    expect(describeUA("")).toBe("");
    expect(describeUA("curl/8.0")).toBe("");
    expect(uaBrowser("curl/8.0")).toBeUndefined();
  });
});
