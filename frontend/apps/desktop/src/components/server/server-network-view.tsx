import { useCallback, useEffect, useMemo, useState } from "react";
import { Globe2, Network, RefreshCw, Square } from "lucide-react";
import { backend } from "@/backend";
import { ActionIconButton, exchangeIcon, mirrorIcon, portForwardIcon } from "@/components/network/action-icons";
import { CopyableText } from "@/components/shared/copyable-text";
import { EmptyState } from "@/components/shared/empty-state";
import { PageShell } from "@/components/shared/page-shell";
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

export function ServerNetworkView({ profileId }: { profileId: string }) {
  const [inventory, setInventory] = useState<RemoteInventory>();
  const [forwards, setForwards] = useState<ServerPortForwardInfo[]>([]);
  const [exchanges, setExchanges] = useState<ServerExchangeInfo[]>([]);
  const [mirrors, setMirrors] = useState<ServerMirrorInfo[]>([]);
  const [previews, setPreviews] = useState<ServerPreviewInfo[]>([]);
  const [query, setQuery] = useState("");
  const [action, setAction] = useState<Action>();
  const [service, setService] = useState<RemoteService>();
  const [servicePort, setServicePort] = useState("");
  const [localHost, setLocalHost] = useState("127.0.0.1");
  const [localPort, setLocalPort] = useState("");
  const [previewName, setPreviewName] = useState("");
  const [previewProtocol, setPreviewProtocol] = useState<"tcp" | "udp">("tcp");
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(async (namespace = "") => {
    if (!profileId) {
      setInventory(undefined);
      return;
    }
    setLoading(true);
    try {
      const next = await backend.loadServerInventory(profileId, namespace);
      const [nextForwards, nextExchanges, nextMirrors, nextPreviews] = await Promise.all([
        backend.listServerPortForwards(profileId), backend.listServerExchanges(profileId),
        backend.listServerMirrors(profileId), backend.listServerPreviews(profileId),
      ]);
      setInventory(next); setForwards(nextForwards); setExchanges(nextExchanges);
      setMirrors(nextMirrors); setPreviews(nextPreviews); setError("");
    } catch (reason) {
      setError(messageOf(reason));
    } finally {
      setLoading(false);
    }
  }, [profileId]);

  useEffect(() => { void load(); }, [load]);

  const services = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    return (inventory?.services ?? []).filter((item) => !normalized || [
      item.name, item.namespace, item.type, item.clusterIp, item.externalName,
    ].some((value) => value?.toLocaleLowerCase().includes(normalized)));
  }, [inventory?.services, query]);

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
        await backend.startServerPortForward({ profileId, kind: "service", name: service.name, protocol: protocolFor(service, remote), remotePort: remote, localPort: local });
      } else if (action === "exchange" && service) {
        await backend.startServerExchange({ profileId, service: service.name, targets: [{ servicePort: remote, protocol: protocolFor(service, remote), localHost, localPort: local }] });
      } else if (action === "mirror" && service) {
        await backend.startServerMirror({ profileId, service: service.name, targets: [{ servicePort: remote, protocol: protocolFor(service, remote), localHost, localPort: local }] });
      } else if (action === "preview" && previewName.trim()) {
        await backend.startServerPreview({ profileId, namespace: inventory.namespace ?? "", name: previewName.trim(), targets: [{ servicePort: remote, protocol: previewProtocol, localHost, localPort: local }] });
      } else {
        throw new Error("Choose a Service or enter a Preview name.");
      }
      setAction(undefined); setService(undefined); await load(inventory.namespace);
    } catch (reason) {
      setError(messageOf(reason));
    } finally { setBusy(false); }
  }

  async function stop(kind: Action, id: string) {
    if (!profileId || busy) return;
    setBusy(true); setError("");
    try {
      if (kind === "port-forward") await backend.stopServerPortForward(profileId, id);
      else if (kind === "exchange") await backend.stopServerExchange(profileId, id);
      else if (kind === "mirror") await backend.stopServerMirror(profileId, id);
      else await backend.stopServerPreview(profileId, id);
      await load(inventory?.namespace);
    } catch (reason) { setError(messageOf(reason)); } finally { setBusy(false); }
  }

  const ready = inventory?.dataPlane?.state === "connected";
  const canForward = inventory?.capabilities.includes("ports.forward") ?? false;
  const canExchange = inventory?.capabilities.includes("services.exchange") ?? false;
  const canMirror = inventory?.capabilities.includes("services.mirror") ?? false;
  const canPreview = inventory?.capabilities.includes("services.preview") ?? false;
  return (
    <PageShell title="Network" description="Manage Services and Session-bound traffic operations through the Gateway.">
      <div className="mb-4 flex flex-nowrap items-center gap-2">
        <select className="h-9 shrink-0 rounded-md border border-input bg-background px-3 text-sm" value={inventory?.namespace ?? ""} disabled={!inventory || loading} onChange={(event) => void load(event.target.value)}>
          {(inventory?.namespaces ?? []).map((item) => <option key={item.name} value={item.name}>{item.name}</option>)}
        </select>
        <Input className="min-w-0 flex-1" value={query} placeholder="Search Services" onChange={(event) => setQuery(event.target.value)} />
        <Button className="shrink-0 whitespace-nowrap" type="button" variant="outline" size="sm" disabled={!profileId || loading} onClick={() => void load(inventory?.namespace)}>{loading ? <Spinner data-icon="inline-start" /> : <RefreshCw size={14} />}Refresh</Button>
        <Button className="shrink-0 whitespace-nowrap" type="button" size="sm" disabled={!inventory?.session || !ready || !canPreview} onClick={() => selectAction("preview")}><Globe2 size={14} />Create Preview</Button>
      </div>
      {!profileId || (!inventory && !loading) ? <EmptyState icon={Network} title="Network unavailable" detail={error || "Select and sign in to a Server first."} /> : <div className="space-y-5">
        {error ? <p className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</p> : null}
        <div className="overflow-hidden rounded-lg border bg-card">
          <Table><TableHeader><TableRow><TableHead className="w-40 min-w-40 max-w-40">Name</TableHead><TableHead>Namespace</TableHead><TableHead>Type</TableHead><TableHead>Cluster IP</TableHead><TableHead>Ports</TableHead><TableHead>Actions</TableHead></TableRow></TableHeader>
          <TableBody>{services.map((item) => {
            const exchange = exchanges.some((entry) => entry.service === item.name && entry.namespace === item.namespace);
            const mirror = mirrors.some((entry) => entry.service === item.name && entry.namespace === item.namespace);
            const preview = previews.some((entry) => entry.name === item.name && entry.namespace === item.namespace);
            return <TableRow key={`${item.namespace}/${item.name}`}><TableCell className="w-40 min-w-40 max-w-40 font-medium"><span className="block truncate" title={item.name}>{item.name}</span></TableCell><TableCell>{item.namespace}</TableCell><TableCell>{item.type}</TableCell><TableCell className="font-mono text-xs"><CopyableText value={item.clusterIp || item.externalName} /></TableCell><TableCell className="font-mono text-xs text-muted-foreground">{item.ports.length > 0 ? <div className="flex flex-col items-start gap-0.5">{item.ports.map((port) => <CopyableText key={`${port.protocol}-${port.port}-${port.name || ""}`} value={item.clusterIp ? `${item.clusterIp}:${port.port}` : null} label={`${port.protocol}/${port.port}`} titleKey="network.copyAddress" successKey="network.addressCopied" failKey="network.addressCopyFailed" empty={`${port.protocol}/${port.port}`} />)}</div> : "—"}</TableCell>
              <TableCell><div className="flex items-center gap-1"><ActionIconButton label="Port Forward" icon={portForwardIcon} disabled={preview || !ready || !canForward} onClick={() => selectAction("port-forward", item)} /><ActionIconButton label="Exchange" icon={exchangeIcon} disabled={preview || !ready || !canExchange || exchange || mirror} onClick={() => selectAction("exchange", item)} /><ActionIconButton label="Mirror" icon={mirrorIcon} disabled={preview || !ready || !canMirror || exchange || mirror} onClick={() => selectAction("mirror", item)} /></div></TableCell></TableRow>;
          })}</TableBody></Table>
          {!loading && services.length === 0 ? <div className="p-8 text-center text-sm text-muted-foreground">No Services found.</div> : null}
        </div>

        <div className="rounded-lg border bg-card"><div className="border-b px-4 py-3 font-medium">Active Sessions</div><Table><TableHeader><TableRow><TableHead>Type</TableHead><TableHead>Target</TableHead><TableHead>Address / Target</TableHead><TableHead>State</TableHead><TableHead className="text-right">Actions</TableHead></TableRow></TableHeader><TableBody>
          {forwards.map((item) => <OperationRow key={item.id} kind="port-forward" id={item.id} target={`${item.kind}/${item.name}:${item.remotePort}`} detail={portForwardDetail(item)} state={item.state} busy={busy} onStop={stop} />)}
          {exchanges.map((item) => <OperationRow key={item.id} kind="exchange" id={item.id} target={item.service} detail={trafficTargetDetail(item)} state={item.state} busy={busy} onStop={stop} />)}
          {mirrors.map((item) => <OperationRow key={item.id} kind="mirror" id={item.id} target={item.service} detail={trafficTargetDetail(item)} state={item.state} busy={busy} onStop={stop} />)}
          {previews.map((item) => <OperationRow key={item.id} kind="preview" id={item.id} target={`${item.namespace}/${item.name}`} detail={trafficTargetDetail(item)} state={item.state} busy={busy} onStop={stop} />)}
        </TableBody></Table>{forwards.length + exchanges.length + mirrors.length + previews.length === 0 ? <div className="p-8 text-center text-sm text-muted-foreground">No active sessions.</div> : null}</div>
      </div>}

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

function OperationRow({ kind, id, target, detail, state, busy, onStop }: { kind: Action; id: string; target: string; detail?: string; state: string; busy: boolean; onStop(kind: Action, id: string): void }) {
  return <TableRow><TableCell><Badge variant="outline">{labelFor(kind)}</Badge></TableCell><TableCell>{target}</TableCell><TableCell className="font-mono text-xs">{detail || "—"}</TableCell><TableCell>{state}</TableCell><TableCell><div className="flex justify-end gap-1"><Button type="button" size="xs" variant="outline" disabled={busy} onClick={() => onStop(kind, id)}><Square size={11} />Stop</Button></div></TableCell></TableRow>;
}
function protocolFor(service: RemoteService, port: number): "tcp" | "udp" { return service.ports.find((item) => item.port === port)?.protocol.toLowerCase() === "udp" ? "udp" : "tcp"; }
function portForwardDetail(item: ServerPortForwardInfo) {
  const protocol = item.protocol ? ` (${item.protocol.toUpperCase()})` : "";
  return item.dialAddress ? `${item.address} → ${item.dialAddress}${protocol}` : `${item.address}${protocol}`;
}
function trafficTargetDetail(item: ServerExchangeInfo | ServerMirrorInfo | ServerPreviewInfo) {
  if (item.targets.length === 0) return item.clusterIp;
  return item.targets.map((target) => {
    const serviceAddress = item.clusterIp ? `${item.clusterIp}:${target.servicePort}` : String(target.servicePort);
    return `${serviceAddress} → ${target.localHost}:${target.localPort} (${target.protocol.toUpperCase()})`;
  }).join(", ");
}
function labelFor(action: Action) { return action === "port-forward" ? "Port Forward" : action[0]!.toUpperCase() + action.slice(1); }
function messageOf(reason: unknown) { return reason instanceof Error ? reason.message : String(reason); }
