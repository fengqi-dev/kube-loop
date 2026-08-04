import { useEffect, useMemo, useRef, useState } from "react";
import { Circle, Network } from "lucide-react";
import { toast } from "sonner";
import { backend } from "@/backend";
import {
  ActionIconButton,
  portForwardIcon,
} from "@/components/network/action-icons";
import { ActiveSessions } from "@/components/network/active-sessions";
import { PortForwardDialog } from "@/components/network/portfwd-dialog";
import {
  ALL_NAMESPACES,
  ResourceToolbar,
} from "@/components/network/resource-toolbar";
import { CopyableText } from "@/components/shared/copyable-text";
import { EmptyState } from "@/components/shared/empty-state";
import { PageShell } from "@/components/shared/page-shell";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useI18n } from "@/i18n";
import { cn } from "@/lib/utils";
import type { PodInfo, SessionState } from "@/types";

export function WorkloadView({
  contextName,
  namespaces,
  namespaceScoped = false,
  ready,
  session,
}: {
  contextName: string;
  namespaces: string[];
  namespaceScoped?: boolean;
  ready: boolean;
  session: SessionState;
}) {
  const { t } = useI18n();
  const [namespace, setNamespace] = useState(
    namespaceScoped && namespaces[0] ? namespaces[0] : ALL_NAMESPACES,
  );
  const [query, setQuery] = useState("");
  const [pods, setPods] = useState<PodInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [selected, setSelected] = useState<PodInfo | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const reloadGeneration = useRef(0);

  const connected = session.phase === "connected";
  const livePods = session.pods;

  useEffect(() => {
    setNamespace(
      namespaceScoped && namespaces[0] ? namespaces[0] : ALL_NAMESPACES,
    );
    setQuery("");
    setSelected(null);
    setDialogOpen(false);
  }, [contextName]);

  useEffect(() => {
    const fallback =
      namespaceScoped && namespaces[0] ? namespaces[0] : ALL_NAMESPACES;
    if (
      (namespaceScoped &&
        (namespace === ALL_NAMESPACES || !namespaces.includes(namespace))) ||
      (!namespaceScoped &&
        namespace !== ALL_NAMESPACES &&
        !namespaces.includes(namespace))
    ) {
      setNamespace(fallback);
    }
  }, [namespace, namespaceScoped, namespaces]);

  async function reload() {
    const generation = ++reloadGeneration.current;
    if (!contextName) {
      setPods([]);
      return;
    }
    if (connected && livePods) {
      setPods(livePods);
      return;
    }
    setLoading(true);
    try {
      const items = await backend.listPods(contextName, namespace);
      if (generation === reloadGeneration.current) setPods(items);
    } catch (error) {
      if (generation === reloadGeneration.current) {
        toast.error(t("workload.loadFailed"), {
          description: error instanceof Error ? error.message : String(error),
        });
      }
    } finally {
      if (generation === reloadGeneration.current) setLoading(false);
    }
  }

  useEffect(() => {
    if (connected && livePods) {
      reloadGeneration.current += 1;
      setPods(livePods);
      setLoading(false);
      return;
    }
    void reload();
    return () => {
      reloadGeneration.current += 1;
    };
  }, [connected, contextName, livePods, namespace]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return pods.filter((item) => {
      if (namespace !== ALL_NAMESPACES && item.namespace !== namespace) {
        return false;
      }
      if (!q) return true;
      return (
        item.name.toLowerCase().includes(q) ||
        item.namespace.toLowerCase().includes(q) ||
        (item.ip ?? "").toLowerCase().includes(q) ||
        (item.node ?? "").toLowerCase().includes(q)
      );
    });
  }, [namespace, pods, query]);

  return (
    <PageShell title={t("workload.title")} description={t("workload.description")}>
      <ResourceToolbar
        namespaces={namespaces}
        namespace={namespace}
        onNamespaceChange={setNamespace}
        query={query}
        onQueryChange={setQuery}
        searchPlaceholder={t("workload.search")}
        count={filtered.length}
        loading={loading}
        disabled={!contextName}
        onRefresh={() => void reload()}
        allowAllNamespaces={!namespaceScoped}
      />

      {!contextName ? (
        <EmptyState
          icon={Network}
          title={t("network.waitingTitle")}
          detail={t("network.selectContext")}
        />
      ) : (
        <div className="overflow-hidden rounded-lg border bg-card">
          {!loading && filtered.length === 0 ? (
            <div className="px-4 py-12 text-center text-[12px] text-muted-foreground">
              {t("workload.empty")}
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="h-9 w-40 min-w-40 max-w-40 text-[11px] font-medium text-muted-foreground">
                    {t("network.colName")}
                  </TableHead>
                  <TableHead className="h-9 text-[11px] font-medium text-muted-foreground">
                    {t("network.colNamespace")}
                  </TableHead>
                  <TableHead className="h-9 text-[11px] font-medium text-muted-foreground">
                    {t("network.colIP")}
                  </TableHead>
                  <TableHead className="h-9 text-[11px] font-medium text-muted-foreground">
                    {t("network.colStatus")}
                  </TableHead>
                  <TableHead className="h-9 text-[11px] font-medium text-muted-foreground">
                    {t("network.colNode")}
                  </TableHead>
                  <TableHead className="h-9 text-[11px] font-medium text-muted-foreground">
                    {t("network.colPorts")}
                  </TableHead>
                  <TableHead className="h-9 text-[11px] font-medium text-muted-foreground">
                    {t("network.actions")}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filtered.map((item) => (
                  <TableRow key={`${item.namespace}/${item.name}`}>
                    <TableCell className="w-40 min-w-40 max-w-40 font-medium">
                      <span className="block truncate" title={item.name}>
                        {item.name}
                      </span>
                    </TableCell>
                    <TableCell className="text-primary">{item.namespace}</TableCell>
                    <TableCell className="font-mono text-[12px]">
                      <CopyableText value={item.ip} />
                    </TableCell>
                    <TableCell>
                      <StatusPill
                        ok={item.ready}
                        label={item.ready ? t("network.ready") : item.phase || t("network.notReady")}
                      />
                    </TableCell>
                    <TableCell className="text-muted-foreground">{item.node || "—"}</TableCell>
                    <TableCell className="font-mono text-[12px] text-muted-foreground">
                      {item.ports.length > 0 ? (
                        <div className="flex flex-col gap-0.5">
                          {item.ports.map((port) => (
                            <CopyableText
                              key={`${port.protocol}-${port.port}-${port.name || ""}`}
                              value={item.ip ? `${item.ip}:${port.port}` : null}
                              label={`${port.protocol}/${port.port}`}
                              titleKey="network.copyAddress"
                              successKey="network.addressCopied"
                              failKey="network.addressCopyFailed"
                              empty={`${port.protocol}/${port.port}`}
                            />
                          ))}
                        </div>
                      ) : (
                        "—"
                      )}
                    </TableCell>
                    <TableCell>
                      <ActionIconButton
                        label={t("network.tabPortForward")}
                        icon={portForwardIcon}
                        disabled={!ready}
                        onClick={() => {
                          setSelected(item);
                          setDialogOpen(true);
                        }}
                      />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
      )}

      <ActiveSessions
        contextName={contextName}
        ready={ready}
        refreshKey={refreshKey}
        inventoryRevision={session.inventoryRevision}
        scope="podPortForward"
      />

      <PortForwardDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        contextName={contextName}
        kind="pod"
        target={selected}
        onStarted={() => setRefreshKey((value) => value + 1)}
      />
    </PageShell>
  );
}

function StatusPill({ ok, label }: { ok: boolean; label: string }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 text-[12px]",
        ok ? "text-success" : "text-muted-foreground",
      )}
    >
      <Circle
        size={8}
        className={ok ? "fill-success text-success" : "fill-muted-foreground/50 text-muted-foreground/50"}
      />
      {label}
    </span>
  );
}
