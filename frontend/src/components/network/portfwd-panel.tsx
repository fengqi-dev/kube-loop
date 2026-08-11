import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Cable, Copy, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { backend } from "@/backend";
import { NamespaceSelect } from "@/components/network/namespace-select";
import { Button } from "@/components/ui/button";
import {
  Field as ShadcnField,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useI18n } from "@/i18n";
import { isValidPort } from "@/lib/validation";
import type { PodInfo, PortForwardInfo, ServiceInfo, ServicePortInfo } from "@/types";

const inputClassName =
  "h-9 w-full rounded-lg border border-input bg-transparent px-2.5 text-sm outline-none transition-colors focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-input/30";

type Kind = "pod" | "service";

export function PortForwardPanel({
  contextName,
  namespaces,
  namespace,
  onNamespaceChange,
  embedded = false,
}: {
  contextName: string;
  namespaces: string[];
  namespace: string;
  onNamespaceChange(value: string): void;
  embedded?: boolean;
}) {
  const { t } = useI18n();
  const enabled = Boolean(contextName);
  const [kind, setKind] = useState<Kind>("service");
  const [services, setServices] = useState<ServiceInfo[]>([]);
  const [pods, setPods] = useState<PodInfo[]>([]);
  const [forwards, setForwards] = useState<PortForwardInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [targetName, setTargetName] = useState("");
  const [portKey, setPortKey] = useState("");
  const [manualRemotePort, setManualRemotePort] = useState("");
  const [localPort, setLocalPort] = useState("");

  const ports = useMemo(() => {
    if (kind === "service") {
      return services.find((item) => item.name === targetName)?.ports ?? [];
    }
    return (pods.find((item) => item.name === targetName)?.ports ?? []).map(
      (port) =>
        ({
          name: port.name,
          port: port.port,
          protocol: port.protocol,
        }) satisfies ServicePortInfo,
    );
  }, [kind, pods, services, targetName]);

  const selectedPort = useMemo(() => {
    if (ports.length > 0) {
      return ports.find((port) => portKeyOf(port) === portKey);
    }
    const parsed = Number(manualRemotePort);
    if (!isValidPort(parsed)) {
      return undefined;
    }
    return { name: "", port: parsed, protocol: "TCP" } satisfies ServicePortInfo;
  }, [manualRemotePort, portKey, ports]);

  useEffect(() => {
    if (!enabled) {
      setServices([]);
      setPods([]);
      setForwards([]);
      setTargetName("");
      setPortKey("");
      return;
    }
    let active = true;
    setLoading(true);
    Promise.all([
      backend.listServices(contextName, namespace),
      backend.listPods(contextName, namespace),
      backend.listPortForwards(),
    ])
      .then(([serviceItems, podItems, forwardItems]) => {
        if (!active) return;
        setServices(serviceItems);
        setPods(podItems);
        setForwards(forwardItems);
      })
      .catch((error: Error) => {
        if (active) toast.error(t("portfwd.loadFailed"), { description: error.message });
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [contextName, enabled, namespace, t]);

  useEffect(() => {
    const names =
      kind === "service" ? services.map((item) => item.name) : pods.map((item) => item.name);
    if (names.length === 0) {
      setTargetName("");
      return;
    }
    setTargetName((current) => (names.includes(current) ? current : names[0]));
  }, [kind, pods, services]);

  useEffect(() => {
    if (ports.length === 0) {
      setPortKey("");
      return;
    }
    const next = ports[0];
    setPortKey(portKeyOf(next));
  }, [ports]);

  async function refresh() {
    setForwards(await backend.listPortForwards());
  }

  async function onStart() {
    if (!selectedPort || !targetName) return;
    let parsedLocal = 0;
    if (localPort.trim() !== "") {
      parsedLocal = Number(localPort);
      if (!isValidPort(parsedLocal)) {
        toast.error(t("portfwd.invalidLocalPort"));
        return;
      }
    }
    setBusy(true);
    try {
      const info = await backend.startPortForward({
        context: contextName,
        namespace,
        kind,
        name: targetName,
        protocol: selectedPort.protocol,
        remotePort: selectedPort.port,
        localPort: parsedLocal,
      });
      await refresh();
      try {
        await navigator.clipboard.writeText(info.address);
        toast.success(t("portfwd.started"), {
          description: `${info.address} → ${kind}/${targetName}:${selectedPort.port} · ${t("portfwd.copied")}`,
        });
      } catch {
        toast.success(t("portfwd.started"), {
          description: `${info.address} → ${kind}/${targetName}:${selectedPort.port}`,
        });
      }
    } catch (error) {
      toast.error(t("portfwd.startFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setBusy(false);
    }
  }

  async function onStop(id: string) {
    setBusy(true);
    try {
      await backend.stopPortForward(id);
      await refresh();
      toast.success(t("portfwd.stopped"));
    } catch (error) {
      toast.error(t("portfwd.stopFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setBusy(false);
    }
  }

  async function onCopyAddress(address: string) {
    try {
      await navigator.clipboard.writeText(address);
      toast.success(t("portfwd.copied"), { description: address });
    } catch {
      toast.error(t("portfwd.copyFailed"));
    }
  }

  const targets = kind === "service" ? services : pods;

  return (
    <section
      className={`overflow-hidden rounded-lg border bg-card ${embedded ? "" : "mt-5"}`}
    >
      {!embedded && (
        <div className="flex items-start justify-between gap-3 border-b px-4 py-3">
          <div>
            <div className="flex items-center gap-2 text-[13px] font-semibold">
              <Cable size={15} className="text-muted-foreground" />
              {t("portfwd.title")}
            </div>
            <p className="mt-1 text-[11px] text-muted-foreground">
              {t("portfwd.description")}
            </p>
          </div>
          {!enabled && (
            <span className="shrink-0 rounded-md border px-2 py-1 text-[10px] text-muted-foreground">
              {t("portfwd.selectContext")}
            </span>
          )}
        </div>
      )}
      {embedded && !enabled && (
        <div className="flex justify-end border-b px-4 py-2">
          <span className="rounded-md border px-2 py-1 text-[10px] text-muted-foreground">
            {t("portfwd.selectContext")}
          </span>
        </div>
      )}

      <div className="grid gap-3 border-b px-4 py-4 md:grid-cols-[0.9fr_0.8fr_1.2fr_1fr_0.9fr_auto]">
        <NamespaceSelect
          value={namespace}
          namespaces={namespaces}
          disabled={!enabled}
          onChange={onNamespaceChange}
        />
        <Field label={t("portfwd.kind")}>
          <Select
            value={kind}
            disabled={!enabled}
            onValueChange={(value) => setKind(value as Kind)}
          >
            <SelectTrigger className="h-9 w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="service">{t("portfwd.kindService")}</SelectItem>
              <SelectItem value="pod">{t("portfwd.kindPod")}</SelectItem>
            </SelectContent>
          </Select>
        </Field>

        <Field label={t("portfwd.target")}>
          <Select
            value={targetName || undefined}
            disabled={!enabled || loading || targets.length === 0}
            onValueChange={setTargetName}
          >
            <SelectTrigger className="h-9 w-full">
              <SelectValue
                placeholder={
                  loading ? t("portfwd.loading") : t("portfwd.selectTarget")
                }
              />
            </SelectTrigger>
            <SelectContent>
              {kind === "service"
                ? services.map((service) => (
                    <SelectItem key={service.name} value={service.name}>
                      {service.name}
                      <span className="ml-2 text-muted-foreground">{service.clusterIP}</span>
                    </SelectItem>
                  ))
                : pods.map((pod) => (
                    <SelectItem key={pod.name} value={pod.name}>
                      {pod.name}
                      <span className="ml-2 text-muted-foreground">
                        {pod.ready ? t("portfwd.ready") : pod.phase}
                      </span>
                    </SelectItem>
                  ))}
            </SelectContent>
          </Select>
        </Field>

        <Field label={t("portfwd.remotePort")}>
          {ports.length > 0 ? (
            <Select
              value={portKey || undefined}
              disabled={!enabled}
              onValueChange={setPortKey}
            >
              <SelectTrigger className="h-9 w-full">
                <SelectValue placeholder={t("portfwd.selectPort")} />
              </SelectTrigger>
              <SelectContent>
                {ports.map((port) => (
                  <SelectItem key={portKeyOf(port)} value={portKeyOf(port)}>
                    {port.protocol}/{port.port}
                    {port.name ? ` (${port.name})` : ""}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          ) : (
            <Input
              className={inputClassName}
              value={manualRemotePort}
              disabled={!enabled}
              inputMode="numeric"
              onChange={(event) => setManualRemotePort(event.target.value)}
              placeholder="8080"
            />
          )}
        </Field>

        <Field label={t("portfwd.localPort")}>
          <Input
            className={inputClassName}
            value={localPort}
            disabled={!enabled}
            inputMode="numeric"
            onChange={(event) => setLocalPort(event.target.value)}
            placeholder={t("portfwd.localPortAuto")}
          />
        </Field>

        <div className="flex items-end">
          <Button
            type="button"
            size="sm"
            className="h-9 w-full md:w-auto"
            disabled={!enabled || busy || !targetName || !selectedPort}
            onClick={() => void onStart()}
          >
            {busy ? (
              <Spinner data-icon="inline-start" />
            ) : (
              <Plus data-icon="inline-start" />
            )}
            {t("portfwd.start")}
          </Button>
        </div>
      </div>

      {forwards.length === 0 ? (
        <div className="px-4 py-6 text-[12px] text-muted-foreground">
          {enabled ? t("portfwd.emptyReady") : t("portfwd.emptyNoContext")}
        </div>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="bg-muted/40 hover:bg-muted/40">
              <TableHead className="text-[10px] tracking-wide uppercase">
                {t("portfwd.target")}
              </TableHead>
              <TableHead className="text-[10px] tracking-wide uppercase">
                {t("portfwd.mapping")}
              </TableHead>
              <TableHead className="w-[1%] text-[10px] tracking-wide uppercase">
                {t("portfwd.actions")}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {forwards.map((item) => (
              <TableRow key={item.id}>
                <TableCell className="font-medium">
                  {item.kind}/{item.namespace}/{item.name}
                  {item.kind === "service" && item.podName ? (
                    <div className="mt-0.5 font-mono text-[10px] text-muted-foreground">
                      → pod/{item.podName}
                    </div>
                  ) : null}
                </TableCell>
                <TableCell className="font-mono text-[12px] text-muted-foreground">
                  {(item.protocol || "tcp").toUpperCase()} {item.address} → :{item.remotePort}
                </TableCell>
                <TableCell>
                  <div className="flex items-center gap-1.5">
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      disabled={!item.address}
                      onClick={() => void onCopyAddress(item.address)}
                    >
                      <Copy data-icon="inline-start" />
                      {t("portfwd.copyAddress")}
                    </Button>
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      disabled={busy}
                      onClick={() => void onStop(item.id)}
                    >
                      <Trash2 data-icon="inline-start" />
                      {t("portfwd.stop")}
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <ShadcnField className="min-w-0 gap-1.5">
      <FieldLabel className="text-[10px] font-semibold tracking-[0.1em] text-muted-foreground uppercase">
        {label}
      </FieldLabel>
      {children}
    </ShadcnField>
  );
}

function portKeyOf(port: ServicePortInfo) {
  return `${port.protocol || "TCP"}:${port.port}:${port.name || ""}`;
}
