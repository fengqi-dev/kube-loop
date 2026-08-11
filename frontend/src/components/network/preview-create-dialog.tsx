import { useEffect, useState } from "react";
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

const inputClassName =
  "h-9 w-full rounded-lg border border-input bg-transparent px-2.5 text-sm outline-none transition-colors focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-input/30";

export function PreviewCreateDialog({
  open,
  onOpenChange,
  namespaces,
  defaultNamespace,
  onStarted,
}: {
  open: boolean;
  onOpenChange(open: boolean): void;
  namespaces: string[];
  defaultNamespace: string;
  onStarted(): void;
}) {
  const { t } = useI18n();
  const [namespace, setNamespace] = useState("default");
  const [name, setName] = useState("");
  const [servicePort, setServicePort] = useState("8080");
  const [protocol, setProtocol] = useState("TCP");
  const [localHost, setLocalHost] = useState("127.0.0.1");
  const [localPort, setLocalPort] = useState("8080");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) return;
    setNamespace(defaultNamespace && defaultNamespace !== "*" ? defaultNamespace : "default");
    setName("");
    setServicePort("8080");
    setProtocol("TCP");
    setLocalHost("127.0.0.1");
    setLocalPort("8080");
  }, [defaultNamespace, open]);

  async function onStart() {
    const trimmed = name.trim();
    if (!trimmed) {
      toast.error(t("preview.invalidName"));
      return;
    }
    const parsedServicePort = Number(servicePort);
    if (!isValidPort(parsedServicePort)) {
      toast.error(t("preview.invalidServicePort"));
      return;
    }
    const parsedLocal = Number(localPort);
    if (!isValidPort(parsedLocal)) {
      toast.error(t("preview.invalidLocalPort"));
      return;
    }
    setBusy(true);
    try {
      await backend.startPreview({
        namespace,
        name: trimmed,
        ports: [
          {
            servicePort: parsedServicePort,
            protocol,
            localHost: localHost.trim() || "127.0.0.1",
            localPort: parsedLocal,
          },
        ],
      });
      toast.success(t("preview.started"), {
        description: `${namespace}/${trimmed}:${parsedServicePort}`,
      });
      onStarted();
      onOpenChange(false);
    } catch (error) {
      toast.error(t("preview.startFailed"), {
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
          <DialogTitle>{t("preview.title")}</DialogTitle>
          <DialogDescription>{t("preview.description")}</DialogDescription>
        </DialogHeader>

        <div className="grid gap-3">
          <Field label={t("network.namespace")}>
            <Select value={namespace} onValueChange={setNamespace}>
              <SelectTrigger className="h-9 w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(namespaces.length > 0 ? namespaces : ["default"]).map((item) => (
                  <SelectItem key={item} value={item}>
                    {item}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field label={t("preview.serviceName")}>
            <Input
              className={inputClassName}
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="my-local-svc"
            />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label={t("preview.servicePort")}>
              <Input
                className={inputClassName}
                value={servicePort}
                onChange={(event) => setServicePort(event.target.value)}
                inputMode="numeric"
              />
            </Field>
            <Field label={t("preview.protocol")}>
              <Select value={protocol} onValueChange={setProtocol}>
                <SelectTrigger className="h-9 w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="TCP">TCP</SelectItem>
                  <SelectItem value="UDP">UDP</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          </div>
          <Field label={t("preview.localHost")}>
            <Input
              className={inputClassName}
              value={localHost}
              onChange={(event) => setLocalHost(event.target.value)}
            />
          </Field>
          <Field label={t("preview.localPort")}>
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
          <Button type="button" disabled={busy} onClick={() => void onStart()}>
            {busy && <Spinner data-icon="inline-start" />}
            {t("preview.start")}
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
