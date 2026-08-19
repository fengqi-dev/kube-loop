import { Fragment, useEffect, useState } from "react";
import { ChevronDown, ChevronRight, Copy, FileSearch2 } from "lucide-react";
import { toast } from "sonner";
import { backend } from "@/backend";
import { EmptyState } from "@/components/shared/empty-state";
import { PageShell } from "@/components/shared/page-shell";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useI18n } from "@/i18n";
import type { TrafficInspectionEvent, TrafficInspectionRaw } from "@/types";
import { buildTrafficInspectionCommand, groupTrafficInspectionEvents } from "./traffic-inspection-model";

const queryLimit = 200;

export function TrafficInspectionView() {
  const { t } = useI18n();
  const [host, setHost] = useState("");
  const [path, setPath] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [events, setEvents] = useState<TrafficInspectionEvent[]>([]);
  const [expanded, setExpanded] = useState<string>();
  const [error, setError] = useState("");
  const exchanges = groupTrafficInspectionEvents(events);

  useEffect(() => {
    let active = true;
    const refresh = async () => {
      try {
        const result = await backend.trafficInspectionEvents({ host, path, limit: queryLimit });
        if (!active) return;
        setEnabled(result.enabled);
        setEvents(Array.isArray(result.events) ? result.events : []);
        setError("");
      } catch (reason) {
        if (active) setError(reason instanceof Error ? reason.message : String(reason));
      }
    };
    void refresh();
    const timer = window.setInterval(() => void refresh(), 1_000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [host, path]);

  return (
    <PageShell title={t("inspection.title")} description={t("inspection.description")}>
      <div className="mb-4 grid gap-3 sm:grid-cols-2">
        <Input
          value={host}
          onChange={(event) => setHost(event.target.value)}
          placeholder={t("inspection.hostPlaceholder")}
          aria-label={t("inspection.host")}
        />
        <Input
          value={path}
          onChange={(event) => setPath(event.target.value)}
          placeholder={t("inspection.pathPlaceholder")}
          aria-label={t("inspection.path")}
        />
      </div>
      {error ? <p className="mb-3 text-xs text-destructive">{t("inspection.loadFailed")}: {error}</p> : null}
      {!enabled ? (
        <EmptyState icon={FileSearch2} title={t("inspection.disabledTitle")} detail={t("inspection.disabledDetail")} />
      ) : events.length === 0 ? (
        <EmptyState icon={FileSearch2} title={t("inspection.emptyTitle")} detail={t("inspection.emptyDetail")} />
      ) : (
        <div className="overflow-auto rounded-lg border bg-card">
          <Table>
            <TableHeader>
              <TableRow className="bg-muted/50 hover:bg-muted/50">
                <TableHead className="w-8" />
                <TableHead>{t("inspection.time")}</TableHead>
                <TableHead>{t("inspection.protocol")}</TableHead>
                <TableHead>{t("inspection.type")}</TableHead>
                <TableHead>{t("inspection.host")}</TableHead>
                <TableHead>{t("inspection.path")}</TableHead>
                <TableHead>{t("inspection.status")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {exchanges.map((exchange) => {
                const event = exchange.summaryEvent;
                const isExpanded = expanded === exchange.flowId;
                return (
                  <Fragment key={exchange.flowId}>
                    <TableRow
                      className="cursor-pointer"
                      aria-expanded={isExpanded}
                      onClick={() => setExpanded(isExpanded ? undefined : exchange.flowId)}
                    >
                      <TableCell className="pr-0 text-muted-foreground">
                        {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                      </TableCell>
                      <TableCell className="whitespace-nowrap font-mono text-[11px] text-muted-foreground">
                        {formatTimestamp(exchange.firstEvent.timestamp)}
                      </TableCell>
                      <TableCell><Badge variant="outline">{event.protocol.toUpperCase()}</Badge></TableCell>
                      <TableCell className="font-mono text-[11px]">{eventMethod(event)}</TableCell>
                      <TableCell className="max-w-[220px] truncate font-mono text-[11px]">
                        {eventHost(event)}
                      </TableCell>
                      <TableCell className="max-w-[280px] truncate font-mono text-[11px] text-primary">
                        {eventPath(event)}
                      </TableCell>
                      <TableCell className="font-mono text-[11px]">{eventStatus(event)}</TableCell>
                    </TableRow>
                    {isExpanded ? (
                      <TableRow className="hover:bg-transparent">
                        <TableCell colSpan={7} className="bg-muted/20 p-0">
                          <CommandBlock events={exchange.events} />
                          <div className="grid gap-px bg-border lg:grid-cols-2">
                            <ExchangeSide
                              title={t("inspection.request")}
                              direction="request"
                              events={exchange.events}
                            />
                            <ExchangeSide
                              title={t("inspection.response")}
                              direction="response"
                              events={exchange.events}
                            />
                          </div>
                        </TableCell>
                      </TableRow>
                    ) : null}
                  </Fragment>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}
    </PageShell>
  );
}

function CommandBlock({ events }: { events: TrafficInspectionEvent[] }) {
  const { t } = useI18n();
  const command = buildTrafficInspectionCommand(events);
  if (!command) return null;
  const tool = command.startsWith("grpcurl ") ? "grpcurl" : "curl";

  async function copy() {
    try {
      await navigator.clipboard.writeText(command!);
      toast.success(t("inspection.commandCopied"));
    } catch {
      toast.error(t("inspection.commandCopyFailed"));
    }
  }

  return (
    <section className="border-b bg-card p-3">
      <div className="mb-2 flex items-center justify-between gap-3">
        <h3 className="text-[10px] font-semibold tracking-wide text-muted-foreground uppercase">{tool}</h3>
        <Button type="button" variant="outline" size="xs" onClick={() => void copy()}>
          <Copy data-icon="inline-start" />
          {t("inspection.copyCommand")}
        </Button>
      </div>
      <pre className="max-h-[180px] overflow-auto whitespace-pre-wrap break-all font-mono text-[11px] leading-5">{command}</pre>
    </section>
  );
}

function ExchangeSide({
  title,
  direction,
  events,
}: {
  title: string;
  direction: "request" | "response";
  events: TrafficInspectionEvent[];
}) {
  const { t } = useI18n();
  const phase = events.find((event) => event.type === direction);
  const body = findBodyEvent(events, direction);
  const raw = body?.raw ?? phase?.raw;

  return (
    <section className="min-w-0 bg-card">
      <h3 className="border-b px-3 py-2 text-xs font-semibold">{title}</h3>
      {body?.protobuf ? (
        <LogBlock
          title={`PROTOBUF · ${body.protobuf.schema}${body.protobuf.message_type ? ` · ${body.protobuf.message_type}` : ""}`}
          value={body.protobuf.error || body.protobuf.data || "{}"}
        />
      ) : null}
      <LogBlock
        title={`${t("inspection.raw")}${raw?.truncated ? ` · ${t("inspection.truncated")}` : ""}`}
        value={raw ? formatRaw(raw) : t("inspection.noRaw")}
      />
    </section>
  );
}

function LogBlock({ title, value }: { title: string; value: string }) {
  return (
    <section className="min-w-0 bg-card p-3">
      <h3 className="mb-2 text-[10px] font-semibold tracking-wide text-muted-foreground uppercase">{title}</h3>
      <pre className="max-h-[360px] overflow-auto whitespace-pre-wrap break-all font-mono text-[11px] leading-5">{value}</pre>
    </section>
  );
}

function findBodyEvent(events: TrafficInspectionEvent[], direction: "request" | "response") {
  for (let index = events.length - 1; index >= 0; index -= 1) {
    const event = events[index];
    if (event.type === "body" && event.raw?.direction === direction) return event;
  }
  return undefined;
}

function eventHost(event: TrafficInspectionEvent) {
  return event.http?.host || event.destination || "—";
}

function eventPath(event: TrafficInspectionEvent) {
  return event.http?.path || event.grpc?.path || "—";
}

function eventStatus(event: TrafficInspectionEvent) {
  return event.http?.status || event.grpc?.status || "—";
}

function eventMethod(event: TrafficInspectionEvent) {
  return event.http?.method || event.grpc?.method || "—";
}

function formatTimestamp(value: string) {
  const timestamp = new Date(value);
  return Number.isNaN(timestamp.getTime()) ? value : timestamp.toLocaleTimeString();
}

function formatRaw(raw: TrafficInspectionRaw) {
  if (raw.encoding !== "base64") return raw.data;
  try {
    const binary = window.atob(raw.data);
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
    if (raw.format === "http") return new TextDecoder().decode(bytes);
    return Array.from(bytes, (byte, index) => {
      const prefix = index > 0 && index % 16 === 0 ? "\n" : index > 0 ? " " : "";
      return `${prefix}${byte.toString(16).padStart(2, "0")}`;
    }).join("");
  } catch {
    return raw.data;
  }
}
