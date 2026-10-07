import { describe, expect, it } from "vitest";

import { chapterAt, chapterTarget, nextRate, previewStyle, tipLeft } from "~/lib/timeline";
import type { MediaInfo } from "~/protocol";

const chapters = [
  { startMs: 0, endMs: 60_000, title: "Intro" },
  { startMs: 60_000, endMs: 120_000, title: "Middle" },
  { startMs: 120_000, endMs: 180_000, title: "End" },
];

describe("timeline", () => {
  it("finds the chapter at a position", () => {
    expect(chapterAt(chapters, 0)?.title).toBe("Intro");
    expect(chapterAt(chapters, 60_000)?.title).toBe("Middle");
    expect(chapterAt(chapters, 179_999)?.title).toBe("End");
    expect(chapterAt([], 5)).toBeNull();
  });

  it("steps chapters, restarting the current one when well into it", () => {
    expect(chapterTarget(chapters, 70_000, 1)?.title).toBe("End");
    expect(chapterTarget(chapters, 70_000, -1)?.title).toBe("Middle"); // 10 s in: restart
    expect(chapterTarget(chapters, 61_000, -1)?.title).toBe("Intro"); // 1 s in: previous
    expect(chapterTarget(chapters, 150_000, 1)?.title).toBe("End"); // stays at the last
    expect(chapterTarget(chapters, 1_000, -1)?.title).toBe("Intro");
    expect(chapterTarget([], 0, 1)).toBeNull();
  });

  it("steps the speed and stops at the ends", () => {
    expect(nextRate(1, 1)).toBe(1.25);
    expect(nextRate(1, -1)).toBe(0.75);
    expect(nextRate(2, 1)).toBeNull();
    expect(nextRate(0.5, -1)).toBeNull();
    expect(nextRate(1.1, 1)).toBe(1.25); // an unknown rate steps from 1
  });

  const media = {
    manifest: "/media/m/manifest.mpd",
    token: "tok",
    storyboard: { intervalMs: 10_000, count: 150, cols: 10, rows: 10, width: 160, height: 90 },
  } as MediaInfo;

  it("crops the storyboard cell for a position", () => {
    // Frame 123: second sheet, row 2, column 3.
    expect(previewStyle(media, 1_234_000)).toEqual({
      width: "160px",
      height: "90px",
      "background-image": 'url("/media/m/sb-1.jpg?t=tok")',
      "background-position": "-480px -180px",
    });
    // Past the end clamps to the last frame.
    expect(previewStyle(media, 10_000_000)!["background-position"]).toBe("-1440px -360px");
    expect(previewStyle({ ...media, storyboard: undefined }, 0)).toBeNull();
    expect(previewStyle(undefined, 0)).toBeNull();
  });

  it("keeps the seek tip inside the bar", () => {
    expect(tipLeft(media, 10, 600)).toBe(84); // half the preview plus a margin
    expect(tipLeft(media, 590, 600)).toBe(516);
    expect(tipLeft(media, 300, 600)).toBe(300);
    expect(tipLeft(undefined, 5, 600)).toBe(24);
    expect(tipLeft(media, 50, 100)).toBe(50); // narrower than the tip: follow the pointer
  });
});
