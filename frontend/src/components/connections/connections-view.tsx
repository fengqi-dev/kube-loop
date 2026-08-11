import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { Activity } from "lucide-react";
import { EmptyState } from "@/components/shared/empty-state";
import { PageShell } from "@/components/shared/page-shell";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useI18n } from "@/i18n";
import {
  executableName,
  formatBytes,
  formatDuration,
  formatSpeed,
} from "@/lib/format";
import type { Connection, Metrics } from "@/types";

const NetworkTrafficChart = lazy(() =>
  import("@/components/network/network-traffic-chart").then((module) => ({
    default: module.NetworkTrafficChart,
  })),
);

const stickyForMs = 30_000;
const maxTableRows = 100;
const emptyConnections: Connection[] = [];

const columns = [
  { key: "process", align: "left" as const },
  { key: "network", align: "left" as const },
  { key: "inbound", align: "left" as const },
  { key: "destination", align: "left" as const },
  { key: "source", align: "left" as const },
  { key: "traffic", align: "right" as const },
  { key: "speed", align: "right" as const },
  { key: "outbound", align: "left" as const },
  { key: "rule", align: "left" as const },
  { key: "duration", align: "left" as const },
] as const;

function StackedMetric({
  upload,
  download,
}: {
  upload: string;
  download: string;
}) {
  return (
    <div className="flex flex-col items-end gap-0.5 font-mono text-[11px] leading-4 text-muted-foreground">
      <span>↑ {upload}</span>
      <span>↓ {download}</span>
    </div>
  );
}

/** Split clash/sing-box rule strings onto readable lines. */
function formatRuleLines(rule: string): string {
  const [match, action] = rule.split(/\s*=>\s*/, 2);
  const lines = match.split(/\s+/).filter(Boolean);
  if (action) lines.push(`=> ${action}`);
  return lines.join("\n");
}

export function ConnectionsView({
  ready,
  metrics,
}: {
  ready: boolean;
  metrics?: Metrics;
}) {
  const { t } = useI18n();
  const live = Array.isArray(metrics?.connections)
    ? metrics.connections
    : emptyConnections;
  const liveCount = live.length;
  const [sticky, setSticky] = useState<Connection[]>([]);
  const stickyUntil = useRef(0);
  const now = Date.now();

  useEffect(() => {
    if (!ready) {
      if (sticky.length > 0) setSticky([]);
      stickyUntil.current = 0;
      return;
    }
    if (liveCount > 0) {
      setSticky(live.slice(0, maxTableRows));
      stickyUntil.current = Date.now() + stickyForMs;
      return;
    }
    if (sticky.length === 0 || stickyUntil.current === 0) return;
    const remaining = Math.max(0, stickyUntil.current - Date.now());
    const timer = window.setTimeout(() => {
      setSticky([]);
      stickyUntil.current = 0;
    }, remaining);
    return () => window.clearTimeout(timer);
  }, [ready, live, liveCount, sticky.length]);

  const connections = (liveCount > 0 ? live : sticky).slice(0, maxTableRows);

  return (
    <PageShell
      title={t("connections.title")}
      description={t("connections.description")}
    >
      {!ready ? (
        <EmptyState
          icon={Activity}
          title={t("connections.disconnectedTitle")}
          detail={t("connections.disconnectedDetail")}
        />
      ) : (
        <>
          <Suspense
            fallback={
              <div className="mb-4 h-[218px] animate-pulse rounded-2xl border border-border/80 bg-card/70" />
            }
          >
            <NetworkTrafficChart ready={ready} metrics={metrics} />
          </Suspense>
          <div className="overflow-auto rounded-lg border bg-card">
            <Table>
              <TableHeader>
                <TableRow className="bg-muted/50 hover:bg-muted/50">
                  {columns.map((column) => (
                    <TableHead
                      key={column.key}
                      className={
                        column.align === "right"
                          ? "text-right text-[10px] tracking-wide uppercase"
                          : "text-[10px] tracking-wide uppercase"
                      }
                    >
                      {t(`connections.${column.key}`)}
                    </TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {connections.length === 0 ? (
                  <TableRow className="hover:bg-transparent">
                    <TableCell
                      colSpan={columns.length}
                      className="h-40 text-center text-muted-foreground"
                    >
                      <div className="mx-auto max-w-sm space-y-1">
                        <p className="text-sm font-medium text-foreground">
                          {t("connections.emptyTitle")}
                        </p>
                        <p className="text-xs">{t("connections.emptyDetail")}</p>
                      </div>
                    </TableCell>
                  </TableRow>
                ) : (
                  connections.map((connection) => (
                    <TableRow key={connection.id}>
                      <TableCell className="max-w-[140px] truncate font-medium">
                        {executableName(connection.process) ||
                          connection.process ||
                          "—"}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {connection.network?.toUpperCase() || "—"}
                      </TableCell>
                      <TableCell className="max-w-[130px] truncate text-[11px] text-muted-foreground">
                        {connection.feature || connection.inbound || "—"}
                      </TableCell>
                      <TableCell className="max-w-[220px] truncate font-mono text-[11px] text-primary">
                        {connection.destination || "—"}
                      </TableCell>
                      <TableCell className="font-mono text-[11px] text-muted-foreground">
                        {connection.source || "—"}
                      </TableCell>
                      <TableCell className="align-middle">
                        <StackedMetric
                          upload={formatBytes(connection.upload ?? 0)}
                          download={formatBytes(connection.download ?? 0)}
                        />
                      </TableCell>
                      <TableCell className="align-middle">
                        <StackedMetric
                          upload={formatSpeed(connection.uploadSpeed ?? 0)}
                          download={formatSpeed(connection.downloadSpeed ?? 0)}
                        />
                      </TableCell>
                      <TableCell className="max-w-[120px] truncate text-[11px] text-muted-foreground">
                        {connection.outbound || "—"}
                      </TableCell>
                      <TableCell className="min-w-[160px] max-w-[260px] align-top font-mono text-[11px] leading-4 text-muted-foreground">
                        {connection.rule ? (
                          <span className="block whitespace-pre-wrap break-all">
                            {formatRuleLines(connection.rule)}
                          </span>
                        ) : (
                          "—"
                        )}
                      </TableCell>
                      <TableCell className="font-mono text-[11px] text-muted-foreground">
                        {formatDuration(connection.startedAt, now)}
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </div>
        </>
      )}
    </PageShell>
  );
}
