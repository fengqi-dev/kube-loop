import { errorMessage } from "@/lib/errors";
import { ToolbarActions } from "@/components/workspace/toolbar-actions";
import { useRequestGeneration } from "@/components/workspace/use-request-generation";
import { ResourceWorkspace, useResourceWorkspace } from "@/components/workspace/resource-workspace";
import { resourceKey } from "@/components/workspace/workspace-model";
import { useI18n } from "@/i18n";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Globe2, Network, Pause, Play, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { backend } from "@/backend";
import { ActionIconButton, exchangeIcon, mirrorIcon, portForwardIcon } from "@/components/network/action-icons";
import { CopyableText } from "@/components/shared/copyable-text";
import { EmptyState } from "@/components/shared/empty-state";
import { PageShell } from "@/components/shared/page-shell";
import { ResourcePagination, RESOURCE_PAGE_SIZE } from "@/components/shared/resource-pagination";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type {
  RemoteInventory,
  RemoteService,
  ServerExchangeInfo,
  ServerMirrorInfo,
  ServerPortForwardInfo,
  ServerPreviewInfo,
} from "@/types";

type Action = "port-forward" | "exchange" | "mirror" | "preview";

export function ServerNetworkView({ profileId, active = true, selectedNamespace, sharedInventory, inventoryLoading = false, onNamespaceChange }: { profileId: string; active?: boolean; selectedNamespace?: string; sharedInventory?: RemoteInventory; inventoryLoading?: boolean; onNamespaceChange?(namespace: string): void }) {
  const { t } = useI18n();
  const workspace = useResourceWorkspace();
  const requests = useRequestGeneration();
  const [inventory, setInventory] = useState<RemoteInventory>();
  const [forwards, setForwards] = useState<ServerPortForwardInfo[]>([]);
  const [exchanges, setExchanges] = useState<ServerExchangeInfo[]>([]);
  const [mirrors, setMirrors] = useState<ServerMirrorInfo[]>([]);
  const [previews, setPreviews] = useState<ServerPreviewInfo[]>([]);
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(1);
  const [action, setAction] = useState<Action>();
  const [service, setService] = useState<RemoteService>();
  const [servicePort, setServicePort] = useState("");
  const [localHost, setLocalHost] = useState("127.0.0.1");
  const [localPort, setLocalPort] = useState("");
  const [previewName, setPreviewName] = useState("");
  const [previewProtocol, setPreviewProtocol] = useState<"tcp" | "udp">("tcp");
  const [localLoading, setLoading] = useState(true);
  const loading = localLoading || inventoryLoading;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(async (namespace = "", snapshot?: RemoteInventory) => {
    const isCurrent = requests.begin();
    if (!profileId) {
      if (!isCurrent()) return;
      setInventory(undefined);
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const next = snapshot ?? await backend.loadServerInventory(profileId, namespace);
      if (!isCurrent()) return;
      setInventory(next);
      const [nextForwards, nextExchanges, nextMirrors, nextPreviews] = await Promise.allSettled([
        backend.listServerPortForwards(profileId), backend.listServerExchanges(profileId),
        backend.listServerMirrors(profileId), backend.listServerPreviews(profileId),
      ]);
      if (!isCurrent()) return;
      if (nextForwards.status === "fulfilled") setForwards(nextForwards.value);
      if (nextExchanges.status === "fulfilled") setExchanges(nextExchanges.value);
      if (nextMirrors.status === "fulfilled") setMirrors(nextMirrors.value);
      if (nextPreviews.status === "fulfilled") setPreviews(nextPreviews.value);
      const failures = [nextForwards, nextExchanges, nextMirrors, nextPreviews]
        .filter((result) => result.status === "rejected")
        .map((result) => errorMessage(result.reason));
      setError(failures.join("\n"));
    } catch (reason) {
      if (!isCurrent()) return;
      setError(errorMessage(reason));
    } finally {
      if (isCurrent()) setLoading(false);
    }
  }, [profileId]);

  useEffect(() => {
    if (!active) { requests.invalidate(); return; }
    // The connection controller already loads the selected namespace. Reuse its
    // snapshot instead of issuing another full inventory request after it finishes.
    if (onNamespaceChange) {
      if (sharedInventory) void load(sharedInventory.namespace, sharedInventory);
      else { requests.invalidate(); setInventory(undefined); setLoading(false); }
      return;
    }
    void load(selectedNamespace ?? inventory?.namespace);
    // Keep each view’s filters while revalidating its current namespace.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load, active, selectedNamespace, sharedInventory]);

  const services = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
  return (inventory?.services ?? []).filter((item) => !normalized || [
      item.name, item.namespace, item.type, item.clusterIp, item.externalName,
    ].some((value) => value?.toLocaleLowerCase().includes(normalized)));
  }, [inventory?.services, query]);

  const pageCount = Math.max(1, Math.ceil(services.length / RESOURCE_PAGE_SIZE));
  const visibleServices = services.slice((page - 1) * RESOURCE_PAGE_SIZE, page * RESOURCE_PAGE_SIZE);

  useEffect(() => setPage(1), [query, inventory?.namespace]);
  useEffect(() => setPage((current) => Math.min(current, pageCount)), [pageCount]);

  function selectAction(next: Action, target?: RemoteService) {
    setAction(next); setService(target); setLocalHost("127.0.0.1");
    setError("");
    const port = target?.ports[0]?.port;
    setServicePort(port ? String(port) : "");
    setLocalPort(next === "port-forward" ? "" : port ? String(port) : "");
    if (next === "preview") setPreviewName("");
  }

  async function start() {
    if (!profileId || !inventory?.session || !action || busy) return;
    const remote = Number(servicePort);
    const local = localPort.trim() ? Number(localPort) : 0;
    if (!Number.isInteger(remote) || remote < 1 || remote > 65535 || !Number.isInteger(local) || local < 0 || local > 65535) {
      setError("Enter valid Service and local ports."); return;
    }
    setBusy(true); setError("");
    try {
      if (action === "port-forward" && service) {
        const created = await backend.startServerPortForward({ profileId, kind: "service", name: service.name, protocol: protocolFor(service, remote), remotePort: remote, localPort: local });
        setForwards((current) => upsertTask(current, created));
        toast.success("Port Forward started", { description: created.address });
      } else if (action === "exchange" && service) {
        const created = await backend.startServerExchange({ profileId, service: service.name, targets: [{ servicePort: remote, protocol: protocolFor(service, remote), localHost, localPort: local }] });
        setExchanges((current) => upsertTask(current, created));
        toast.success("Exchange started", { description: `${service.name}:${remote} → ${localHost}:${local}` });
      } else if (action === "mirror" && service) {
        const created = await backend.startServerMirror({ profileId, service: service.name, targets: [{ servicePort: remote, protocol: protocolFor(service, remote), localHost, localPort: local }] });
        setMirrors((current) => upsertTask(current, created));
        toast.success("Mirror started", { description: `${service.name}:${remote} → ${localHost}:${local}` });
      } else if (action === "preview" && previewName.trim()) {
        const created = await backend.startServerPreview({ profileId, namespace: inventory.namespace ?? "", name: previewName.trim(), targets: [{ servicePort: remote, protocol: previewProtocol, localHost, localPort: local }] });
        setPreviews((current) => upsertTask(current, created));
        toast.success("Preview started", { description: `${created.namespace}/${created.name} → ${localHost}:${local}` });
      } else {
        throw new Error("Choose a Service or enter a Preview name.");
      }
      setAction(undefined); setService(undefined);
    } catch (reason) {
      setError(errorMessage(reason));
    } finally { setBusy(false); }
  }

  const ready = inventory?.dataPlane?.state === "connected";
  const canForward = inventory?.capabilities.includes("ports.forward") ?? false;
  const canExchange = inventory?.capabilities.includes("services.exchange") ?? false;
  const canMirror = inventory?.capabilities.includes("services.mirror") ?? false;
  const canPreview = inventory?.capabilities.includes("services.preview") ?? false;
    const workspaceResources = (inventory?.services ?? []).map(item => ({
    key: resourceKey({ profileId, namespace: item.namespace, kind: "service", id: item.name }),
    label: item.name, namespace: item.namespace,
    fields: [[t("network.colNamespace"), item.namespace], [t("workspace.type"), item.type],
      [t("workspace.address"), item.clusterIp || item.externalName],
      [t("network.colPorts"), item.ports.map(port => `${port.protocol}/${port.port}`).join(", ")]] as Array<[string, React.ReactNode]>,
    actions: <>
      <ActionIconButton label="Port Forward" icon={portForwardIcon} disabled={!ready || !canForward || forwards.some(entry => entry.kind === "service" && entry.name === item.name && entry.namespace === item.namespace)} onClick={() => selectAction("port-forward", item)} />
      <ActionIconButton label="Exchange" icon={exchangeIcon} disabled={!ready || !canExchange || exchanges.some(entry => entry.service === item.name && entry.namespace === item.namespace)} onClick={() => selectAction("exchange", item)} />
      <ActionIconButton label="Mirror" icon={mirrorIcon} disabled={!ready || !canMirror || mirrors.some(entry => entry.service === item.name && entry.namespace === item.namespace)} onClick={() => selectAction("mirror", item)} />
    </>,
  }));
  return (
    <PageShell title="Network" description="Manage Services and Session-bound traffic operations through the Gateway.">
      <div className="resource-toolbar">
        <select className="h-9 w-[180px] shrink-0 rounded-md border border-input bg-background px-3 text-sm" value={inventory?.namespace ?? ""} disabled={!inventory || loading} onChange={(event) => onNamespaceChange ? onNamespaceChange(event.target.value) : void load(event.target.value)}>
          {!inventory ? <option value="" disabled>Loading…</option> : null}
          {(inventory?.namespaces ?? []).map((item) => <option key={item.name} value={item.name}>{item.name}</option>)}
        </select>
        <Input className="min-w-0 flex-1" value={query} placeholder="Search Services" onChange={(event) => setQuery(event.target.value)} />
        <Button className="shrink-0 whitespace-nowrap" type="button" variant="outline" size="sm" disabled={!profileId || loading} onClick={() => onNamespaceChange ? onNamespaceChange(inventory?.namespace ?? "") : void load(inventory?.namespace)}>{loading ? <Spinner data-icon="inline-start" /> : <RefreshCw size={14} />}Refresh</Button>
        <ToolbarActions><Button className="shrink-0 whitespace-nowrap" type="button" size="sm" disabled={!inventory?.session || !ready || !canPreview} onClick={() => selectAction("preview")}><Globe2 size={14} />Create Preview</Button></ToolbarActions>
      </div>
      <ResourceWorkspace namespace={inventory?.namespace} workspace={workspace} resources={workspaceResources} settled={!loading && !error}>
      {!profileId || (!inventory && !loading) ? <EmptyState icon={Network} title="Network unavailable" detail={error || "Select and sign in to a Server first."} /> : <div className="space-y-5">
        {error ? <p className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</p> : null}
        <div className="overflow-hidden rounded-lg border bg-card">
          <Table><TableHeader><TableRow><TableHead className="w-40 min-w-40 max-w-40">Name</TableHead><TableHead>Namespace</TableHead><TableHead>Type</TableHead><TableHead>Cluster IP</TableHead><TableHead>Ports</TableHead><TableHead>Actions</TableHead></TableRow></TableHeader>
          <TableBody>
            {loading ? (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={6} className="h-32 text-center text-[12px] text-muted-foreground">
                  <Spinner className="mx-auto" />
                </TableCell>
              </TableRow>
            ) : services.length === 0 ? (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={6} className="h-32 text-center text-[12px] text-muted-foreground">
                  No Services found.
                </TableCell>
              </TableRow>
            ) : (
              visibleServices.map((item) => {
            const forward = forwards.some((entry) => entry.kind === "service" && entry.name === item.name && entry.namespace === item.namespace);
            const exchange = exchanges.some((entry) => entry.service === item.name && entry.namespace === item.namespace);
            const mirror = mirrors.some((entry) => entry.service === item.name && entry.namespace === item.namespace);
            const preview = previews.some((entry) => entry.name === item.name && entry.namespace === item.namespace);
            return <TableRow key={`${item.namespace}/${item.name}`} className="cursor-pointer" tabIndex={0}
              data-state={workspace.state.active === resourceKey({ profileId, namespace: item.namespace, kind: "service", id: item.name }) ? "selected" : undefined}
              onClick={event => { if (!(event.target as HTMLElement).closest("button, a")) workspace.open(workspaceResources.find(resource => resource.label === item.name && resource.namespace === item.namespace)!); }}
              onKeyDown={event => { if (event.target === event.currentTarget && event.key === "Enter") workspace.open(workspaceResources.find(resource => resource.label === item.name && resource.namespace === item.namespace)!); }}><TableCell className="w-40 min-w-40 max-w-40 font-medium"><span className="block truncate" title={item.name}>{item.name}</span></TableCell><TableCell>{item.namespace}</TableCell><TableCell>{item.type}</TableCell><TableCell className="font-mono text-xs"><CopyableText value={item.clusterIp || item.externalName} /></TableCell><TableCell className="font-mono text-xs text-muted-foreground">{item.ports.length > 0 ? <div className="flex flex-col items-start gap-0.5">{item.ports.map((port) => <CopyableText key={`${port.protocol}-${port.port}-${port.name || ""}`} value={item.clusterIp ? `${item.clusterIp}:${port.port}` : null} label={`${port.protocol}/${port.port}`} titleKey="network.copyAddress" successKey="network.addressCopied" failKey="network.addressCopyFailed" empty={`${port.protocol}/${port.port}`} />)}</div> : "—"}</TableCell>
              <TableCell><div className="flex items-center gap-1"><ActionIconButton label="Port Forward" icon={portForwardIcon} disabled={forward || preview || !ready || !canForward} onClick={() => selectAction("port-forward", item)} /><ActionIconButton label="Exchange" icon={exchangeIcon} disabled={preview || !ready || !canExchange || exchange || mirror} onClick={() => selectAction("exchange", item)} /><ActionIconButton label="Mirror" icon={mirrorIcon} disabled={preview || !ready || !canMirror || exchange || mirror} onClick={() => selectAction("mirror", item)} /></div></TableCell></TableRow>;
              })
            )}
          </TableBody></Table>
          <ResourcePagination page={page} total={services.length} showWhenEmpty onPageChange={setPage} />
        </div>
      </div>}

      </ResourceWorkspace>

      <Dialog
        open={action === "port-forward"}
        onOpenChange={(open) => {
          if (!open && !busy) {
            setAction(undefined);
            setService(undefined);
            setError("");
          }
        }}
      >
        <DialogContent showCloseButton={!busy}>
          <DialogHeader>
            <DialogTitle>Port Forward</DialogTitle>
            <DialogDescription>
              {service ? `service/${service.namespace}/${service.name}` : "Choose a Service port and local listening port."}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="grid gap-2">
              <label htmlFor="service-forward-remote-port" className="text-sm font-medium">Service port</label>
              <select
                id="service-forward-remote-port"
                className="h-9 rounded-md border border-input bg-background px-3 text-sm"
                value={servicePort}
                disabled={busy}
                onChange={(event) => {
                  setServicePort(event.target.value);
                  setLocalPort(event.target.value);
                }}
              >
                {service?.ports.map((port) => (
                  <option key={`${port.protocol}/${port.port}`} value={port.port}>{port.port}/{port.protocol}</option>
                ))}
              </select>
            </div>
            <div className="grid gap-2">
              <label htmlFor="service-forward-local-port" className="text-sm font-medium">Local port</label>
              <Input
                id="service-forward-local-port"
                type="number"
                min={0}
                max={65535}
                value={localPort}
                placeholder="Auto"
                disabled={busy}
                onChange={(event) => setLocalPort(event.target.value)}
              />
            </div>
          </div>
          {error ? <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => { setAction(undefined); setService(undefined); setError(""); }}>Cancel</Button>
            <Button type="button" disabled={busy || !service || !servicePort} onClick={() => void start()}>
              {busy ? <Spinner data-icon="inline-start" /> : null}
              Start
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={action === "exchange" || action === "mirror"}
        onOpenChange={(open) => {
          if (!open && !busy) {
            setAction(undefined);
            setService(undefined);
            setError("");
          }
        }}
      >
        <DialogContent showCloseButton={!busy}>
          <DialogHeader>
            <DialogTitle>{action === "mirror" ? "Mirror" : "Exchange"}</DialogTitle>
            <DialogDescription>
              {service ? `service/${service.namespace}/${service.name}` : "Choose the Service port and local target."}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="grid gap-2">
              <label htmlFor="service-traffic-remote-port" className="text-sm font-medium">Service port</label>
              <select
                id="service-traffic-remote-port"
                className="h-9 rounded-md border border-input bg-background px-3 text-sm"
                value={servicePort}
                disabled={busy}
                onChange={(event) => {
                  setServicePort(event.target.value);
                  setLocalPort(event.target.value);
                }}
              >
                {service?.ports.map((port) => (
                  <option key={`${port.protocol}/${port.port}`} value={port.port}>{port.port}/{port.protocol}</option>
                ))}
              </select>
            </div>
            <div className="grid gap-2">
              <label htmlFor="service-traffic-local-port" className="text-sm font-medium">Local port</label>
              <Input
                id="service-traffic-local-port"
                type="number"
                min={0}
                max={65535}
                value={localPort}
                placeholder="Auto"
                disabled={busy}
                onChange={(event) => setLocalPort(event.target.value)}
              />
            </div>
            <div className="grid gap-2 sm:col-span-2">
              <label htmlFor="service-traffic-local-host" className="text-sm font-medium">Local host</label>
              <Input
                id="service-traffic-local-host"
                value={localHost}
                placeholder="127.0.0.1"
                disabled={busy}
                onChange={(event) => setLocalHost(event.target.value)}
              />
            </div>
          </div>
          {error ? <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => { setAction(undefined); setService(undefined); setError(""); }}>Cancel</Button>
            <Button type="button" disabled={busy || !service || !servicePort || !localHost.trim()} onClick={() => void start()}>
              {busy ? <Spinner data-icon="inline-start" /> : null}
              Start
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={action === "preview"}
        onOpenChange={(open) => {
          if (!open && !busy) {
            setAction(undefined);
            setError("");
          }
        }}
      >
        <DialogContent showCloseButton={!busy}>
          <DialogHeader>
            <DialogTitle>Create Preview</DialogTitle>
            <DialogDescription>Expose a local target through a temporary Service preview.</DialogDescription>
          </DialogHeader>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="grid gap-2 sm:col-span-2">
              <label htmlFor="preview-namespace" className="text-sm font-medium">Namespace</label>
              <Input id="preview-namespace" value={inventory?.namespace ?? ""} readOnly disabled />
            </div>
            <div className="grid gap-2 sm:col-span-2">
              <label htmlFor="preview-name" className="text-sm font-medium">Name</label>
              <Input
                id="preview-name"
                value={previewName}
                placeholder="Preview name"
                disabled={busy}
                onChange={(event) => setPreviewName(event.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <label htmlFor="preview-protocol" className="text-sm font-medium">Protocol</label>
              <select
                id="preview-protocol"
                className="h-9 rounded-md border border-input bg-background px-3 text-sm"
                value={previewProtocol}
                disabled={busy}
                onChange={(event) => setPreviewProtocol(event.target.value === "udp" ? "udp" : "tcp")}
              >
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
              </select>
            </div>
            <div className="grid gap-2">
              <label htmlFor="preview-service-port" className="text-sm font-medium">Service port</label>
              <Input
                id="preview-service-port"
                type="number"
                min={1}
                max={65535}
                value={servicePort}
                placeholder="Service port"
                disabled={busy}
                onChange={(event) => {
                  const nextServicePort = event.target.value;
                  const previousServicePort = servicePort;
                  setServicePort(nextServicePort);
                  setLocalPort((current) => current === "" || current === previousServicePort ? nextServicePort : current);
                }}
              />
            </div>
            <div className="grid gap-2">
              <label htmlFor="preview-local-host" className="text-sm font-medium">Local host</label>
              <Input
                id="preview-local-host"
                value={localHost}
                placeholder="127.0.0.1"
                disabled={busy}
                onChange={(event) => setLocalHost(event.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <label htmlFor="preview-local-port" className="text-sm font-medium">Local port</label>
              <Input
                id="preview-local-port"
                type="number"
                min={0}
                max={65535}
                value={localPort}
                placeholder="Auto"
                disabled={busy}
                onChange={(event) => setLocalPort(event.target.value)}
              />
            </div>
          </div>
          {error ? <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</p> : null}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => { setAction(undefined); setError(""); }}>Cancel</Button>
            <Button type="button" disabled={busy || !previewName.trim() || !servicePort || !localHost.trim()} onClick={() => void start()}>
              {busy ? <Spinner data-icon="inline-start" /> : null}
              Start
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </PageShell>
  );
}

function protocolFor(service: RemoteService, port: number): "tcp" | "udp" { return service.ports.find((item) => item.port === port)?.protocol.toLowerCase() === "udp" ? "udp" : "tcp"; }
function labelFor(action: Action) { return action === "port-forward" ? "Port Forward" : action[0]!.toUpperCase() + action.slice(1); }
function upsertTask<T extends { id: string }>(items: T[], task: T) {
  return [...items.filter((item) => item.id !== task.id), task];
}
