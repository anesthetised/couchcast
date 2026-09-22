// formatTime renders milliseconds as m:ss or h:mm:ss.
export function formatTime(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const mm = h > 0 ? String(m).padStart(2, "0") : String(m);
  return `${h > 0 ? `${h}:` : ""}${mm}:${String(s).padStart(2, "0")}`;
}

// formatAgo renders how long ago a moment was: "just now", "5m ago",
// "3h ago", "2d ago".
export function formatAgo(ms: number, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - ms) / 1000));
  if (s < 60) return "just now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

// formatDuration renders a coarse length for summaries: "3h 12m", "45m",
// "< 1m".
export function formatDuration(ms: number): string {
  const min = Math.round(ms / 60_000);
  if (min < 1) return "< 1m";
  const h = Math.floor(min / 60);
  const m = min % 60;
  return h > 0 ? `${h}h ${m}m` : `${m}m`;
}

// formatSpeed renders bytes per second: "1.2 MB/s", "340 KB/s".
export function formatSpeed(bps: number): string {
  if (bps >= 1 << 20) return `${(bps / (1 << 20)).toFixed(1)} MB/s`;
  if (bps >= 1 << 10) return `${Math.round(bps / (1 << 10))} KB/s`;
  return `${Math.round(bps)} B/s`;
}

// formatEta renders time left in the coarsest useful unit: "12 s", "3 min",
// "1h 05m".
export function formatEta(ms: number): string {
  const s = Math.max(1, Math.round(ms / 1000));
  if (s < 90) return `${s} s`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m} min`;
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, "0")}m`;
}

// progressDetail describes an ingest step in flight: percentage plus
// speed and time left when known.
export function progressDetail(m: { status: string; progress: number; speedBps?: number; etaMs?: number }): string | null {
  if (m.status !== "downloading" && m.status !== "packaging") return null;
  const parts = [`${Math.round(m.progress * 100)}%`];
  if (m.speedBps) parts.push(formatSpeed(m.speedBps));
  if (m.etaMs) parts.push(`${formatEta(m.etaMs)} left`);
  return parts.join(" · ");
}
