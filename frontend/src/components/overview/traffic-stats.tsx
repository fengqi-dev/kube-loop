import { ArrowDown, ArrowUp, Microchip, type LucideIcon } from "lucide-react";
import { useTrafficHistory, type TrafficPoint } from "@/hooks/use-traffic-history";
import { useI18n } from "@/i18n";
import { formatBytes, formatSpeed } from "@/lib/format";
import type { Metrics } from "@/types";

const uploadColor = "var(--chart-5)";
const downloadColor = "var(--primary)";

export function TrafficStats({
  ready,
  metrics,
}: {
  ready: boolean;
  metrics?: Metrics;
}) {
  const { t } = useI18n();
  const traffic = useTrafficHistory(ready, metrics);

  return (
    <section className="shrink-0 px-2 py-1">
      <div>
        <TrafficChart history={traffic.history} windowMs={traffic.windowMs} />
        <div className="mt-2 space-y-1.5">
          <MetricRow
            icon={ArrowUp}
            iconClassName="text-chart-5"
            label={t("traffic.uploadSpeed")}
            value={formatSpeed(traffic.uploadSpeed)}
          />
          <MetricRow
            icon={ArrowDown}
            iconClassName="text-primary"
            label={t("traffic.downloadSpeed")}
            value={formatSpeed(traffic.downloadSpeed)}
          />
          <MetricRow
            icon={Microchip}
            iconClassName="text-foreground"
            label={t("traffic.memory")}
            value={formatBytes(traffic.memory)}
          />
        </div>
      </div>
    </section>
  );
}

function MetricRow({
  icon: Icon,
  iconClassName,
  label,
  value,
}: {
  icon: LucideIcon;
  iconClassName: string;
  label: string;
  value: string;
}) {
  return (
    <div className="grid grid-cols-[18px_minmax(0,1fr)_auto] items-center gap-2">
      <Icon size={16} strokeWidth={1.9} className={iconClassName} aria-hidden />
      <span className="truncate text-[10px] text-muted-foreground">{label}</span>
      <span className="whitespace-nowrap font-mono text-[12px] font-medium tabular-nums">
        {value}
      </span>
    </div>
  );
}

function TrafficChart({
  history,
  windowMs,
}: {
  history: TrafficPoint[];
  windowMs: number;
}) {
  const width = 320;
  const height = 72;
  const now = history.length > 0 ? history[history.length - 1].at : Date.now();
  const start = now - windowMs;
  const maxSpeed = Math.max(
    1,
    ...history.map((point) => Math.max(point.upload, point.download)),
  );
  const toX = (at: number) => ((at - start) / windowMs) * width;
  const toY = (value: number) => height - 4 - (value / maxSpeed) * (height - 8);

  const uploadPath = polyline(history, toX, toY, "upload");
  const downloadPath = polyline(history, toX, toY, "download");

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      className="h-[72px] w-full"
      role="img"
      aria-label="traffic chart"
    >
      {[4, 36, 68].map((y) => (
        <line
          key={y}
          x1={0}
          x2={width}
          y1={y}
          y2={y}
          stroke="currentColor"
          className="text-sidebar-ring/25"
          strokeWidth={1}
        />
      ))}
      {uploadPath ? (
        <path
          d={uploadPath}
          fill="none"
          stroke={uploadColor}
          strokeWidth={2}
          strokeLinejoin="round"
          strokeLinecap="round"
        />
      ) : null}
      {downloadPath ? (
        <path
          d={downloadPath}
          fill="none"
          stroke={downloadColor}
          strokeWidth={2}
          strokeLinejoin="round"
          strokeLinecap="round"
        />
      ) : null}
    </svg>
  );
}

function polyline(
  history: TrafficPoint[],
  toX: (at: number) => number,
  toY: (value: number) => number,
  key: "upload" | "download",
) {
  if (history.length === 0) return "";
  return history
    .map((point, index) => {
      const command = index === 0 ? "M" : "L";
      return `${command}${toX(point.at).toFixed(1)} ${toY(point[key]).toFixed(1)}`;
    })
    .join(" ");
}
