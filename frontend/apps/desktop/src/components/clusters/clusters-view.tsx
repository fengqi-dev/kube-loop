import { useEffect, useRef, useState } from "react";
import {
  ClipboardPaste,
  FilePlus2,
  MoreHorizontal,
  Power,
  RefreshCw,
  Trash2,
  Wifi,
  WifiOff,
} from "lucide-react";
import { ClipboardGetText } from "../../../../../wailsjs/runtime/runtime";
import { toast } from "sonner";
import { EmptyState } from "@/components/shared/empty-state";
import { PageShell } from "@/components/shared/page-shell";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Spinner } from "@/components/ui/spinner";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useI18n } from "@/i18n";
import { cn } from "@/lib/utils";
import type {
  ClusterInventory,
  ContextInfo,
  KubeconfigFileInfo,
  ProbeResult,
  SessionState,
} from "@/types";

type ProbeState = ProbeResult & { probing?: boolean };

const PROBE_CONCURRENCY = 3;

export function ClustersView({
  contexts,
  files,
  contextName,
  session,
  busy,
  ready,
  loading,
  onSelect,
  onConnect,
  onReload,
  onAddFile,
  onAddClipboard,
  onRemoveFile,
  onProbe,
}: {
  contexts: ContextInfo[];
  files: KubeconfigFileInfo[];
  contextName: string;
  session: SessionState;
  busy: boolean;
  ready: boolean;
  loading: boolean;
  onSelect(name: string): void;
  onConnect(name: string): void;
  onReload(): Promise<ClusterInventory>;
  onAddFile(): Promise<ClusterInventory>;
  onAddClipboard(content: string): Promise<ClusterInventory>;
  onRemoveFile(path: string): Promise<ClusterInventory>;
  onProbe(name: string): Promise<ProbeResult>;
}) {
  const { t } = useI18n();
  const [operation, setOperation] = useState<
    { kind: "reload" | "add" | "paste" | "remove"; target?: string } | undefined
  >();
  const operationActive = useRef(false);
  const [probes, setProbes] = useState<Record<string, ProbeState>>({});
  const probeGen = useRef(0);
  const probeRequests = useRef<Record<string, number>>({});
  const refreshing = operation?.kind === "reload";
  const adding = operation?.kind === "add";
  const pasting = operation?.kind === "paste";
  const removing = operation?.kind === "remove" ? operation.target ?? "" : "";

  function beginOperation(
    kind: "reload" | "add" | "paste" | "remove",
    target?: string,
  ) {
    if (operationActive.current) return false;
    operationActive.current = true;
    setOperation({ kind, target });
    return true;
  }

  function finishOperation() {
    operationActive.current = false;
    setOperation(undefined);
  }

  async function runProbes(items: ContextInfo[]) {
    const gen = ++probeGen.current;
    setProbes((current) => {
      const next: Record<string, ProbeState> = {};
      for (const item of items) {
        next[item.name] = { context: item.name, ok: false, probing: true };
      }
      return next;
    });
    let index = 0;
    async function worker() {
      while (index < items.length) {
        const item = items[index++];
        if (!item || gen !== probeGen.current) return;
        try {
          const result = await onProbe(item.name);
          if (gen !== probeGen.current) return;
          setProbes((current) => ({
            ...current,
            [item.name]: { ...result, probing: false },
          }));
        } catch (error) {
          if (gen !== probeGen.current) return;
          setProbes((current) => ({
            ...current,
            [item.name]: {
              context: item.name,
              ok: false,
              probing: false,
              error: error instanceof Error ? error.message : String(error),
            },
          }));
        }
      }
    }
    await Promise.all(
      Array.from({ length: Math.min(PROBE_CONCURRENCY, items.length) }, () => worker()),
    );
  }

  useEffect(() => {
    if (loading || contexts.length === 0) return;
    void runProbes(contexts);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- probe on context list identity
  }, [contexts, loading]);

  async function handleReload() {
    if (!beginOperation("reload")) return;
    try {
      const inventory = await onReload();
      await runProbes(inventory.contexts);
    } catch (error) {
      toast.error(t("clusters.reloadFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      finishOperation();
    }
  }

  async function handleAdd() {
    if (!beginOperation("add")) return;
    try {
      const inventory = await onAddFile();
      await runProbes(inventory.contexts);
    } catch (error) {
      toast.error(t("clusters.addFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      finishOperation();
    }
  }

  async function handlePaste() {
    if (!beginOperation("paste")) return;
    try {
      let content = "";
      try {
        content = await ClipboardGetText();
      } catch {
        content = await navigator.clipboard.readText();
      }
      const inventory = await onAddClipboard(content);
      await runProbes(inventory.contexts);
      toast.success(t("clusters.pasteDone"));
    } catch (error) {
      toast.error(t("clusters.pasteFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      finishOperation();
    }
  }

  async function handleRemove(path: string) {
    if (!beginOperation("remove", path)) return;
    try {
      const inventory = await onRemoveFile(path);
      await runProbes(inventory.contexts);
    } catch (error) {
      toast.error(t("clusters.removeFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      finishOperation();
    }
  }

  async function handleProbeOne(name: string) {
    const generation = probeGen.current;
    const request = (probeRequests.current[name] ?? 0) + 1;
    probeRequests.current[name] = request;
    setProbes((current) => ({
      ...current,
      [name]: { ...(current[name] ?? { context: name, ok: false }), probing: true },
    }));
    try {
      const result = await onProbe(name);
      if (
        generation !== probeGen.current ||
        probeRequests.current[name] !== request
      ) {
        return;
      }
      setProbes((current) => ({ ...current, [name]: { ...result, probing: false } }));
    } catch (error) {
      if (
        generation !== probeGen.current ||
        probeRequests.current[name] !== request
      ) {
        return;
      }
      setProbes((current) => ({
        ...current,
        [name]: {
          context: name,
          ok: false,
          probing: false,
          error: error instanceof Error ? error.message : String(error),
        },
      }));
    }
  }

  function actionLabel(name: string) {
    if (busy && session.context === name) return t("overview.cancel");
    if (ready && session.context === name) return t("overview.disconnect");
    if (ready) return t("clusters.switch");
    return t("clusters.connect");
  }

  return (
    <PageShell
      title={t("clusters.title")}
      description={t("clusters.description")}
      action={
        <div className="flex items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={Boolean(operation) || loading}
            onClick={() => void handleReload()}
          >
            {refreshing ? (
              <Spinner data-icon="inline-start" />
            ) : (
              <RefreshCw size={14} data-icon="inline-start" />
            )}
            {t("clusters.refresh")}
          </Button>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={Boolean(operation) || loading}
            onClick={() => void handlePaste()}
          >
            {pasting ? (
              <Spinner data-icon="inline-start" />
            ) : (
              <ClipboardPaste size={14} data-icon="inline-start" />
            )}
            {t("clusters.paste")}
          </Button>
          <Button
            type="button"
            size="sm"
            disabled={Boolean(operation) || loading}
            onClick={() => void handleAdd()}
          >
            {adding ? (
              <Spinner data-icon="inline-start" />
            ) : (
              <FilePlus2 size={14} data-icon="inline-start" />
            )}
            {t("clusters.addFile")}
          </Button>
        </div>
      }
    >
      {loading ? (
        <div className="grid min-h-[240px] place-items-center text-sm text-muted-foreground">
          <Spinner />
        </div>
      ) : contexts.length === 0 ? (
        <EmptyState
          icon={WifiOff}
          title={t("clusters.emptyTitle")}
          detail={t("clusters.emptyDetail")}
        />
      ) : (
        <Card className="gap-0 overflow-hidden py-0 shadow-none">
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>{t("clusters.colContext")}</TableHead>
                <TableHead>{t("clusters.colCluster")}</TableHead>
                <TableHead className="hidden lg:table-cell">{t("clusters.colServer")}</TableHead>
                <TableHead className="hidden md:table-cell">{t("clusters.colUser")}</TableHead>
                <TableHead>{t("clusters.colSource")}</TableHead>
                <TableHead>{t("clusters.colStatus")}</TableHead>
                <TableHead className="w-[148px] text-right">
                  {t("clusters.colActions")}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {contexts.map((item) => {
                const selected = item.name === contextName;
                const connectedHere = ready && session.context === item.name;
                const probe = probes[item.name];
                const source = files.find((file) => file.path === item.source);
                return (
                  <TableRow
                    key={item.name}
                    className={cn(selected && "bg-muted/50")}
                    onClick={() => onSelect(item.name)}
                  >
                    <TableCell className="font-medium">
                      <div className="flex items-center gap-2">
                        <span
                          className={cn("truncate", source?.default && "text-primary")}
                          title={source?.default ? t("clusters.defaultFile") : undefined}
                        >
                          {item.name}
                        </span>
                        {connectedHere ? (
                          <Badge className="rounded-md bg-success/15 text-[10px] text-success">
                            {t("phase.connected")}
                          </Badge>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell className="max-w-[140px] truncate text-muted-foreground">
                      {item.cluster || "—"}
                    </TableCell>
                    <TableCell className="hidden max-w-[180px] truncate font-mono text-[11px] text-muted-foreground lg:table-cell">
                      {item.server || "—"}
                    </TableCell>
                    <TableCell className="hidden max-w-[120px] truncate text-muted-foreground md:table-cell">
                      {item.user || "—"}
                    </TableCell>
                    <TableCell className="max-w-[260px]">
                      <div className="flex min-w-0 items-center gap-1.5">
                        <span
                          className="min-w-0 truncate font-mono text-[11px] text-muted-foreground"
                          title={item.source}
                        >
                          {item.source || "—"}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell>
                      <ProbeBadge probe={probe} />
                    </TableCell>
                    <TableCell className="w-[148px] text-right">
                      <div className="grid grid-cols-[1.75rem_7rem] items-center justify-end gap-1">
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon"
                              className="size-7"
                              disabled={Boolean(operation)}
                              onClick={(event) => event.stopPropagation()}
                              aria-label={t("clusters.colActions")}
                            >
                              {probe?.probing || (source && removing === source.path) ? (
                                <Spinner />
                              ) : (
                                <MoreHorizontal size={15} />
                              )}
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end" onClick={(event) => event.stopPropagation()}>
                            <DropdownMenuItem
                              disabled={probe?.probing}
                              onSelect={() => void handleProbeOne(item.name)}
                            >
                              <Wifi size={14} />
                              {t("clusters.probe")}
                            </DropdownMenuItem>
                            {source && !source.default ? (
                              <>
                                <DropdownMenuSeparator />
                                <DropdownMenuItem
                                  variant="destructive"
                                  disabled={Boolean(operation)}
                                  onSelect={() => void handleRemove(source.path)}
                                >
                                  <Trash2 size={14} />
                                  {t("clusters.removeFile")}
                                </DropdownMenuItem>
                              </>
                            ) : null}
                          </DropdownMenuContent>
                        </DropdownMenu>
                        <Button
                          type="button"
                          variant={connectedHere ? "outline" : "default"}
                          size="sm"
                          className="w-full justify-center"
                          disabled={busy && session.context !== item.name}
                          onClick={(event) => {
                            event.stopPropagation();
                            onConnect(item.name);
                          }}
                        >
                          <Power size={13} data-icon="inline-start" />
                          {actionLabel(item.name)}
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </Card>
      )}
    </PageShell>
  );
}

function ProbeBadge({ probe }: { probe?: ProbeState }) {
  const { t } = useI18n();
  if (!probe || probe.probing) {
    return (
      <span className="inline-flex items-center gap-1 text-[11px] text-muted-foreground">
        <Spinner />
        {t("clusters.probing")}
      </span>
    );
  }
  if (probe.ok) {
    return (
      <span className="text-[11px] text-success">
        {t("clusters.reachable")}
        {probe.latencyMs != null ? ` · ${probe.latencyMs}ms` : ""}
      </span>
    );
  }
  return (
    <span className="max-w-[160px] truncate text-[11px] text-destructive" title={probe.error}>
      {t("clusters.unreachable")}
    </span>
  );
}
