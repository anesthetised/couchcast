import shaka from "shaka-player";

export interface QualityOption {
  height: number;
  bandwidth: number;
}

// Player wraps Shaka: loads tokenised manifests and exposes quality
// selection. All segment requests carry ?t=<token> via a request filter.
export class Player {
  readonly shaka: shaka.Player;
  private token = "";
  onBuffering: (buffering: boolean) => void = () => {};
  onTracks: (tracks: QualityOption[], active: number | null) => void = () => {};
  onError: (message: string) => void = () => {};

  constructor(
    private readonly video: HTMLVideoElement,
    options: { maxHeight?: number } = {},
  ) {
    shaka.polyfill.installAll();
    this.shaka = new shaka.Player();
    this.shaka.configure({
      streaming: { bufferingGoal: 20, rebufferingGoal: 2 },
      abr: { enabled: true, restrictions: options.maxHeight ? { maxHeight: options.maxHeight } : {} },
    });
    this.shaka.getNetworkingEngine()?.registerRequestFilter((_type, request) => {
      request.uris = request.uris.map((u) => (u.includes("?t=") ? u : `${u}?t=${this.token}`));
    });
    this.shaka.addEventListener("buffering", (e) => {
      this.onBuffering(Boolean((e as unknown as { buffering: boolean }).buffering));
    });
    this.shaka.addEventListener("error", (e) => {
      const detail = (e as unknown as { detail: shaka.util.Error }).detail;
      // Recoverable errors (a failed segment fetch while the server is
      // away) are retried by Shaka; only critical ones need the user.
      if (detail.severity === shaka.util.Error.Severity.CRITICAL) this.onError(`Player error ${detail.code}`);
    });
    const emit = () => this.emitTracks();
    this.shaka.addEventListener("trackschanged", emit);
    this.shaka.addEventListener("adaptation", emit);
    this.shaka.addEventListener("variantchanged", emit);
  }

  async attach() {
    await this.shaka.attach(this.video);
  }

  async load(manifest: string, token: string, startMs: number) {
    this.token = token;
    await this.shaka.load(manifest, startMs / 1000);
    this.emitTracks();
  }

  async unload() {
    await this.shaka.unload();
    this.onTracks([], null);
  }

  // setToken refreshes the token without reloading (tokens expire hourly).
  setToken(token: string) {
    this.token = token;
  }

  // selectQuality picks a fixed height, or re-enables ABR with null.
  selectQuality(height: number | null) {
    if (height === null) {
      this.shaka.configure({ abr: { enabled: true } });
      this.emitTracks();
      return;
    }
    const track = this.shaka.getVariantTracks().find((t) => t.height === height);
    if (!track) return;
    this.shaka.configure({ abr: { enabled: false } });
    this.shaka.selectVariantTrack(track, true);
    this.emitTracks();
  }

  private emitTracks() {
    const tracks = this.shaka.getVariantTracks();
    const seen = new Map<number, QualityOption>();
    for (const t of tracks) {
      if (t.height && !seen.has(t.height)) seen.set(t.height, { height: t.height, bandwidth: t.bandwidth });
    }
    const active = tracks.find((t) => t.active)?.height ?? null;
    this.onTracks(
      [...seen.values()].sort((a, b) => b.height - a.height),
      active,
    );
  }

  destroy() {
    void this.shaka.destroy();
  }
}
