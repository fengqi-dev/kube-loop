import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { Network } from "lucide-react";
import { toast } from "sonner";
import { backend } from "@/backend";
import {
  ActionIconButton,
  exchangeIcon,
  mirrorIcon,
  portForwardIcon,
  previewIcon,
} from "@/components/network/action-icons";
import { ActiveSessions } from "@/components/network/active-sessions";
import { ExchangeDialog } from "@/components/network/exchange-dialog";
import { MirrorDialog } from "@/components/network/mirror-dialog";
import { PortForwardDialog } from "@/components/network/portfwd-dialog";
import { PreviewCreateDialog } from "@/components/network/preview-create-dialog";
import {
  ALL_NAMESPACES,
  ResourceToolbar,
} from "@/components/network/resource-toolbar";
import { CopyableText } from "@/components/shared/copyable-text";
import { EmptyState } from "@/components/shared/empty-state";
import { PageShell } from "@/components/shared/page-shell";
import {
  ResourcePagination,
  RESOURCE_PAGE_SIZE,
} from "@/components/shared/resource-pagination";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useI18n } from "@/i18n";
import type { ServiceInfo, SessionState } from "@/types";

type ServiceBinding = "exchange" | "mirror" | "preview" | "portForward" | "idle";

const NetworkTrafficChart = lazy(() =>
  import("@/components/network/network-traffic-chart").then((module) => ({
    default: module.NetworkTrafficChart,
  })),
);

export function NetworkView({
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
  const [page, setPage] = useState(1);
  const [services, setServices] = useState<ServiceInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [pfOpen, setPfOpen] = useState(false);
  const [exOpen, setExOpen] = useState(false);
  const [mirrorOpen, setMirrorOpen] = useState(false);
  const [previewOpen, setPreviewOpen] = useState(false);
  const [selected, setSelected] = useState<ServiceInfo | null>(null);
  const [bindings, setBindings] = useState<Record<string, ServiceBinding>>({});
  const reloadGeneration = useRef(0);

  const connected = session.phase === "connected";
  const liveServices = session.services;
  const canExchange = session.capabilities?.serviceWrite !== false;
  const canPreview = session.capabilities?.serviceCreate !== false;

  useEffect(() => {
    setNamespace(
      namespaceScoped && namespaces[0] ? namespaces[0] : ALL_NAMESPACES,
    );
    setQuery("");
    setSelected(null);
    setPfOpen(false);
    setExOpen(false);
    setMirrorOpen(false);
    setPreviewOpen(false);
    setBindings({});
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
      setServices([]);
      return;
    }
    if (connected && liveServices) {
      setServices(liveServices);
      return;
    }
    setLoading(true);
    try {
      const items = await backend.listServices(contextName, namespace);
      if (generation === reloadGeneration.current) setServices(items);
    } catch (error) {
      if (generation === reloadGeneration.current) {
        toast.error(t("network.loadFailed"), {
          description: error instanceof Error ? error.message : String(error),
        });
      }
    } finally {
      if (generation === reloadGeneration.current) setLoading(false);
    }
  }

  useEffect(() => {
    if (connected && liveServices) {
      reloadGeneration.current += 1;
      setServices(liveServices);
      setLoading(false);
      return;
    }
    void reload();
    return () => {
      reloadGeneration.current += 1;
    };
  }, [connected, contextName, liveServices, namespace]);

  useEffect(() => {
    let active = true;
    Promise.all([
      backend.listPortForwards(),
      ready ? backend.listIntercepts() : Promise.resolve([]),
      ready ? backend.listMirrors() : Promise.resolve([]),
      ready ? backend.listPreviews() : Promise.resolve([]),
    ])
      .then(([forwards, exchanges, mirrors, previews]) => {
        if (!active) return;
        const next: Record<string, ServiceBinding> = {};
        for (const item of forwards) {
          if (item.context !== contextName || item.kind !== "service") continue;
          next[`${item.namespace}/${item.name}`] = "portForward";
        }
        for (const item of exchanges) {
          next[`${item.namespace}/${item.service}`] = "exchange";
        }
        for (const item of mirrors) {
          next[`${item.namespace}/${item.service}`] = "mirror";
        }
        for (const item of previews) {
          next[`${item.namespace}/${item.service}`] = "preview";
        }
        setBindings(next);
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [contextName, ready, refreshKey, session.inventoryRevision]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return services.filter((item) => {
      if (namespace !== ALL_NAMESPACES && item.namespace !== namespace) {
        return false;
      }
      if (!q) return true;
      return (
        item.name.toLowerCase().includes(q) ||
        item.namespace.toLowerCase().includes(q) ||
        item.clusterIP.toLowerCase().includes(q)
      );
    });
  }, [namespace, query, services]);
  const pageCount = Math.max(1, Math.ceil(filtered.length / RESOURCE_PAGE_SIZE));
  const visibleServices = filtered.slice(
    (page - 1) * RESOURCE_PAGE_SIZE,
    page * RESOURCE_PAGE_SIZE,
  );

  useEffect(() => {
    setPage(1);
  }, [contextName, namespace, query]);

  useEffect(() => {
    setPage((current) => Math.min(current, pageCount));
  }, [pageCount]);

  function bumpActive() {
    setRefreshKey((value) => value + 1);
  }

  return (
    <PageShell title={t("network.title")} description={t("network.description")}>
      <ResourceToolbar
        namespaces={namespaces}
        namespace={namespace}
        onNamespaceChange={setNamespace}
        query={query}
        onQueryChange={setQuery}
        searchPlaceholder={t("network.searchServices")}
        count={filtered.length}
        loading={loading}
        disabled={!contextName}
        onRefresh={() => void reload()}
        allowAllNamespaces={!namespaceScoped}
        actions={
          <ActionIconButton
            label={
              canPreview ? t("network.createPreview") : t("network.previewDenied")
            }
            icon={previewIcon}
            disabled={!ready || !canPreview}
            onClick={() => setPreviewOpen(true)}
          />
        }
      />

      {contextName ? (
        <Suspense
          fallback={
            <div className="mb-4 h-[218px] animate-pulse rounded-2xl border border-border/80 bg-card/70" />
          }
        >
          <NetworkTrafficChart ready={ready} metrics={session.metrics} />
        </Suspense>
      ) : null}

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
              {t("network.emptyServices")}
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
                    {t("network.colClusterIP")}
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
                {visibleServices.map((item) => (
                  <TableRow key={`${item.namespace}/${item.name}`}>
                    <TableCell className="w-40 min-w-40 max-w-40 font-medium">
                      <span className="block truncate" title={item.name}>
                        {item.name}
                      </span>
                    </TableCell>
                    <TableCell className="text-primary">{item.namespace}</TableCell>
                    <TableCell className="font-mono text-[12px]">
                      <CopyableText value={item.clusterIP} />
                    </TableCell>
                    <TableCell className="font-mono text-[12px] text-muted-foreground">
                      {item.ports.length > 0 ? (
                        <div className="flex flex-col gap-0.5">
                          {item.ports.map((port) => (
                            <CopyableText
                              key={`${port.protocol}-${port.port}-${port.name || ""}`}
                              value={
                                item.clusterIP ? `${item.clusterIP}:${port.port}` : null
                              }
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
                      <div className="flex items-center gap-1">
                        <ActionIconButton
                          label={t("network.tabPortForward")}
                          icon={portForwardIcon}
                          disabled={!ready}
                          onClick={() => {
                            setSelected(item);
                            setPfOpen(true);
                          }}
                        />
                        <ActionIconButton
                          label={exchangeActionLabel(
                            t,
                            canExchange,
                            bindings[`${item.namespace}/${item.name}`] ?? "idle",
                          )}
                          icon={exchangeIcon}
                          disabled={
                            !ready ||
                            !canExchange ||
                            isServiceExclusiveBound(
                              bindings[`${item.namespace}/${item.name}`] ?? "idle",
                            )
                          }
                          onClick={() => {
                            setSelected(item);
                            setExOpen(true);
                          }}
                        />
                        <ActionIconButton
                          label={mirrorActionLabel(
                            t,
                            canExchange,
                            bindings[`${item.namespace}/${item.name}`] ?? "idle",
                          )}
                          icon={mirrorIcon}
                          disabled={
                            !ready ||
                            !canExchange ||
                            isServiceExclusiveBound(
                              bindings[`${item.namespace}/${item.name}`] ?? "idle",
                            )
                          }
                          onClick={() => {
                            setSelected(item);
                            setMirrorOpen(true);
                          }}
                        />
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          <ResourcePagination
            page={page}
            total={filtered.length}
            onPageChange={setPage}
          />
        </div>
      )}

      <ActiveSessions
        contextName={contextName}
        ready={ready}
        refreshKey={refreshKey}
        inventoryRevision={session.inventoryRevision}
      />

      <PortForwardDialog
        open={pfOpen}
        onOpenChange={setPfOpen}
        contextName={contextName}
        kind="service"
        target={selected}
        onStarted={bumpActive}
      />
      <ExchangeDialog
        open={exOpen}
        onOpenChange={setExOpen}
        service={selected}
        onStarted={bumpActive}
      />
      <MirrorDialog
        open={mirrorOpen}
        onOpenChange={setMirrorOpen}
        service={selected}
        onStarted={bumpActive}
      />
      <PreviewCreateDialog
        open={previewOpen}
        onOpenChange={setPreviewOpen}
        namespaces={namespaces}
        defaultNamespace={namespace}
        onStarted={bumpActive}
      />
    </PageShell>
  );
}

function isServiceExclusiveBound(binding: ServiceBinding) {
  return binding === "exchange" || binding === "mirror" || binding === "preview";
}

function exchangeActionLabel(
  t: ReturnType<typeof useI18n>["t"],
  canExchange: boolean,
  binding: ServiceBinding,
) {
  if (!canExchange) return t("network.exchangeDenied");
  if (binding === "mirror") return t("network.exchangeBlockedByMirror");
  if (binding === "exchange") return t("network.exchangeAlreadyActive");
  if (binding === "preview") return t("network.exchangeBlockedByPreview");
  return t("network.tabExchange");
}

function mirrorActionLabel(
  t: ReturnType<typeof useI18n>["t"],
  canExchange: boolean,
  binding: ServiceBinding,
) {
  if (!canExchange) return t("network.mirrorDenied");
  if (binding === "exchange") return t("network.mirrorBlockedByExchange");
  if (binding === "mirror") return t("network.mirrorAlreadyActive");
  if (binding === "preview") return t("network.mirrorBlockedByPreview");
  return t("network.tabMirror");
}
