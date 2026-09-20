import shaka from "shaka-player";

// prefixOf returns "/media/<id>/" for any URL under a media prefix.
function prefixOf(u: string): string {
  const i = u.indexOf("/media/");
  if (i < 0) return "";
  const j = u.indexOf("/", i + "/media/".length);
  return j < 0 ? u.slice(i) : u.slice(i, j + 1);
}

export interface QualityOption {
  height: number;
  bandwidth: number;
}

// Player wraps Shaka: loads tokenised manifests and exposes quality
// selection. All segment requests carry ?t=<token> via a request filter.
export class Player {
  readonly shaka: shaka.Player;
  // Tokens by media prefix ("/media/<id>/"): the current item and a
  // preloaded next one may be fetching at the same time.
  private tokens = new Map<string, string>();
  private preloaded: { manifest: string; manager: shaka.media.PreloadManager } | null = null;
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
      request.uris = request.uris.map((u) => {
        if (u.includes("?t=")) return u;
        const token = this.tokens.get(prefixOf(u));
        return token ? `${u}?t=${token}` : u;
      });
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
    this.tokens.set(prefixOf(manifest), token);
    // A matching preload carries the parsed manifest and first segments;
    // anything else is dropped.
    const pre = this.preloaded;
    this.preloaded = null;
    if (pre && pre.manifest === manifest) {
      await this.shaka.load(pre.manager, startMs / 1000);
    } else {
      if (pre) void pre.manager.destroy();
      await this.shaka.load(manifest, startMs / 1000);
    }
    this.emitTracks();
  }

  // preload fetches the next item's manifest and first segments ahead of
  // time so auto-advance starts without a black gap.
  async preload(manifest: string, token: string) {
    if (this.preloaded?.manifest === manifest) return;
    if (this.preloaded) void this.preloaded.manager.destroy();
    this.preloaded = null;
    this.tokens.set(prefixOf(manifest), token);
    try {
      const manager = await this.shaka.preload(manifest, 0);
      if (manager) this.preloaded = { manifest, manager };
    } catch {
      // preloading is best effort
    }
  }

  // setToken refreshes a media's token without reloading.
  setTokenFor(manifest: string, token: string) {
    this.tokens.set(prefixOf(manifest), token);
  }

  // addSubtitles attaches sidecar WebVTT tracks next to the manifest
  // (sub-<lang>.vtt under the same media prefix, so the token applies).
  async addSubtitles(manifest: string, subs: { lang: string; name: string }[]) {
    const base = manifest.slice(0, manifest.lastIndexOf("/") + 1);
    for (const s of subs) {
      try {
        await this.shaka.addTextTrackAsync(`${base}sub-${s.lang}.vtt`, s.lang, "subtitles", "text/vtt", undefined, s.name || s.lang);
      } catch {
        // a missing or malformed track just does not show up
      }
    }
  }

  // selectSubtitle turns a language on, or all captions off with null
  // (in Shaka 5 an unselected text track is a hidden one).
  selectSubtitle(lang: string | null) {
    if (lang === null) {
      this.shaka.selectTextTrack(null);
      return;
    }
    const track = this.shaka.getTextTracks().find((t) => t.language === lang);
    if (track) this.shaka.selectTextTrack(track);
  }

  async unload() {
    if (this.preloaded) {
      void this.preloaded.manager.destroy();
      this.preloaded = null;
    }
    await this.shaka.unload();
    this.onTracks([], null);
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
