import { useCallback, useEffect, useMemo, useState } from "react";
import { Pause, Play, RefreshCw, Trash2 } from "lucide-react";
import { backend } from "@/backend";
import { ActionIconButton } from "@/components/network/action-icons";
import { PageShell } from "@/components/shared/page-shell";
import { ResourcePagination, RESOURCE_PAGE_SIZE } from "@/components/shared/resource-pagination";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useI18n } from "@/i18n";
import type {
  RemoteInventory,
  ServerTrafficBindingSession,
} from "@/types";

type Action = "port-forward" | "exchange" | "mirror" | "preview";
type SessionRow = { key: string; id: string; kind: Action; target: string; detail?: string; state: string };

export function SessionsView({ profileId }: { profileId: string }) {
  const { t } = useI18n();
  const [inventory, setInventory] = useState<RemoteInventory>();
  const [sessions, setSessions] = useState<ServerTrafficBindingSession[]>([]);
  const [namespace, setNamespace] = useState("");
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [busyTasks, setBusyTasks] = useState<Set<string>>(() => new Set());
  const [error, setError] = useState("");

  const load = useCallback(async (nextNamespace: string) => {
    if (!profileId) {
      setInventory(undefined);
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const next = await backend.loadServerInventory(profileId, nextNamespace);
      const nextSessions = await backend.listServerSessions(profileId);
      setInventory(next);
      setNamespace(next.namespace ?? "");
      setSessions(nextSessions);
      setError("");
    } catch (reason) {
      setError(messageOf(reason));
    } finally {
      setLoading(false);
    }
  }, [profileId]);

  useEffect(() => {
    setInventory(undefined);
    setNamespace("");
    void load("");
  }, [load]);

  const rows = useMemo(() => {
    const list: SessionRow[] = sessions.map(sessionRow);
    const normalized = query.trim().toLocaleLowerCase();
    return normalized
      ? list.filter((row) => [labelFor(row.kind), row.target, row.detail, row.state].some((value) => String(value).toLocaleLowerCase().includes(normalized)))
      : list;
  }, [sessions, query]);

  const pageCount = Math.max(1, Math.ceil(rows.length / RESOURCE_PAGE_SIZE));
  const visibleRows = rows.slice((page - 1) * RESOURCE_PAGE_SIZE, page * RESOURCE_PAGE_SIZE);

  useEffect(() => setPage(1), [query, namespace]);
  useEffect(() => setPage((current) => Math.min(current, pageCount)), [pageCount]);

  async function mutateTask(operation: "pause" | "resume" | "delete", kind: Action, id: string) {
    const key = `${kind}:${id}`;
    if (!profileId || busyTasks.has(key)) return;
    setBusyTasks((current) => new Set(current).add(key));
    setError("");
    try {
      if (kind === "port-forward") {
        await mutateSession(operation, {
          pause: () => backend.pauseServerPortForward(profileId, id),
          resume: () => backend.resumeServerPortForward(profileId, id),
          delete: () => backend.deleteServerPortForward(profileId, id),
        });
      } else if (kind === "exchange") {
        await mutateSession(operation, {
          pause: () => backend.pauseServerExchange(profileId, id),
          resume: () => backend.resumeServerExchange(profileId, id),
          delete: () => backend.deleteServerExchange(profileId, id),
        });
      } else if (kind === "mirror") {
        await mutateSession(operation, {
          pause: () => backend.pauseServerMirror(profileId, id),
          resume: () => backend.resumeServerMirror(profileId, id),
          delete: () => backend.deleteServerMirror(profileId, id),
        });
      } else {
        await mutateSession(operation, {
          pause: () => backend.pauseServerPreview(profileId, id),
          resume: () => backend.resumeServerPreview(profileId, id),
          delete: () => backend.deleteServerPreview(profileId, id),
        });
      }
      setSessions(await backend.listServerSessions(profileId));
    } catch (reason) {
      setError(messageOf(reason));
    } finally {
      setBusyTasks((current) => {
        const next = new Set(current);
        next.delete(key);
        return next;
      });
    }
  }

  return (
    <PageShell title={t("nav.sessions")} description={t("header.sessions")}>
      <div className="mb-4 flex flex-nowrap items-center gap-2">
        <select className="h-9 w-[180px] shrink-0 rounded-md border border-input bg-background px-3 text-sm" value={namespace} disabled={!inventory || loading} onChange={(event) => void load(event.target.value)}>
          {!inventory ? <option value="" disabled>Loading…</option> : null}
          {(inventory?.namespaces ?? []).map((item) => <option key={item.name} value={item.name}>{item.name}</option>)}
        </select>
        <Input className="min-w-0 flex-1" value={query} placeholder="Search sessions" onChange={(event) => setQuery(event.target.value)} />
        <Button className="shrink-0 whitespace-nowrap" type="button" variant="outline" size="sm" disabled={!profileId || loading} onClick={() => void load(namespace)}>{loading ? <Spinner data-icon="inline-start" /> : <RefreshCw size={14} />}Refresh</Button>
      </div>

      {!profileId || (!inventory && !loading) ? <p className="rounded-md border bg-card p-8 text-center text-sm text-muted-foreground">Select and sign in to a Server first.</p> : <div className="space-y-5">
        {error && !loading ? <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</p> : null}

        <div className="overflow-hidden rounded-lg border bg-card">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Type</TableHead>
                <TableHead>Target</TableHead>
                <TableHead>Address / Target</TableHead>
                <TableHead>State</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading ? (
                <TableRow className="hover:bg-transparent"><TableCell colSpan={5} className="h-32 text-center text-[12px] text-muted-foreground"><Spinner className="mx-auto" /></TableCell></TableRow>
              ) : rows.length === 0 ? (
                <TableRow className="hover:bg-transparent"><TableCell colSpan={5} className="h-32 text-center text-[12px] text-muted-foreground">No active sessions.</TableCell></TableRow>
              ) : (
                visibleRows.map((row) => <OperationRow key={row.key} kind={row.kind} id={row.id} target={row.target} detail={row.detail} state={row.state} busy={busyTasks.has(row.key)} onMutate={mutateTask} />)
              )}
            </TableBody>
          </Table>
          <ResourcePagination page={page} total={rows.length} showWhenEmpty onPageChange={setPage} />
        </div>
      </div>}
    </PageShell>
  );
}

function OperationRow({ kind, id, target, detail, state, busy, onMutate }: { kind: Action; id: string; target: string; detail?: string; state: string; busy: boolean; onMutate(operation: "pause" | "resume" | "delete", kind: Action, id: string): void }) {
  const paused = state === "paused" || state === "stopped";
  return (
    <TableRow>
      <TableCell><Badge variant="outline">{labelFor(kind)}</Badge></TableCell>
      <TableCell>{target}</TableCell>
      <TableCell className="font-mono text-xs">{detail || "—"}</TableCell>
      <TableCell>{state}</TableCell>
      <TableCell>
        <div className="flex justify-end gap-1">
          {paused ? (
            <ActionIconButton label="Resume" icon={Play} disabled={busy} onClick={() => onMutate("resume", kind, id)} />
          ) : (
            <ActionIconButton label="Pause" icon={Pause} disabled={busy} onClick={() => onMutate("pause", kind, id)} />
          )}
          <ActionIconButton label="Delete" icon={Trash2} disabled={busy} onClick={() => onMutate("delete", kind, id)} />
        </div>
      </TableCell>
    </TableRow>
  );
}

function sessionRow(item: ServerTrafficBindingSession): SessionRow {
  const kind = actionForMode(item.mode);
  const port = item.ports[0];
  const target = item.mode === "Preview"
    ? `${item.namespace}/${item.preview?.serviceName ?? item.serviceName ?? item.name}`
    : item.mode === "PortForward"
      ? `${item.target?.kind?.toLocaleLowerCase() ?? "target"}/${item.target?.name ?? "—"}:${port?.targetPort ?? "—"}`
      : item.target?.name ?? item.serviceName ?? item.name;
  const mappings = item.ports.map((entry) => entry.relayPort
    ? `${entry.targetPort} ↔ ${entry.relayPort}/${entry.protocol.toUpperCase()}`
    : `${entry.targetPort}/${entry.protocol.toUpperCase()}`).join(", ");
  const detail = [item.serviceClusterIp, item.relay?.address, mappings].filter(Boolean).join(" · ");
  return { key: `${kind}:${item.id}`, id: item.id, kind, target, detail, state: stateForBinding(item) };
}

function actionForMode(mode: ServerTrafficBindingSession["mode"]): Action {
  return mode === "PortForward" ? "port-forward" : mode.toLocaleLowerCase() as Action;
}

function stateForBinding(item: ServerTrafficBindingSession) {
  if (item.desiredState === "Paused") return "paused";
  if (item.phase === "Ready") return "running";
  return item.phase ? item.phase.toLocaleLowerCase() : "pending";
}
function labelFor(action: Action) { return action === "port-forward" ? "Port Forward" : action[0]!.toUpperCase() + action.slice(1); }
function messageOf(reason: unknown) { return reason instanceof Error ? reason.message : String(reason); }

type TaskMutation = {
  pause(): Promise<void>;
  resume(): Promise<unknown>;
  delete(): Promise<void>;
};

async function mutateSession(
  operation: "pause" | "resume" | "delete",
  mutation: TaskMutation,
) {
  if (operation === "delete") await mutation.delete();
  else if (operation === "pause") await mutation.pause();
  else await mutation.resume();
}
