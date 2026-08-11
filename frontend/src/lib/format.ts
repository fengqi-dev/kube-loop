export function executableName(path: string) {
  return path.split(/[\\/]/).filter(Boolean).pop() ?? "";
}

export function formatBytes(value: number) {
  if (!Number.isFinite(value) || value < 1) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const scaled = value / 1024 ** index;
  return `${scaled >= 10 || index === 0 ? scaled.toFixed(0) : scaled.toFixed(1)} ${units[index]}`;
}

export function formatSpeed(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "0 B/s";
  return `${formatBytes(value)}/s`;
}

export function formatDuration(start: string, now = Date.now()) {
  const started = Date.parse(start);
  if (!Number.isFinite(started)) return "—";
  const totalSeconds = Math.max(0, Math.floor((now - started) / 1000));
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  return [hours, minutes, seconds].map((part) => String(part).padStart(2, "0")).join(":");
}
