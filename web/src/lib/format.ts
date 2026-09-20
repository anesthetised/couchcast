// formatTime renders milliseconds as m:ss or h:mm:ss.
export function formatTime(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const mm = h > 0 ? String(m).padStart(2, "0") : String(m);
  return `${h > 0 ? `${h}:` : ""}${mm}:${String(s).padStart(2, "0")}`;
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
