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
import type { ServiceInfo, ServicePortInfo } from "@/types";

const inputClassName =
  "h-9 w-full rounded-lg border border-input bg-transparent px-2.5 text-sm outline-none transition-colors focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-input/30";

function portKeyOf(port: ServicePortInfo) {
  return `${port.protocol || "TCP"}:${port.port}:${port.name || ""}`;
}

export function MirrorDialog({
  open,
  onOpenChange,
  service,
  onStarted,
}: {
  open: boolean;
  onOpenChange(open: boolean): void;
  service: ServiceInfo | null;
  onStarted(): void;
}) {
  const { t } = useI18n();
  const [portKey, setPortKey] = useState("");
  const [localHost, setLocalHost] = useState("127.0.0.1");
  const [localPort, setLocalPort] = useState("");
  const [busy, setBusy] = useState(false);

  const selectedPort = useMemo(() => {
    if (!service) return undefined;
    return service.ports.find((port) => portKeyOf(port) === portKey);
  }, [portKey, service]);

  useEffect(() => {
    if (!open || !service || service.ports.length === 0) {
      setPortKey("");
      setLocalPort("");
      return;
    }
    const next = service.ports[0];
    setPortKey(portKeyOf(next));
    setLocalPort(String(next.port));
    setLocalHost("127.0.0.1");
  }, [open, service]);

  async function onStart() {
    if (!service || !selectedPort) return;
    const parsedLocal = Number(localPort);
    if (!isValidPort(parsedLocal)) {
      toast.error(t("mirror.invalidLocalPort"));
      return;
    }
    setBusy(true);
    try {
      await backend.startMirror({
        namespace: service.namespace,
        service: service.name,
        ports: [
          {
            servicePort: selectedPort.port,
            protocol: selectedPort.protocol || "TCP",
            localHost: localHost.trim() || "127.0.0.1",
            localPort: parsedLocal,
          },
        ],
      });
      toast.success(t("mirror.started"), {
        description: `${service.name}:${selectedPort.port} → ${localHost}:${parsedLocal}`,
      });
      onStarted();
      onOpenChange(false);
    } catch (error) {
      toast.error(t("mirror.startFailed"), {
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
          <DialogTitle>{t("mirror.title")}</DialogTitle>
          <DialogDescription>
            {service
              ? `${service.namespace}/${service.name}`
              : t("mirror.description")}
          </DialogDescription>
        </DialogHeader>

        <p className="text-[12px] text-muted-foreground">{t("mirror.description")}</p>

        <div className="grid gap-3">
          <Field label={t("mirror.servicePort")}>
            <Select
              value={portKey}
              onValueChange={setPortKey}
              disabled={!service || !service.ports.length}
            >
              <SelectTrigger className="h-9 w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(service?.ports ?? []).map((port) => (
                  <SelectItem key={portKeyOf(port)} value={portKeyOf(port)}>
                    {port.protocol || "TCP"}/{port.port}
                    {port.name ? ` (${port.name})` : ""}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field label={t("mirror.localHost")}>
            <Input
              className={inputClassName}
              value={localHost}
              onChange={(event) => setLocalHost(event.target.value)}
            />
          </Field>
          <Field label={t("mirror.localPort")}>
            <Input
              className={inputClassName}
              value={localPort}
              onChange={(event) => setLocalPort(event.target.value)}
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
            disabled={!service || !selectedPort || busy}
            onClick={() => void onStart()}
          >
            {busy && <Spinner data-icon="inline-start" />}
            {t("mirror.start")}
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
