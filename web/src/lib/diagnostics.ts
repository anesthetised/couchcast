// Diagnostics for bug reports. A ring buffer records what happened
// recently (errors, socket drops, media status changes — never chat
// text); probes registered by the player and the room store describe the
// current state; collect() assembles everything the report dialog sends.

export type EventKind = "player" | "server" | "socket" | "toast" | "media" | "window";

export interface DiagEvent {
  t: number; // client time, unix ms
  kind: EventKind;
  text: string;
  data?: unknown;
}

export interface SyncSample {
  t: number;
  driftMs: number;
  rate: number;
  action: string;
}

const MAX_EVENTS = 100;
const MAX_SYNC = 20;

const events: DiagEvent[] = [];
const syncSamples: SyncSample[] = [];

// logEvent appends to the ring buffer; data is kept small by the caller.
export function logEvent(kind: EventKind, text: string, data?: unknown) {
  events.push({ t: Date.now(), kind, text: text.slice(0, 500), data });
  if (events.length > MAX_EVENTS) events.splice(0, events.length - MAX_EVENTS);
}

// logSync keeps the recent corrections that changed something; steady
// "idle" ticks would only push the interesting ones out.
export function logSync(s: { driftMs: number; rate: number; action: string }) {
  const last = syncSamples.at(-1);
  if (s.action === "idle" && last?.action === "idle") return;
  syncSamples.push({ t: Date.now(), driftMs: Math.round(s.driftMs), rate: Math.round(s.rate * 1000) / 1000, action: s.action });
  if (syncSamples.length > MAX_SYNC) syncSamples.splice(0, syncSamples.length - MAX_SYNC);
}

// Probes describe live state at collection time. The room page registers
// "session"; the player registers "media" and the frame grabber.
type Probe = () => unknown;
const probes = new Map<string, Probe>();
let frameGrabber: (() => HTMLVideoElement | null) | null = null;

export function registerProbe(name: string, probe: Probe): () => void {
  probes.set(name, probe);
  return () => {
    if (probes.get(name) === probe) probes.delete(name);
  };
}

export function registerVideo(get: () => HTMLVideoElement | null): () => void {
  frameGrabber = get;
  return () => {
    if (frameGrabber === get) frameGrabber = null;
  };
}

let globalHooks = false;

// installGlobalHandlers records uncaught errors; called once at startup.
export function installGlobalHandlers() {
  if (globalHooks || typeof window === "undefined") return;
  globalHooks = true;
  window.addEventListener("error", (e) => {
    logEvent("window", e.message || "error", { source: e.filename, line: e.lineno, col: e.colno });
  });
  window.addEventListener("unhandledrejection", (e) => {
    const r = e.reason as unknown;
    logEvent("window", `unhandled rejection: ${r instanceof Error ? r.message : String(r)}`);
  });
}

declare const __APP_VERSION__: string;

const CODECS: Record<string, string> = {
  vp9: 'video/webm; codecs="vp09.00.10.08"',
  av1: 'video/mp4; codecs="av01.0.05M.08"',
  avc1: 'video/mp4; codecs="avc1.64001f"',
  opus: 'audio/webm; codecs="opus"',
  aac: 'audio/mp4; codecs="mp4a.40.2"',
};

type NavigatorExtras = Navigator & {
  userAgentData?: { brands?: { brand: string; version: string }[]; platform?: string; mobile?: boolean };
  deviceMemory?: number;
  connection?: { effectiveType?: string; downlink?: number; rtt?: number; saveData?: boolean };
  standalone?: boolean;
};

function device() {
  const nav = navigator as NavigatorExtras;
  const mse = typeof MediaSource !== "undefined";
  const codecs: Record<string, boolean> = {};
  for (const [name, type] of Object.entries(CODECS)) codecs[name] = mse && MediaSource.isTypeSupported(type);
  return {
    userAgent: nav.userAgent,
    brands: nav.userAgentData?.brands?.map((b) => `${b.brand} ${b.version}`),
    platform: nav.userAgentData?.platform ?? nav.platform,
    mobile: nav.userAgentData?.mobile,
    language: nav.language,
    timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    screen: { width: screen.width, height: screen.height, dpr: window.devicePixelRatio },
    viewport: { width: window.innerWidth, height: window.innerHeight },
    cores: nav.hardwareConcurrency,
    memoryGb: nav.deviceMemory,
    connection: nav.connection
      ? { type: nav.connection.effectiveType, downlinkMbps: nav.connection.downlink, rttMs: nav.connection.rtt, saveData: nav.connection.saveData }
      : undefined,
    standalone: window.matchMedia("(display-mode: standalone)").matches || nav.standalone === true,
    visibility: document.visibilityState,
    codecs,
  };
}

export interface Diagnostics {
  collectedAt: number;
  app: { version: string; route: string };
  device: ReturnType<typeof device>;
  sync: SyncSample[];
  events: DiagEvent[];
  [probe: string]: unknown;
}

// collect assembles the snapshot sent with a report. Probes that throw
// are reported as such instead of failing the whole collection.
export function collect(): Diagnostics {
  const out: Diagnostics = {
    collectedAt: Date.now(),
    app: { version: typeof __APP_VERSION__ === "string" ? __APP_VERSION__ : "dev", route: location.pathname + location.search },
    device: device(),
    sync: [...syncSamples],
    events: [...events],
  };
  for (const [name, probe] of probes) {
    try {
      out[name] = probe();
    } catch (err) {
      out[name] = { error: err instanceof Error ? err.message : String(err) };
    }
  }
  return out;
}

const FRAME_MAX_WIDTH = 1280;

// captureFrame grabs the current video frame as a JPEG. The media is
// served from our own origin, so the canvas is not tainted; any failure
// (no video, nothing decoded yet) simply yields null.
export async function captureFrame(): Promise<Blob | null> {
  const video = frameGrabber?.();
  if (!video || video.readyState < 2 || !video.videoWidth) return null;
  const scale = Math.min(1, FRAME_MAX_WIDTH / video.videoWidth);
  const canvas = document.createElement("canvas");
  canvas.width = Math.round(video.videoWidth * scale);
  canvas.height = Math.round(video.videoHeight * scale);
  const ctx = canvas.getContext("2d");
  if (!ctx) return null;
  try {
    ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
    return await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/jpeg", 0.8));
  } catch {
    return null;
  }
}

// bufferedRanges renders a TimeRanges as [[start, end], …] in seconds.
export function bufferedRanges(r: TimeRanges): [number, number][] {
  const out: [number, number][] = [];
  for (let i = 0; i < r.length; i++) out.push([Math.round(r.start(i) * 10) / 10, Math.round(r.end(i) * 10) / 10]);
  return out;
}
