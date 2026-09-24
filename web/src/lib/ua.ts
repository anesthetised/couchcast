// describeUA turns a user agent into "Chrome 140 · macOS"; unknown parts
// are left out. Good enough to recognise one's own devices.
export function describeUA(ua: string): string {
  return [uaBrowser(ua), uaPlatform(ua)].filter(Boolean).join(" · ");
}

export function uaBrowser(ua: string): string | undefined {
  const m = ua.match(/(Firefox|Edg|OPR|Chrome|Version)\/(\d+)/);
  if (!m) return undefined;
  const name = m[1] === "Version" ? "Safari" : m[1] === "Edg" ? "Edge" : m[1] === "OPR" ? "Opera" : m[1]!;
  return `${name} ${m[2]}`;
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
