// describeUA turns a user agent into "Chrome 140 · macOS"; unknown parts
// are left out. Good enough to recognise one's own devices.
export function describeUA(ua: string): string {
  return [uaBrowser(ua), uaPlatform(ua)].filter(Boolean).join(" · ");
}

// Edge and Opera also carry a Chrome token (and Chrome a Safari one), so
// the more specific tokens are tried first.
const BROWSERS: [RegExp, string][] = [
  [/Edg\/(\d+)/, "Edge"],
  [/OPR\/(\d+)/, "Opera"],
  [/Firefox\/(\d+)/, "Firefox"],
  [/Chrome\/(\d+)/, "Chrome"],
  [/Version\/(\d+).*Safari/, "Safari"],
];

export function uaBrowser(ua: string): string | undefined {
  for (const [re, name] of BROWSERS) {
    const m = ua.match(re);
    if (m) return `${name} ${m[1]}`;
  }
  return undefined;
}

function uaPlatform(ua: string): string | undefined {
  if (/iPhone|iPad|iPod/.test(ua)) return "iOS";
  if (/Android/.test(ua)) return "Android";
  if (/CrOS/.test(ua)) return "ChromeOS";
  if (/Mac OS X|Macintosh/.test(ua)) return "macOS";
  if (/Windows/.test(ua)) return "Windows";
  if (/Linux/.test(ua)) return "Linux";
  return undefined;
}
