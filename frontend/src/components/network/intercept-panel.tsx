import { useEffect, useMemo, useState, type ReactNode } from "react";
import { ArrowRightLeft, Plus, Trash2 } from "lucide-react";
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
import type { InterceptInfo, ServiceInfo, ServicePortInfo } from "@/types";

const inputClassName =
  "h-9 w-full rounded-lg border border-input bg-transparent px-2.5 text-sm outline-none transition-colors focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-input/30";

export function InterceptPanel({
  ready,
  contextName,
  namespaces,
  namespace,
  onNamespaceChange,
  embedded = false,
}: {
  ready: boolean;
  contextName: string;
  namespaces: string[];
  namespace: string;
  onNamespaceChange(value: string): void;
  embedded?: boolean;
}) {
  const { t } = useI18n();
  const [services, setServices] = useState<ServiceInfo[]>([]);
  const [intercepts, setIntercepts] = useState<InterceptInfo[]>([]);
  const [loadingServices, setLoadingServices] = useState(false);
  const [busy, setBusy] = useState(false);
  const [serviceName, setServiceName] = useState("");
  const [portKey, setPortKey] = useState("");
  const [localHost, setLocalHost] = useState("127.0.0.1");
  const [localPort, setLocalPort] = useState("");

  const selectedService = useMemo(
    () => services.find((item) => item.name === serviceName),
    [serviceName, services],
  );
  const selectedPort = useMemo(() => {
    if (!selectedService) return undefined;
    return selectedService.ports.find((port) => portKeyOf(port) === portKey);
  }, [portKey, selectedService]);

  useEffect(() => {
    if (!ready || !contextName) {
      setServices([]);
      setIntercepts([]);
      setServiceName("");
      setPortKey("");
      return;
    }
    let active = true;
    setLoadingServices(true);
    backend
      .listServices(contextName, namespace)
      .then((items) => {
        if (!active) return;
        setServices(items);
        if (items.length > 0) {
          setServiceName((current) =>
            items.some((item) => item.name === current) ? current : items[0].name,
          );
        } else {
          setServiceName("");
        }
      })
      .catch((error: Error) => {
        if (active) toast.error(t("intercept.loadFailed"), { description: error.message });
      })
      .finally(() => {
        if (active) setLoadingServices(false);
      });
    backend
      .listIntercepts()
      .then((items) => {
        if (active) setIntercepts(items);
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [contextName, namespace, ready, t]);

  useEffect(() => {
    if (!selectedService || selectedService.ports.length === 0) {
      setPortKey("");
      setLocalPort("");
      return;
    }
    const next = selectedService.ports[0];
    setPortKey(portKeyOf(next));
    setLocalPort(String(next.port));
  }, [selectedService]);

  async function refreshIntercepts() {
    const items = await backend.listIntercepts();
    setIntercepts(items);
  }

  async function onStart() {
    if (!selectedService || !selectedPort) return;
    const parsedLocal = Number(localPort);
    if (!isValidPort(parsedLocal)) {
      toast.error(t("intercept.invalidLocalPort"));
      return;
    }
    setBusy(true);
    try {
      await backend.startIntercept({
        namespace,
        service: selectedService.name,
        ports: [
          {
            servicePort: selectedPort.port,
            protocol: selectedPort.protocol || "TCP",
            localHost: localHost.trim() || "127.0.0.1",
            localPort: parsedLocal,
          },
        ],
      });
      await refreshIntercepts();
      toast.success(t("intercept.started"), {
        description: `${selectedService.name}:${selectedPort.port} → ${localHost}:${parsedLocal}`,
      });
    } catch (error) {
      toast.error(t("intercept.startFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setBusy(false);
    }
  }

  async function onStop(id: string) {
    setBusy(true);
    try {
      await backend.stopIntercept(id);
      await refreshIntercepts();
      toast.success(t("intercept.stopped"));
    } catch (error) {
      toast.error(t("intercept.stopFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <section
      className={`overflow-hidden rounded-lg border bg-card ${embedded ? "" : "mt-5"}`}
    >
      {!embedded && (
        <div className="flex items-start justify-between gap-3 border-b px-4 py-3">
          <div>
            <div className="flex items-center gap-2 text-[13px] font-semibold">
              <ArrowRightLeft size={15} className="text-muted-foreground" />
              {t("intercept.title")}
            </div>
            <p className="mt-1 text-[11px] text-muted-foreground">
              {t("intercept.description")}
            </p>
          </div>
          {!ready && (
            <span className="shrink-0 rounded-md border px-2 py-1 text-[10px] text-muted-foreground">
              {t("intercept.connectFirst")}
            </span>
          )}
        </div>
      )}
      {embedded && !ready && (
        <div className="flex justify-end border-b px-4 py-2">
          <span className="rounded-md border px-2 py-1 text-[10px] text-muted-foreground">
            {t("intercept.connectFirst")}
          </span>
        </div>
      )}

      <div className="grid gap-3 border-b px-4 py-4 md:grid-cols-[0.9fr_1.2fr_1fr_1fr_0.9fr_auto]">
        <NamespaceSelect
          value={namespace}
          namespaces={namespaces}
          disabled={!ready}
          onChange={onNamespaceChange}
        />
        <Field label={t("intercept.service")}>
          <Select
            value={serviceName || undefined}
            disabled={!ready || loadingServices || services.length === 0}
            onValueChange={setServiceName}
          >
            <SelectTrigger className="h-9 w-full">
              <SelectValue
                placeholder={
                  loadingServices
                    ? t("intercept.loadingServices")
                    : t("intercept.selectService")
                }
              />
            </SelectTrigger>
            <SelectContent>
              {services.map((service) => (
                <SelectItem key={service.name} value={service.name}>
                  {service.name}
                  <span className="ml-2 text-muted-foreground">{service.clusterIP}</span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>

        <Field label={t("intercept.servicePort")}>
          <Select
            value={portKey || undefined}
            disabled={!ready || !selectedService}
            onValueChange={(value) => {
              setPortKey(value);
              const port = selectedService?.ports.find((item) => portKeyOf(item) === value);
              if (port) setLocalPort(String(port.port));
            }}
          >
            <SelectTrigger className="h-9 w-full">
              <SelectValue placeholder={t("intercept.selectPort")} />
            </SelectTrigger>
            <SelectContent>
              {(selectedService?.ports ?? []).map((port) => (
                <SelectItem key={portKeyOf(port)} value={portKeyOf(port)}>
                  {port.protocol}/{port.port}
                  {port.name ? ` (${port.name})` : ""}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>

        <Field label={t("intercept.localHost")}>
          <Input
            className={inputClassName}
            value={localHost}
            disabled={!ready}
            onChange={(event) => setLocalHost(event.target.value)}
            placeholder="127.0.0.1"
          />
        </Field>

        <Field label={t("intercept.localPort")}>
          <Input
            className={inputClassName}
            value={localPort}
            disabled={!ready}
            inputMode="numeric"
            onChange={(event) => setLocalPort(event.target.value)}
            placeholder="8080"
          />
        </Field>

        <div className="flex items-end">
          <Button
            type="button"
            size="sm"
            className="h-9 w-full md:w-auto"
            disabled={!ready || busy || !selectedService || !selectedPort}
            onClick={() => void onStart()}
          >
            {busy ? <Spinner data-icon="inline-start" /> : <Plus data-icon="inline-start" />}
            {t("intercept.start")}
          </Button>
        </div>
      </div>

      {intercepts.length === 0 ? (
        <div className="px-4 py-6 text-[12px] text-muted-foreground">
          {ready ? t("intercept.emptyReady") : t("intercept.emptyDisconnected")}
        </div>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="bg-muted/40 hover:bg-muted/40">
              <TableHead className="text-[10px] tracking-wide uppercase">
                {t("intercept.service")}
              </TableHead>
              <TableHead className="text-[10px] tracking-wide uppercase">
                {t("intercept.mapping")}
              </TableHead>
              <TableHead className="w-[1%] text-[10px] tracking-wide uppercase">
                {t("intercept.actions")}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {intercepts.map((item) => (
              <TableRow key={item.id}>
                <TableCell className="font-medium">
                  {item.namespace}/{item.service}
                </TableCell>
                <TableCell className="font-mono text-[12px] text-muted-foreground">
                  {item.locals
                    .map(
                      (local, index) =>
                        `${item.ports[index]?.protocol ?? local.protocol}:${local.servicePort} → ${local.localHost}:${local.localPort}`,
                    )
                    .join(", ")}
                </TableCell>
                <TableCell>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={busy}
                    onClick={() => void onStop(item.id)}
                  >
                    <Trash2 data-icon="inline-start" />
                    {t("intercept.stop")}
                  </Button>
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
