// FakePlayer stands in for the Shaka wrapper: it records calls and lets
// a test raise the events Shaka would.
export const players: FakePlayer[] = [];
export class FakePlayer {
  calls: [string, ...unknown[]][] = [];
  onBuffering: (b: boolean) => void = () => {};
  onTracks: (t: { height: number; width: number; bandwidth: number; active: boolean }[], active: number | null) => void = () => {};
  onError: (m: string) => void = () => {};
  constructor(public video: HTMLVideoElement) {
    players.push(this);
  }
  attach = () => (this.calls.push(["attach"]), Promise.resolve());
  load = (manifest: string, token: string, startMs: number) => (this.calls.push(["load", manifest, token, startMs]), Promise.resolve());
  unload = () => (this.calls.push(["unload"]), Promise.resolve());
  preload = () => Promise.resolve();
  setTokenFor = (manifest: string, token: string) => void this.calls.push(["setTokenFor", manifest, token]);
  addSubtitles = (manifest: string, subs: unknown) => (this.calls.push(["addSubtitles", manifest, subs]), Promise.resolve());
  selectSubtitle = (lang: string | null) => void this.calls.push(["selectSubtitle", lang]);
  selectQuality = (h: number | null) => void this.calls.push(["selectQuality", h]);
  stats = () => ({});
  destroy = () => void this.calls.push(["destroy"]);
}
