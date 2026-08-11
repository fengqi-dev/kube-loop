import { useEffect, useState, type ReactNode } from "react";
import { Eye, Plus, Trash2 } from "lucide-react";
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
import type { PreviewInfo } from "@/types";

const inputClassName =
  "h-9 w-full rounded-lg border border-input bg-transparent px-2.5 text-sm outline-none transition-colors focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-input/30";

export function PreviewPanel({
  ready,
  namespaces,
  namespace,
  onNamespaceChange,
  embedded = false,
}: {
  ready: boolean;
  namespaces: string[];
  namespace: string;
  onNamespaceChange(value: string): void;
  embedded?: boolean;
}) {
  const { t } = useI18n();
  const [previews, setPreviews] = useState<PreviewInfo[]>([]);
  const [busy, setBusy] = useState(false);
  const [serviceName, setServiceName] = useState("");
  const [servicePort, setServicePort] = useState("8080");
  const [localHost, setLocalHost] = useState("127.0.0.1");
  const [localPort, setLocalPort] = useState("8080");
  const [protocol, setProtocol] = useState("TCP");

  useEffect(() => {
    if (!ready) {
      setPreviews([]);
      return;
    }
    let active = true;
    const refresh = () => {
      backend
        .listPreviews()
        .then((items) => {
          if (active) setPreviews(items);
        })
        .catch((error: Error) => {
          if (active) toast.error(t("preview.loadFailed"), { description: error.message });
        });
    };
    refresh();
    const timer = window.setInterval(refresh, 3000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [ready, t]);

  async function onStart() {
    const parsedServicePort = Number(servicePort);
    const parsedLocalPort = Number(localPort);
    if (!serviceName.trim()) {
      toast.error(t("preview.invalidName"));
      return;
    }
    if (!isValidPort(parsedServicePort)) {
      toast.error(t("preview.invalidServicePort"));
      return;
    }
    if (!isValidPort(parsedLocalPort)) {
      toast.error(t("preview.invalidLocalPort"));
      return;
    }
    setBusy(true);
    try {
      const info = await backend.startPreview({
        namespace,
        name: serviceName.trim(),
        ports: [
          {
            servicePort: parsedServicePort,
            protocol,
            localHost: localHost.trim() || "127.0.0.1",
            localPort: parsedLocalPort,
          },
        ],
      });
      setPreviews(await backend.listPreviews());
      toast.success(t("preview.started"), {
        description: `${info.service}.${info.namespace}.svc (${info.clusterIP})`,
      });
    } catch (error) {
      toast.error(t("preview.startFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setBusy(false);
    }
  }

  async function onStop(id: string) {
    setBusy(true);
    try {
      await backend.stopPreview(id);
      setPreviews(await backend.listPreviews());
      toast.success(t("preview.stopped"));
    } catch (error) {
      toast.error(t("preview.stopFailed"), {
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
              <Eye size={15} className="text-muted-foreground" />
              {t("preview.title")}
            </div>
            <p className="mt-1 text-[11px] text-muted-foreground">
              {t("preview.description")}
            </p>
          </div>
          {!ready && (
            <span className="shrink-0 rounded-md border px-2 py-1 text-[10px] text-muted-foreground">
              {t("preview.connectFirst")}
            </span>
          )}
        </div>
      )}
      {embedded && !ready && (
        <div className="flex justify-end border-b px-4 py-2">
          <span className="rounded-md border px-2 py-1 text-[10px] text-muted-foreground">
            {t("preview.connectFirst")}
          </span>
        </div>
      )}

      <div className="grid gap-3 border-b px-4 py-4 md:grid-cols-[0.9fr_1.2fr_0.8fr_0.7fr_1fr_0.9fr_auto]">
        <NamespaceSelect
          value={namespace}
          namespaces={namespaces}
          disabled={!ready}
          onChange={onNamespaceChange}
        />
        <Field label={t("preview.serviceName")}>
          <Input
            className={inputClassName}
            value={serviceName}
            disabled={!ready}
            onChange={(event) => setServiceName(event.target.value)}
            placeholder="my-local-api"
          />
        </Field>

        <Field label={t("preview.servicePort")}>
          <Input
            className={inputClassName}
            value={servicePort}
            disabled={!ready}
            inputMode="numeric"
            onChange={(event) => setServicePort(event.target.value)}
            placeholder="8080"
          />
        </Field>

        <Field label={t("preview.protocol")}>
          <Select
            value={protocol}
            disabled={!ready}
            onValueChange={setProtocol}
          >
            <SelectTrigger className="h-9 w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="TCP">TCP</SelectItem>
              <SelectItem value="UDP">UDP</SelectItem>
            </SelectContent>
          </Select>
        </Field>

        <Field label={t("preview.localHost")}>
          <Input
            className={inputClassName}
            value={localHost}
            disabled={!ready}
            onChange={(event) => setLocalHost(event.target.value)}
            placeholder="127.0.0.1"
          />
        </Field>

        <Field label={t("preview.localPort")}>
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
            disabled={!ready || busy || !serviceName.trim()}
            onClick={() => void onStart()}
          >
            {busy ? (
              <Spinner data-icon="inline-start" />
            ) : (
              <Plus data-icon="inline-start" />
            )}
            {t("preview.start")}
          </Button>
        </div>
      </div>

      {previews.length === 0 ? (
        <div className="px-4 py-6 text-[12px] text-muted-foreground">
          {ready ? t("preview.emptyReady") : t("preview.emptyDisconnected")}
        </div>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="bg-muted/40 hover:bg-muted/40">
              <TableHead className="text-[10px] tracking-wide uppercase">
                {t("preview.service")}
              </TableHead>
              <TableHead className="text-[10px] tracking-wide uppercase">
                {t("preview.clusterIP")}
              </TableHead>
              <TableHead className="text-[10px] tracking-wide uppercase">
                {t("preview.mapping")}
              </TableHead>
              <TableHead className="w-[1%] text-[10px] tracking-wide uppercase">
                {t("preview.actions")}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {previews.map((item) => (
              <TableRow key={item.id}>
                <TableCell className="font-medium">
                  <div>{item.namespace}/{item.service}</div>
                  <div className="mt-0.5 font-mono text-[10px] text-muted-foreground">
                    {item.service}.{item.namespace}.svc.cluster.local
                  </div>
                </TableCell>
                <TableCell className="font-mono text-[12px] text-primary">
                  {item.clusterIP || "—"}
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
                    {t("preview.stop")}
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
