import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { backend } from "@/backend";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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
import { useI18n } from "@/i18n";
import { isValidPort } from "@/lib/validation";
import type { PodInfo, ServiceInfo, ServicePortInfo } from "@/types";

const inputClassName =
  "h-9 w-full rounded-lg border border-input bg-transparent px-2.5 text-sm outline-none transition-colors focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-input/30";

function portKeyOf(port: ServicePortInfo) {
  return `${port.protocol || "TCP"}:${port.port}:${port.name || ""}`;
}

export function PortForwardDialog({
  open,
  onOpenChange,
  contextName,
  kind,
  target,
  onStarted,
}: {
  open: boolean;
  onOpenChange(open: boolean): void;
  contextName: string;
  kind: "pod" | "service";
  target: PodInfo | ServiceInfo | null;
  onStarted(): void;
}) {
  const { t } = useI18n();
  const [portKey, setPortKey] = useState("");
  const [manualRemotePort, setManualRemotePort] = useState("");
  const [localPort, setLocalPort] = useState("");
  const [busy, setBusy] = useState(false);

  const ports = useMemo(() => {
    if (!target) return [] as ServicePortInfo[];
    return target.ports.map((port) => ({
      name: port.name,
      port: port.port,
      protocol: port.protocol,
    }));
  }, [target]);

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
    if (!open) return;
    setLocalPort("");
    setManualRemotePort("");
    if (ports.length > 0) {
      setPortKey(portKeyOf(ports[0]));
    } else {
      setPortKey("");
    }
  }, [open, ports, target]);

  async function onStart() {
    if (!target || !selectedPort) return;
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
        namespace: target.namespace,
        kind,
        name: target.name,
        protocol: selectedPort.protocol,
        remotePort: selectedPort.port,
        localPort: parsedLocal,
      });
      try {
        await navigator.clipboard.writeText(info.address);
        toast.success(t("portfwd.started"), {
          description: `${info.address} → ${kind}/${target.name}:${selectedPort.port} · ${t("portfwd.copied")}`,
        });
      } catch {
        toast.success(t("portfwd.started"), {
          description: `${info.address} → ${kind}/${target.name}:${selectedPort.port}`,
        });
      }
      onStarted();
      onOpenChange(false);
    } catch (error) {
      toast.error(t("portfwd.startFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!busy) onOpenChange(next);
      }}
    >
      <DialogContent showCloseButton={!busy}>
        <DialogHeader>
          <DialogTitle>{t("portfwd.title")}</DialogTitle>
          <DialogDescription>
            {target
              ? `${kind}/${target.namespace}/${target.name}`
              : t("portfwd.description")}
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-3">
          <Field label={t("portfwd.remotePort")}>
            {ports.length > 0 ? (
              <Select value={portKey} onValueChange={setPortKey}>
                <SelectTrigger className="h-9 w-full">
                  <SelectValue />
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
                onChange={(event) => setManualRemotePort(event.target.value)}
                placeholder="8080"
                inputMode="numeric"
              />
            )}
          </Field>
          <Field label={t("portfwd.localPort")}>
            <Input
              className={inputClassName}
              value={localPort}
              onChange={(event) => setLocalPort(event.target.value)}
              placeholder={t("portfwd.localPortAuto")}
              inputMode="numeric"
            />
          </Field>
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            disabled={busy}
            onClick={() => onOpenChange(false)}
          >
            {t("actions.cancel")}
          </Button>
          <Button
            type="button"
            disabled={!target || !selectedPort || busy}
            onClick={() => void onStart()}
          >
            {busy && <Spinner data-icon="inline-start" />}
            {t("portfwd.start")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <ShadcnField className="gap-1.5">
      <FieldLabel className="text-[12px] text-muted-foreground">{label}</FieldLabel>
      {children}
    </ShadcnField>
  );
}
