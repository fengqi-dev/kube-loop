import {
  ArrowDown,
  ArrowUp,
  Cable,
  CloudDownload,
  CloudUpload,
  Microchip,
} from "lucide-react";
import {
  CartesianGrid,
  Line,
  LineChart,
  XAxis,
  YAxis,
} from "recharts";
import { useMemo } from "react";
import { Card, CardContent } from "@/components/ui/card";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import { useTrafficHistory } from "@/hooks/use-traffic-history";
import { useI18n } from "@/i18n";
import { formatBytes, formatSpeed } from "@/lib/format";
import type { Metrics } from "@/types";

const chartConfig = {
  download: {
    label: "Download",
    color: "var(--chart-2)",
  },
  upload: {
    label: "Upload",
    color: "var(--chart-5)",
  },
} satisfies ChartConfig;

export function NetworkTrafficChart({
  ready,
  metrics,
}: {
  ready: boolean;
  metrics?: Metrics;
}) {
  const { t, locale } = useI18n();
  const traffic = useTrafficHistory(ready, metrics);
  const tooltipTimeFormatter = (value: number) =>
    new Intl.DateTimeFormat(locale, {
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    }).format(value);
  const axisTimeFormatter = (value: number) =>
    new Intl.DateTimeFormat(locale, {
      hour: "2-digit",
      minute: "2-digit",
    }).format(value);
  const chartEnd = traffic.history.at(-1)?.at ?? Date.now();
  const chartStart = chartEnd - traffic.windowMs;
  const timeTicks = useMemo(
    () =>
      Array.from(
        { length: 6 },
        (_, index) => chartStart + (index * traffic.windowMs) / 5,
      ),
    [chartStart, traffic.windowMs],
  );

  const localizedConfig = {
    ...chartConfig,
    download: {
      ...chartConfig.download,
      label: t("traffic.downloadSpeed"),
    },
    upload: {
      ...chartConfig.upload,
      label: t("traffic.uploadSpeed"),
    },
  } satisfies ChartConfig;

  return (
    <Card className="mb-4 gap-3 py-3">
      <CardContent className="px-3">
        <div className="relative overflow-hidden rounded-xl border bg-muted/35 px-2 pt-2">
          <div className="pointer-events-none absolute top-3 right-4 z-10 flex flex-col items-end gap-1 text-[11px] font-medium">
            <span className="text-chart-5">{t("traffic.upload")}</span>
            <span className="text-chart-2">{t("traffic.download")}</span>
          </div>
          <ChartContainer
            config={localizedConfig}
            className="h-[160px] min-h-[160px] w-full aspect-auto"
            initialDimension={{ width: 960, height: 160 }}
          >
            <LineChart
              accessibilityLayer
              data={traffic.history}
              margin={{ top: 28, right: 12, left: 0, bottom: 2 }}
            >
              <CartesianGrid strokeDasharray="0" strokeOpacity={0.5} />
              <YAxis
                axisLine={false}
                tickLine={false}
                tickMargin={8}
                width={52}
                domain={[0, "auto"]}
                tick={{ fontSize: 10 }}
                tickFormatter={formatAxisBytes}
              />
              <XAxis
                dataKey="at"
                type="number"
                scale="time"
                domain={[chartStart, chartEnd]}
                axisLine={false}
                tickLine={false}
                tickMargin={10}
                ticks={timeTicks}
                interval={0}
                minTickGap={48}
                tick={{ fontSize: 10 }}
                tickFormatter={axisTimeFormatter}
              />
              <ChartTooltip
                cursor={false}
                content={
                  <ChartTooltipContent
                    indicator="line"
                    labelFormatter={(_, payload) =>
                      payload?.[0]?.payload?.at
                        ? tooltipTimeFormatter(payload[0].payload.at)
                        : ""
                    }
                    formatter={(value, name, item) => (
                      <div className="flex w-full min-w-40 items-center gap-2">
                        <span
                          className="h-3 w-1 rounded-full"
                          style={{ backgroundColor: item.color }}
                        />
                        <span className="text-muted-foreground">
                          {localizedConfig[name as keyof typeof localizedConfig]?.label}
                        </span>
                        <span className="ml-auto font-mono font-medium text-foreground tabular-nums">
                          {formatSpeed(Number(value))}
                        </span>
                      </div>
                    )}
                  />
                }
              />
              <Line
                dataKey="download"
                type="monotone"
                stroke="var(--color-download)"
                strokeWidth={2.5}
                dot={false}
                activeDot={{ r: 3 }}
                isAnimationActive={false}
              />
              <Line
                dataKey="upload"
                type="monotone"
                stroke="var(--color-upload)"
                strokeWidth={2.5}
                dot={false}
                activeDot={{ r: 3 }}
                isAnimationActive={false}
              />
            </LineChart>
          </ChartContainer>
        </div>

        <div className="mt-3 grid grid-cols-6 gap-1.5">
            <TrafficValue
              icon={ArrowUp}
              label={t("traffic.uploadSpeed")}
              value={formatSpeed(traffic.uploadSpeed)}
              iconClassName="bg-chart-5/10 text-chart-5"
              cardClassName="border-chart-5/20 bg-chart-5/[0.035]"
            />
            <TrafficValue
              icon={ArrowDown}
              label={t("traffic.downloadSpeed")}
              value={formatSpeed(traffic.downloadSpeed)}
              iconClassName="bg-chart-2/10 text-chart-2"
              cardClassName="border-chart-2/20 bg-chart-2/[0.035]"
            />
            <TrafficValue
              icon={Cable}
              label={t("traffic.activeConnections")}
              value={String(traffic.connections)}
              iconClassName="bg-chart-1/10 text-chart-1"
              cardClassName="border-chart-1/20 bg-chart-1/[0.035]"
            />
            <TrafficValue
              icon={CloudUpload}
              label={t("traffic.uploadTotal")}
              value={formatBytes(traffic.uploadTotal)}
              iconClassName="bg-chart-5/10 text-chart-5"
              cardClassName="border-chart-5/20 bg-chart-5/[0.035]"
            />
            <TrafficValue
              icon={CloudDownload}
              label={t("traffic.downloadTotal")}
              value={formatBytes(traffic.downloadTotal)}
              iconClassName="bg-chart-2/10 text-chart-2"
              cardClassName="border-chart-2/20 bg-chart-2/[0.035]"
            />
            <TrafficValue
              icon={Microchip}
              label={t("traffic.memory")}
              value={formatBytes(traffic.memory)}
              iconClassName="bg-destructive/10 text-destructive"
              cardClassName="border-destructive/20 bg-destructive/[0.035]"
            />
        </div>
      </CardContent>
    </Card>
  );
}

function formatAxisBytes(value: number) {
  return formatBytes(value).replace(" ", "");
}

function TrafficValue({
  icon: Icon,
  label,
  value,
  iconClassName,
  cardClassName,
}: {
  icon: typeof ArrowDown;
  label: string;
  value: string;
  iconClassName: string;
  cardClassName: string;
}) {
  return (
    <div
      className={`flex min-w-0 items-center gap-1.5 rounded-lg border px-2 py-2 ${cardClassName}`}
      title={`${label}: ${value}`}
    >
      <div className={`grid size-6 shrink-0 place-items-center rounded-full ${iconClassName}`}>
        <Icon size={13} strokeWidth={2} />
      </div>
      <div className="min-w-0">
        <div className="truncate text-[9px] text-muted-foreground">{label}</div>
        <div className="mt-0.5 truncate font-mono text-[12px] font-semibold tabular-nums">
          {value}
        </div>
      </div>
    </div>
  );
}
