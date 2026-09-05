import { errorMessage } from "@/lib/errors";
import { useRequestGeneration } from "@/components/workspace/use-request-generation";
import { ResourceWorkspace, useResourceWorkspace } from "@/components/workspace/resource-workspace";
import { resourceKey } from "@/components/workspace/workspace-model";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Circle, Network } from "lucide-react";
import { toast } from "sonner";
import { backend } from "@/backend";
import { ActionIconButton, portForwardIcon, sftpIcon, sshIcon } from "@/components/network/action-icons";
import { ALL_NAMESPACES, ResourceToolbar } from "@/components/network/resource-toolbar";
import { SFTPFileManagerDialog } from "@/components/sftp/sftp-file-manager-dialog";
import { CopyableText } from "@/components/shared/copyable-text";
import { EmptyState } from "@/components/shared/empty-state";
import { PageShell } from "@/components/shared/page-shell";
import { ResourcePagination, RESOURCE_PAGE_SIZE } from "@/components/shared/resource-pagination";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useI18n } from "@/i18n";
import { cn } from "@/lib/utils";
import type { RemoteInventory, RemotePod, ServerInventoryEvent, ServerPodSSHInfo, ServerPortForwardInfo } from "@/types";

export function ServerWorkloadView({ profileId, active = true, selectedNamespace, sharedInventory, inventoryLoading = false, onNamespaceChange }: { profileId: string; active?: boolean; selectedNamespace?: string; sharedInventory?: RemoteInventory; inventoryLoading?: boolean; onNamespaceChange?(namespace: string): void }) {
  const { t } = useI18n();
  const workspace = useResourceWorkspace();
  const requests = useRequestGeneration();
  const [inventory, setInventory] = useState<RemoteInventory>();
  const [namespace, setNamespace] = useState("");
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(1);
  const [localLoading, setLoading] = useState(true);
  const loading = localLoading || inventoryLoading;
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<RemotePod | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [sftpOpen, setSFTPOpen] = useState(false);
  const [remotePort, setRemotePort] = useState("");
  const [protocol, setProtocol] = useState<"tcp" | "udp">("tcp");
  const [localPort, setLocalPort] = useState("");
  const [busy, setBusy] = useState(false);
  const [sshEndpoints, setSSHEndpoints] = useState<ServerPodSSHInfo[]>([]);
  const [forwards, setForwards] = useState<ServerPortForwardInfo[]>([]);

  const reload = useCallback(async (nextNamespace = namespace, snapshot?: RemoteInventory) => {
    const isCurrent = requests.begin();
    if (!profileId) {
      if (!isCurrent()) return;
      setInventory(undefined);
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const next = snapshot ?? await backend.loadServerInventory(
        profileId,
        nextNamespace === ALL_NAMESPACES ? "" : nextNamespace,
      );
      if (!isCurrent()) return;
      setInventory({ ...next, pods: (next.pods ?? []).map((pod) => ({ ...pod, ports: pod.ports ?? [] })) });
      setNamespace(next.namespace ?? "");
      const [endpoints, activeForwards] = await Promise.allSettled([
        backend.listServerPodSSH(profileId),
        backend.listServerPortForwards(profileId),
      ]);
      if (!isCurrent()) return;
      if (endpoints.status === "fulfilled") setSSHEndpoints(endpoints.value);
      if (activeForwards.status === "fulfilled") {
        setForwards(activeForwards.value.filter((item) => item.kind === "pod"));
      }
      const failures = [endpoints, activeForwards]
        .filter((result) => result.status === "rejected")
        .map((result) => errorMessage(result.reason));
      setError(failures.join("\n"));
    } catch (reason) {
      if (!isCurrent()) return;
      setError(errorMessage(reason));
    } finally {
      if (isCurrent()) setLoading(false);
    }
  }, [namespace, profileId]);

  useEffect(() => {
    if (!active) { requests.invalidate(); return; }
    // The connection controller already loads the selected namespace. Reuse its
    // snapshot instead of issuing another full inventory request after it finishes.
    if (onNamespaceChange) {
      if (sharedInventory) void reload(sharedInventory.namespace, sharedInventory);
      else { requests.invalidate(); setInventory(undefined); setLoading(false); }
      return;
    }
    void reload(selectedNamespace);
    // Revalidate the retained namespace before its actions become available.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [profileId, active, selectedNamespace, sharedInventory]);

  useEffect(() => {
    const unsubscribe = window.runtime?.EventsOn("server-inventory:snapshot", (value: unknown) => {
      const event = value as ServerInventoryEvent;
      if (event.profileId !== profileId || event.resource !== "pods" || !event.snapshot?.pods) return;
      setInventory((current) => current && current.namespace === event.namespace
        ? { ...current, pods: event.snapshot!.pods!.map((pod) => ({ ...pod, ports: pod.ports ?? [] })) }
        : current);
    });
  return () => unsubscribe?.();
  }, [profileId]);

  const namespaces = useMemo(() => (inventory?.namespaces ?? []).map((item) => item.name), [inventory?.namespaces]);
  const filtered = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    return (inventory?.pods ?? []).filter((pod) => !normalized || [
      pod.name, pod.namespace, pod.podIp, pod.nodeName,
    ].some((value) => value?.toLocaleLowerCase().includes(normalized)));
  }, [inventory?.pods, query]);
  const pageCount = Math.max(1, Math.ceil(filtered.length / RESOURCE_PAGE_SIZE));
  const visiblePods = filtered.slice((page - 1) * RESOURCE_PAGE_SIZE, page * RESOURCE_PAGE_SIZE);

  useEffect(() => setPage(1), [namespace, profileId, query]);
  useEffect(() => setPage((current) => Math.min(current, pageCount)), [pageCount]);

  async function startForward() {
    if (!selected || !profileId || busy) return;
    const remote = Number(remotePort);
    const local = localPort.trim() ? Number(localPort) : 0;
    if (!Number.isInteger(remote) || remote < 1 || remote > 65535 || !Number.isInteger(local) || local < 0 || local > 65535) {
      setError("Enter valid remote and local ports.");
      return;
    }
    setBusy(true);
    try {
      const created = await backend.startServerPortForward({
        profileId,
        kind: "pod",
        name: selected.name,
        protocol,
        remotePort: remote,
        localPort: local,
      });
      setForwards((current) => [...current.filter((item) => item.id !== created.id), created]);
      toast.success("Port Forward started", { description: created.address });
      setDialogOpen(false);
      setSelected(null);
      setRemotePort("");
      setLocalPort("");
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setBusy(false);
    }
  }

  async function openSSH(pod: RemotePod, container: string) {
    if (!profileId || busy) return;
    setBusy(true);
    try {
      let endpoint = sshEndpoints.find((item) => item.pod === pod.name && item.container === container);
      if (!endpoint) endpoint = await backend.startServerPodSSH({ profileId, pod: pod.name, container });
      setSSHEndpoints((current) => [...current.filter((item) => item.id !== endpoint!.id), endpoint!]);
      await backend.openServerPodSSH(profileId, endpoint.id);
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setBusy(false);
    }
  }

  const ready = inventory?.dataPlane?.state === "connected";
  const canForward = inventory?.capabilities.includes("ports.forward") ?? false;
  const canExec = inventory?.capabilities.includes("pods.exec") ?? false;
  const canFiles = inventory?.capabilities.includes("pods.files") ?? false;
  const canManageFiles = inventory?.capabilities.includes("pods.files.manage") ?? false;

    function renderActions(pod: RemotePod) {
    return                       <div className="flex items-center gap-1">
                        <ActionIconButton label={t("network.tabPortForward")} icon={portForwardIcon} disabled={!ready || !canForward || forwards.some((entry) => entry.name === pod.name && entry.namespace === pod.namespace)} onClick={() => { const port = pod.ports?.[0]; setSelected(pod); setRemotePort(port ? String(port.port) : ""); setProtocol(port?.protocol.toLowerCase() === "udp" ? "udp" : "tcp"); setDialogOpen(true); }} />
                        <ActionIconButton label={t("sftp.openManager")} icon={sftpIcon} disabled={!ready || (!canFiles && !canManageFiles)} onClick={() => { setSelected(pod); setSFTPOpen(true); }} />
                        {pod.containers.map((container) => (
                          <ActionIconButton key={container} label={`${t("workload.openSSH")} · ${container}`} icon={sshIcon} text={pod.containers.length > 1 ? container : undefined} disabled={!ready || !pod.ready || !canExec || busy} onClick={() => void openSSH(pod, container)} />
                        ))}
                      </div>;
  }
  const workspaceResources = (inventory?.pods ?? []).map(pod => ({
    key: resourceKey({ profileId, namespace: pod.namespace, kind: "pod", id: pod.name }),
    label: pod.name, namespace: pod.namespace,
    fields: [
      [t("network.colNamespace"), pod.namespace], [t("workspace.state"), pod.phase],
      [t("network.colIP"), pod.podIp], [t("network.colNode"), pod.nodeName],
      [t("workspace.containers"), pod.containers.join(", ")],
      [t("network.colPorts"), pod.ports.map(port => `${port.protocol}/${port.port}`).join(", ")],
    ] as Array<[string, React.ReactNode]>, actions: renderActions(pod),
  }));
  return (
    <PageShell title={t("workload.title")} description={t("workload.description")}>
      <ResourceToolbar
        namespaces={namespaces}
        namespace={namespace}
        onNamespaceChange={(value) => { if (onNamespaceChange) onNamespaceChange(value); else { setNamespace(value); void reload(value); } }}
        query={query}
        onQueryChange={setQuery}
        searchPlaceholder={t("workload.search")}
        count={filtered.length}
        loading={loading}
        disabled={!profileId}
        onRefresh={() => onNamespaceChange ? onNamespaceChange(namespace) : void reload(namespace)}
        allowAllNamespaces={false}
        namespacePlaceholder={loading ? t("overview.loadingKubeconfig") : undefined}
      />

      <ResourceWorkspace namespace={inventory?.namespace} workspace={workspace} resources={workspaceResources} settled={!loading && !error}>
      {!profileId ? (
        <EmptyState icon={Network} title={t("network.waitingTitle")} detail={t("network.selectContext")} />
      ) : (
        <div className="overflow-hidden rounded-lg border bg-card">
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className="h-9 w-40 min-w-40 max-w-40 text-[11px] font-medium text-muted-foreground">{t("network.colName")}</TableHead>
                <TableHead className="h-9 text-[11px] font-medium text-muted-foreground">{t("network.colNamespace")}</TableHead>
                <TableHead className="h-9 text-[11px] font-medium text-muted-foreground">{t("network.colIP")}</TableHead>
                <TableHead className="h-9 text-[11px] font-medium text-muted-foreground">{t("network.colStatus")}</TableHead>
                <TableHead className="h-9 text-[11px] font-medium text-muted-foreground">{t("network.colNode")}</TableHead>
                <TableHead className="h-9 text-[11px] font-medium text-muted-foreground">{t("network.colPorts")}</TableHead>
                <TableHead className="h-9 text-[11px] font-medium text-muted-foreground">{t("network.actions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading ? (
                <TableRow className="hover:bg-transparent">
                  <TableCell colSpan={7} className="h-32 text-center text-[12px] text-muted-foreground">
                    <Spinner className="mx-auto" />
                  </TableCell>
                </TableRow>
              ) : visiblePods.length === 0 ? (
                <TableRow className="hover:bg-transparent">
                  <TableCell colSpan={7} className="h-32 text-center text-[12px] text-muted-foreground">
                    {error || t("workload.empty")}
                  </TableCell>
                </TableRow>
              ) : (
                visiblePods.map((pod) => (
                  <TableRow key={`${pod.namespace}/${pod.name}`} data-state={workspace.state.active === resourceKey({ profileId, namespace: pod.namespace, kind: "pod", id: pod.name }) ? "selected" : undefined}
                    tabIndex={0} className="cursor-pointer" onClick={(event) => { if (!(event.target as HTMLElement).closest("button, a")) workspace.open(workspaceResources.find(item => item.label === pod.name && item.namespace === pod.namespace)!); }}
                    onKeyDown={event => { if (event.target === event.currentTarget && event.key === "Enter") workspace.open(workspaceResources.find(item => item.label === pod.name && item.namespace === pod.namespace)!); }}>
                    <TableCell className="w-40 min-w-40 max-w-40 font-medium"><span className="block truncate" title={pod.name}>{pod.name}</span></TableCell>
                    <TableCell className="text-primary">{pod.namespace}</TableCell>
                    <TableCell className="font-mono text-[12px]"><CopyableText value={pod.podIp} /></TableCell>
                    <TableCell><StatusPill ok={pod.ready} label={pod.ready ? t("network.ready") : pod.phase || t("network.notReady")} /></TableCell>
                    <TableCell className="text-muted-foreground">{pod.nodeName || "—"}</TableCell>
                    <TableCell className="font-mono text-[12px] text-muted-foreground">
                      {(pod.ports ?? []).length > 0 ? (
                        <div className="flex flex-col items-start gap-0.5">
                          {(pod.ports ?? []).map((port) => (
                            <CopyableText
                              key={`${port.protocol}-${port.port}-${port.name || ""}`}
                              value={pod.podIp ? `${pod.podIp}:${port.port}` : null}
                              label={`${port.protocol}/${port.port}`}
                              titleKey="network.copyAddress"
                              successKey="network.addressCopied"
                              failKey="network.addressCopyFailed"
                              empty={`${port.protocol}/${port.port}`}
                            />
                          ))}
                        </div>
                      ) : "—"}
                    </TableCell>
                    <TableCell>
{renderActions(pod)}
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
          <ResourcePagination page={page} total={filtered.length} showWhenEmpty onPageChange={setPage} />
        </div>
      )}

      </ResourceWorkspace>

      <SFTPFileManagerDialog
        open={sftpOpen}
        onOpenChange={(open) => { setSFTPOpen(open); if (!open) setSelected(null); }}
        profileId={profileId}
        pod={selected}
      />

      {error && filtered.length > 0 ? <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</p> : null}

      <Dialog open={dialogOpen} onOpenChange={(open) => { if (!busy) setDialogOpen(open); }}>
        <DialogContent showCloseButton={!busy}>
          <DialogHeader><DialogTitle>{t("portfwd.title")}</DialogTitle><DialogDescription>{selected ? `pod/${selected.namespace}/${selected.name}` : t("portfwd.description")}</DialogDescription></DialogHeader>
          <div className="grid gap-3">
            {selected?.ports?.length ? (
              <select className="h-9 rounded-md border border-input bg-background px-3 text-sm" value={`${protocol}:${remotePort}`} onChange={(event) => { const [nextProtocol, nextPort] = event.target.value.split(":"); setProtocol(nextProtocol === "udp" ? "udp" : "tcp"); setRemotePort(nextPort ?? ""); }}>
                {selected.ports.map((port) => <option key={`${port.protocol}-${port.port}-${port.name || ""}`} value={`${port.protocol.toLowerCase()}:${port.port}`}>{port.protocol}/{port.port}{port.name ? ` (${port.name})` : ""}</option>)}
              </select>
            ) : <Input value={remotePort} onChange={(event) => setRemotePort(event.target.value)} placeholder={t("portfwd.remotePort")} inputMode="numeric" />}
            <Input value={localPort} onChange={(event) => setLocalPort(event.target.value)} placeholder={t("portfwd.localPortAuto")} inputMode="numeric" />
          </div>
          <DialogFooter><Button type="button" variant="outline" disabled={busy} onClick={() => setDialogOpen(false)}>{t("actions.cancel")}</Button><Button type="button" disabled={!selected || !remotePort || busy} onClick={() => void startForward()}>{busy ? <Spinner data-icon="inline-start" /> : null}{t("portfwd.start")}</Button></DialogFooter>
        </DialogContent>
      </Dialog>
    </PageShell>
  );
}

function StatusPill({ ok, label }: { ok: boolean; label: string }) {
  return <span className={cn("inline-flex items-center gap-1.5 text-[12px]", ok ? "text-success" : "text-muted-foreground")}><Circle size={8} className={ok ? "fill-success text-success" : "fill-muted-foreground/50 text-muted-foreground/50"} />{label}</span>;
}
