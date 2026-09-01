import { useCallback, useEffect, useMemo, useState } from "react";
import { ArrowDown, ArrowUpDown, Copy, Pause, Play, RefreshCw, Trash2 } from "lucide-react";
import { backend } from "@/backend";
import { ActionIconButton } from "@/components/network/action-icons";
import {
  sessionTargetType,
  targetWithPorts,
  targetTypeLabel,
  trafficBindingDetails,
  type SessionTargetType,
  type TrafficEndpointPair,
} from "@/components/sessions/session-row-model";
import { PageShell } from "@/components/shared/page-shell";
import { CopyableText } from "@/components/shared/copyable-text";
import { ResourcePagination, RESOURCE_PAGE_SIZE } from "@/components/shared/resource-pagination";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useI18n } from "@/i18n";
import type {
  RemoteInventory,
  ServerExchangeInfo,
  ServerMirrorInfo,
  ServerPortForwardInfo,
  ServerPreviewInfo,
  ServerTrafficBindingSession,
} from "@/types";

type Action = "port-forward" | "exchange" | "mirror" | "preview";
type SessionRow = { key: string; id: string; kind: Action; targetType: SessionTargetType; target: string; details: TrafficEndpointPair[]; state: string; managedLocally: boolean };

export function SessionsView({ profileId }: { profileId: string }) {
  const { t } = useI18n();
  const [inventory, setInventory] = useState<RemoteInventory>();
  const [sessions, setSessions] = useState<ServerTrafficBindingSession[]>([]);
  const [portForwards, setPortForwards] = useState<ServerPortForwardInfo[]>([]);
  const [exchanges, setExchanges] = useState<ServerExchangeInfo[]>([]);
  const [mirrors, setMirrors] = useState<ServerMirrorInfo[]>([]);
  const [previews, setPreviews] = useState<ServerPreviewInfo[]>([]);
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
      setInventory(next);
      setNamespace(next.namespace ?? "");
      const results = await Promise.allSettled([
        backend.listServerSessions(profileId),
        backend.listServerPortForwards(profileId),
        backend.listServerExchanges(profileId),
        backend.listServerMirrors(profileId),
        backend.listServerPreviews(profileId),
      ]);
      if (results[0].status === "fulfilled") setSessions(results[0].value);
      if (results[1].status === "fulfilled") setPortForwards(results[1].value);
      if (results[2].status === "fulfilled") setExchanges(results[2].value);
      if (results[3].status === "fulfilled") setMirrors(results[3].value);
      if (results[4].status === "fulfilled") setPreviews(results[4].value);
      setError(results
        .filter((result) => result.status === "rejected")
        .map((result) => messageOf(result.reason))
        .join("\n"));
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
    const localPortForwards = new Map(portForwards.map((item) => [item.id, item]));
    const localIDs = new Set([
      ...portForwards.map((item) => item.id),
      ...exchanges.map((item) => item.id),
      ...mirrors.map((item) => item.id),
      ...previews.map((item) => item.id),
    ]);
    const list: SessionRow[] = sessions.map((item) =>
      sessionRow(item, localPortForwards.get(item.id), localIDs.has(item.id)));
    const remoteIDs = new Set(sessions.map((item) => item.id));
    list.push(
      ...portForwards.filter((item) => !remoteIDs.has(item.id)).map(portForwardRow),
      ...exchanges.filter((item) => !remoteIDs.has(item.id)).map((item) => localTargetRow("exchange", item)),
      ...mirrors.filter((item) => !remoteIDs.has(item.id)).map((item) => localTargetRow("mirror", item)),
      ...previews.filter((item) => !remoteIDs.has(item.id)).map(previewRow),
    );
    const normalized = query.trim().toLocaleLowerCase();
    return normalized
      ? list.filter((row) => [labelFor(row.kind), row.targetType, row.target, row.details.flatMap((detail) => [detail.local, detail.remote]).join(" "), row.state].some((value) => String(value).toLocaleLowerCase().includes(normalized)))
      : list;
  }, [exchanges, mirrors, portForwards, previews, sessions, query]);

  const pageCount = Math.max(1, Math.ceil(rows.length / RESOURCE_PAGE_SIZE));
  const visibleRows = rows.slice((page - 1) * RESOURCE_PAGE_SIZE, page * RESOURCE_PAGE_SIZE);

  useEffect(() => setPage(1), [query, namespace]);
  useEffect(() => setPage((current) => Math.min(current, pageCount)), [pageCount]);

  async function mutateTask(operation: "pause" | "resume" | "delete", kind: Action, id: string, managedLocally: boolean) {
    const key = `${kind}:${id}`;
    if (!profileId || busyTasks.has(key)) return;
    setBusyTasks((current) => new Set(current).add(key));
    setError("");
    try {
      if (operation === "delete" && !managedLocally) {
        await backend.deleteServerTrafficBinding(profileId, id);
      } else if (kind === "port-forward") {
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
      const results = await Promise.allSettled([
        backend.listServerSessions(profileId),
        backend.listServerPortForwards(profileId),
        backend.listServerExchanges(profileId),
        backend.listServerMirrors(profileId),
        backend.listServerPreviews(profileId),
      ]);
      if (results[0].status === "fulfilled") setSessions(results[0].value);
      if (results[1].status === "fulfilled") setPortForwards(results[1].value);
      if (results[2].status === "fulfilled") setExchanges(results[2].value);
      if (results[3].status === "fulfilled") setMirrors(results[3].value);
      if (results[4].status === "fulfilled") setPreviews(results[4].value);
      setError(results
        .filter((result) => result.status === "rejected")
        .map((result) => messageOf(result.reason))
        .join("\n"));
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
                <TableHead>Target Type</TableHead>
                <TableHead>Target</TableHead>
                <TableHead>Address / Target</TableHead>
                <TableHead>State</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading ? (
                <TableRow className="hover:bg-transparent"><TableCell colSpan={6} className="h-32 text-center text-[12px] text-muted-foreground"><Spinner className="mx-auto" /></TableCell></TableRow>
              ) : rows.length === 0 ? (
                <TableRow className="hover:bg-transparent"><TableCell colSpan={6} className="h-32 text-center text-[12px] text-muted-foreground">No active sessions.</TableCell></TableRow>
              ) : (
                visibleRows.map((row) => <OperationRow key={row.key} kind={row.kind} id={row.id} targetType={row.targetType} target={row.target} details={row.details} state={row.state} managedLocally={row.managedLocally} busy={busyTasks.has(row.key)} onMutate={mutateTask} />)
              )}
            </TableBody>
          </Table>
          <ResourcePagination page={page} total={rows.length} showWhenEmpty onPageChange={setPage} />
        </div>
      </div>}
    </PageShell>
  );
}

function OperationRow({ kind, id, targetType, target, details, state, managedLocally, busy, onMutate }: { kind: Action; id: string; targetType: SessionTargetType; target: string; details: TrafficEndpointPair[]; state: string; managedLocally: boolean; busy: boolean; onMutate(operation: "pause" | "resume" | "delete", kind: Action, id: string, managedLocally: boolean): void }) {
  const paused = state === "paused" || state === "stopped";
  return (
    <TableRow>
      <TableCell><Badge variant="outline">{labelFor(kind)}</Badge></TableCell>
      <TableCell>{targetType}</TableCell>
      <TableCell>{target}</TableCell>
      <TableCell className="font-mono text-xs">
        {details.length > 0
            ? <div className="space-y-3 whitespace-normal break-all">{details.map((detail, index) => (
              <div key={`${detail.local}:${detail.remote}:${index}`} className="inline-grid max-w-full justify-items-start">
                <Endpoint value={detail.flow === "remote-to-local" ? detail.remote : detail.local} />
                {detail.flow === "remote-to-local"
                  ? <ArrowDown className="my-0.5 size-3.5 justify-self-center text-muted-foreground" aria-label="Cluster to local traffic" />
                  : <ArrowUpDown className="my-0.5 size-3.5 justify-self-center text-muted-foreground" aria-label="Bidirectional traffic" />}
                <Endpoint value={detail.flow === "remote-to-local" ? detail.local : detail.remote} emphasized />
              </div>
            ))}</div>
          : "—"}
      </TableCell>
      <TableCell>{state}</TableCell>
      <TableCell>
        <div className="flex justify-end gap-1">
          {paused ? (
            <ActionIconButton label="Resume" icon={Play} disabled={busy} onClick={() => onMutate("resume", kind, id, managedLocally)} />
          ) : (
            <ActionIconButton label="Pause" icon={Pause} disabled={busy} onClick={() => onMutate("pause", kind, id, managedLocally)} />
          )}
          <ActionIconButton label="Delete" icon={Trash2} disabled={busy} onClick={() => onMutate("delete", kind, id, managedLocally)} />
        </div>
      </TableCell>
    </TableRow>
  );
}

function Endpoint({ value, emphasized = false }: { value: string; emphasized?: boolean }) {
  if (value === "—") return <span className={emphasized ? "font-semibold text-foreground" : undefined}>—</span>;
  return (
    <CopyableText
      value={value}
      label={<span className="inline-flex items-center gap-1"><span>{value}</span><Copy className="size-3 text-muted-foreground" aria-hidden="true" /></span>}
      className={emphasized ? "font-semibold text-foreground" : undefined}
      titleKey="network.copyAddress"
      successKey="network.addressCopied"
      failKey="network.addressCopyFailed"
    />
  );
}

function sessionRow(item: ServerTrafficBindingSession, localPortForward: ServerPortForwardInfo | undefined, managedLocally: boolean): SessionRow {
  const kind = actionForMode(item.mode);
  const targetName = item.mode === "Preview"
    ? item.preview?.serviceName ?? item.serviceName ?? item.name
    : item.mode === "PortForward"
      ? item.target?.name ?? "—"
      : item.target?.name ?? item.serviceName ?? item.name;
  return {
    key: `${kind}:${item.id}`,
    id: item.id,
    kind,
    targetType: sessionTargetType(item, localPortForward),
    target: targetWithPorts(targetName, item.ports.map((port) => port.targetPort)),
    details: trafficBindingDetails(item, localPortForward),
    state: stateForBinding(item),
    managedLocally,
  };
}

function portForwardRow(item: ServerPortForwardInfo): SessionRow {
  return {
    key: `port-forward:${item.id}`,
    id: item.id,
    kind: "port-forward",
    targetType: targetTypeLabel(item.kind),
    target: targetWithPorts(`${item.namespace}/${item.name}`, [item.remotePort]),
    details: [{ local: item.address, remote: item.dialAddress, flow: "bidirectional" }],
    state: item.state,
    managedLocally: true,
  };
}

function localTargetRow(kind: "exchange" | "mirror", item: ServerExchangeInfo | ServerMirrorInfo): SessionRow {
  return {
    key: `${kind}:${item.id}`,
    id: item.id,
    kind,
    targetType: "Service",
    target: targetWithPorts(`${item.namespace}/${item.service}`, item.targets.map((target) => target.servicePort)),
    details: localTargetDetails(item.clusterIp, item.targets, kind === "mirror"),
    state: item.state,
    managedLocally: true,
  };
}

function previewRow(item: ServerPreviewInfo): SessionRow {
  return {
    key: `preview:${item.id}`,
    id: item.id,
    kind: "preview",
    targetType: "Service",
    target: targetWithPorts(item.name, item.targets.map((target) => target.servicePort)),
    details: localTargetDetails(item.clusterIp, item.targets, false),
    state: item.state,
    managedLocally: true,
  };
}

function localTargetDetails(
  clusterIp: string,
  targets: Array<{ servicePort: number; protocol: string; localHost: string; localPort: number }>,
  oneWay: boolean,
): TrafficEndpointPair[] {
  return targets.flatMap((target) => {
    const cluster = formatHostPort(clusterIp, target.servicePort);
    const local = formatHostPort(target.localHost, target.localPort);
    return [{ local, remote: cluster, flow: oneWay ? "remote-to-local" : "bidirectional" }];
  });
}

function formatHostPort(host: string, port: number) {
  const formattedHost = host.includes(":") && !host.startsWith("[") ? `[${host}]` : host;
  return `${formattedHost || "—"}:${port}`;
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
