import { useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowDownToLine,
  Download,
  Search,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { PageShell } from "@/components/shared/page-shell";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Toggle } from "@/components/ui/toggle";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { useI18n } from "@/i18n";
import { phaseKeys } from "@/lib/phase";
import { cn } from "@/lib/utils";
import type { SessionState } from "@/types";

type DisplayLogLevel = "INFO" | "WARN" | "ERROR";
type LogLevelFilter = "ALL" | DisplayLogLevel;

type LogEntry = {
  time: string;
  level: DisplayLogLevel;
  text: string;
};

function displayLogLevel(level: string): DisplayLogLevel {
  if (level === "ERROR") return "ERROR";
  if (level === "WARN") return "WARN";
  return "INFO";
}

export function LogsView({
  session,
  error,
}: {
  session: SessionState;
  error: string;
}) {
  const { locale, t } = useI18n();
  const [query, setQuery] = useState("");
  const [level, setLevel] = useState<LogLevelFilter>("ALL");
  const [autoScroll, setAutoScroll] = useState(true);
  const [clearedThrough, setClearedThrough] = useState(0);
  const scrollAreaRef = useRef<HTMLDivElement>(null);

  const lines = useMemo<LogEntry[]>(() => {
    const entries = (session.events ?? []).map((event) => ({
      time: new Date(event.time).toLocaleTimeString(locale, { hour12: false }),
      level: displayLogLevel(event.level),
      text: event.message,
    }));

    if (entries.length > 0) return entries;

    const time = new Date(session.updatedAt).toLocaleTimeString(locale, {
      hour12: false,
    });
    entries.push({
      time,
      level: error ? "ERROR" : "INFO",
      text: error || t(phaseKeys[session.phase]),
    });
    if (session.discovery) {
      entries.push(
        {
          time,
          level: "INFO",
          text: t("logs.podCIDRsFound", {
            count: session.discovery.podCIDRs.length,
          }),
        },
        {
          time,
          level: "INFO",
          text: t("logs.servicesFound", {
            count: session.discovery.serviceIPs.length,
          }),
        },
        {
          time,
          level: "INFO",
          text: `CoreDNS ${session.discovery.dnsServer || t("network.notFound")}`,
        },
      );
    }
    return entries;
  }, [error, locale, session, t]);

  useEffect(() => {
    if (clearedThrough > lines.length) setClearedThrough(0);
  }, [clearedThrough, lines.length]);

  const visibleLines = lines.slice(clearedThrough);
  const levelCounts = useMemo(
    () =>
      visibleLines.reduce(
        (counts, line) => {
          counts[line.level] += 1;
          return counts;
        },
        { INFO: 0, WARN: 0, ERROR: 0 },
      ),
    [visibleLines],
  );
  const filteredLines = useMemo(() => {
    const normalizedQuery = query.trim().toLocaleLowerCase(locale);
    return visibleLines.filter(
      (line) =>
        (level === "ALL" || line.level === level) &&
        (!normalizedQuery ||
          line.text.toLocaleLowerCase(locale).includes(normalizedQuery)),
    );
  }, [level, locale, query, visibleLines]);

  useEffect(() => {
    if (!autoScroll) return;
    const viewport = scrollAreaRef.current?.querySelector<HTMLElement>(
      '[data-slot="scroll-area-viewport"]',
    );
    if (viewport) viewport.scrollTop = viewport.scrollHeight;
  }, [autoScroll, filteredLines.length]);

  const payload = filteredLines
    .map((line) => `${line.time} ${line.level} ${line.text}`)
    .join("\n");

  function downloadDiagnostics() {
    const blob = new Blob([payload], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `kube-loop-${new Date().toISOString().replaceAll(":", "-")}.log`;
    anchor.click();
    URL.revokeObjectURL(url);
    toast.success(t("logs.downloadSuccess"));
  }

  function clearLogs() {
    setClearedThrough(lines.length);
    toast.success(t("logs.cleared"));
  }

  return (
    <PageShell
      title={t("logs.title")}
      description={t("logs.description")}
      action={
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={filteredLines.length === 0}
          onClick={downloadDiagnostics}
        >
          <Download data-icon="inline-start" />
          {t("logs.download")}
        </Button>
      }
    >
      <div className="overflow-hidden rounded-lg border bg-card">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b bg-muted/20 p-2">
          <Tabs
            value={level}
            onValueChange={(value) => setLevel(value as LogLevelFilter)}
          >
            <TabsList className="h-8">
              <TabsTrigger value="ALL" className="h-6 px-2">
                {t("logs.all")}
                <span className="text-muted-foreground">{visibleLines.length}</span>
              </TabsTrigger>
              <TabsTrigger value="INFO" className="h-6 px-2">
                Info
                <span className="text-muted-foreground">{levelCounts.INFO}</span>
              </TabsTrigger>
              <TabsTrigger value="WARN" className="h-6 px-2">
                Warn
                <span className="text-muted-foreground">{levelCounts.WARN}</span>
              </TabsTrigger>
              <TabsTrigger value="ERROR" className="h-6 px-2">
                Error
                <span className="text-muted-foreground">{levelCounts.ERROR}</span>
              </TabsTrigger>
            </TabsList>
          </Tabs>

          <div className="flex min-w-0 items-center gap-1.5">
            <div className="relative w-52 min-w-32">
              <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder={t("logs.search")}
                aria-label={t("logs.search")}
                className="h-7 pl-8 text-xs"
              />
            </div>
            <Tooltip>
              <TooltipTrigger asChild>
                <Toggle
                  pressed={autoScroll}
                  onPressedChange={setAutoScroll}
                  variant="outline"
                  size="sm"
                  aria-label={t("logs.autoScroll")}
                >
                  <ArrowDownToLine />
                </Toggle>
              </TooltipTrigger>
              <TooltipContent>{t("logs.autoScroll")}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  disabled={visibleLines.length === 0}
                  aria-label={t("logs.clear")}
                  onClick={clearLogs}
                >
                  <Trash2 />
                </Button>
              </TooltipTrigger>
              <TooltipContent>{t("logs.clear")}</TooltipContent>
            </Tooltip>
          </div>
        </div>

        <ScrollArea
          ref={scrollAreaRef}
          className="h-[min(56vh,440px)] min-h-72 bg-[var(--console)]"
        >
          <div className="min-w-[560px] font-mono text-[11px]">
            {filteredLines.length > 0 ? (
              filteredLines.map((line, index) => (
                <LogLine key={`${line.time}-${line.text}-${index}`} {...line} />
              ))
            ) : (
              <div className="flex h-56 items-center justify-center text-muted-foreground">
                {visibleLines.length === 0
                  ? t("logs.empty")
                  : t("logs.noMatches")}
              </div>
            )}
          </div>
        </ScrollArea>
      </div>
    </PageShell>
  );
}

function LogLine({
  time,
  level,
  text,
}: {
  time: string;
  level: DisplayLogLevel;
  text: string;
}) {
  return (
    <div className="grid grid-cols-[76px_64px_1fr] items-start border-b px-3 py-2.5 transition-colors last:border-0 hover:bg-muted/30">
      <span className="text-muted-foreground">{time}</span>
      <Badge
        variant={level === "ERROR" ? "destructive" : "outline"}
        className={cn(
          "h-4 px-1.5 font-mono text-[9px] leading-none",
          level === "WARN" &&
            "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400",
          level === "INFO" && "text-primary",
        )}
      >
        {level}
      </Badge>
      <span className="break-words whitespace-pre-wrap text-foreground/80">
        {text}
      </span>
    </div>
  );
}
