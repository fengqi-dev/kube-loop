import { useCallback, useEffect, useState } from "react";
import {
  ArrowRightLeft,
  Cable,
  Copy,
  CopyPlus,
  Eye,
  Globe2,
  Network,
  RadioTower,
  RefreshCw,
  Server,
  ShieldCheck,
  Trash2,
  type LucideIcon,
} from "lucide-react";
import { toast } from "sonner";
import { backend } from "@/backend";
import { ConnectionOrb } from "@/components/overview/connection-orb";
import type { AppView } from "@/components/layout/navigation";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useI18n } from "@/i18n";
import { cn } from "@/lib/utils";
import type {
  DataPlaneStatusEvent,
  HelperStatus,
  RemoteInventory,
  ServerDiscovery,
  ServerProfile,
  SessionState,
} from "@/types";

export function ServerOverviewView({
  profile,
  discovery,
  inventory,
  userName,
  busy,
  dataPlaneError,
  dataPlaneReason,
  counts,
  onRefresh,
  onConnect,
  onDisconnect,
  onNavigate,
}: {
  profile: ServerProfile;
  discovery?: ServerDiscovery;
  inventory: RemoteInventory;
  userName?: string;
  busy: boolean;
  dataPlaneError?: string;
  dataPlaneReason?: DataPlaneStatusEvent["reason"];
  counts: {
    podPortForwards: number;
    networkPortForwards: number;
    exchanges: number;
    mirrors: number;
    previews: number;
  };
  onRefresh(): void;
  onConnect(mode: "socks" | "tun"): void;
  onDisconnect(): void;
  onNavigate(view: AppView): void;
}) {
  const { t } = useI18n();
  const dataPlane = inventory.dataPlane;
  const ready = dataPlane?.state === "connected";
  const phase: SessionState["phase"] = ready
    ? "connected"
    : dataPlane?.state === "error"
      ? "error"
      : dataPlane?.state === "reconnecting"
        ? "starting-tunnel"
        : "idle";
  const networkSpec = inventory.session?.networkSpec;
  const warnings = inventory.network?.issues?.filter((issue) => issue.severity === "warning") ?? [];
  const [selectedMode, setSelectedMode] = useState<"socks" | "tun">(dataPlane?.mode || "socks");
  const [helper, setHelper] = useState<HelperStatus | null>(null);
  const [helperAction, setHelperAction] = useState<"install" | "uninstall" | null>(null);
  const [testingConnectivity, setTestingConnectivity] = useState(false);
  const refreshHelper = useCallback(async () => {
    try {
      setHelper(await backend.helperStatus());
    } catch (reason) {
      toast.error(t("settings.helperLoadFailed"), {
        description: reason instanceof Error ? reason.message : String(reason),
      });
    }
  }, [t]);

  useEffect(() => { void refreshHelper(); }, [refreshHelper]);
  useEffect(() => {
    if (dataPlane?.state === "connected") setSelectedMode(dataPlane.mode || "socks");
  }, [profile.id, dataPlane?.state, dataPlane?.mode]);

  async function installHelper() {
    setHelperAction("install");
    try {
      await backend.installHelper();
      await refreshHelper();
      toast.success(t("settings.helperInstallOk"));
    } catch (reason) {
      toast.error(t("settings.helperInstallFailed"), {
        description: reason instanceof Error ? reason.message : String(reason),
      });
    } finally {
      setHelperAction(null);
    }
  }

  async function uninstallHelper() {
    setHelperAction("uninstall");
    try {
      await backend.uninstallHelper();
      await refreshHelper();
      toast.success(t("settings.helperUninstallOk"));
    } catch (reason) {
      toast.error(t("settings.helperUninstallFailed"), {
        description: reason instanceof Error ? reason.message : String(reason),
      });
    } finally {
      setHelperAction(null);
    }
  }

  async function testConnectivity() {
    if (!ready || testingConnectivity) return;
    setTestingConnectivity(true);
    try {
      await backend.testServerDataPlane(profile.id);
      toast.success("Network connectivity test passed", {
        description: "Local SOCKS, Gateway transport, and cluster DNS are reachable.",
      });
    } catch (reason) {
      toast.error("Network connectivity test failed", {
        description: reason instanceof Error ? reason.message : String(reason),
      });
    } finally {
      setTestingConnectivity(false);
    }
  }

  const helperCurrent = Boolean(helper?.running && helper.version === helper.expected);
  const helperLabel = !helper
    ? t("overview.helperChecking")
    : helperCurrent
      ? t("settings.helperRunning")
      : helper.installed
        ? t("settings.helperStopped")
        : t("settings.helperMissing");

  const socksAddress = dataPlane?.socksAddress || "127.0.0.1:1080";

  return (
    <div className="mx-auto max-w-[1280px]">
      <div className="grid items-start gap-3 lg:grid-cols-2">
        <Card className="gap-0 overflow-hidden py-0 shadow-none lg:col-span-2">
          <CardContent className="flex flex-col gap-2.5 p-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex min-w-0 items-center gap-3">
              <div className="grid size-8 shrink-0 place-items-center rounded-lg bg-primary/10 text-primary">
                <Server size={17} />
              </div>
              <div className="min-w-0">
                <div className="truncate text-[15px] font-semibold">{profile.displayName || discovery?.serviceId || profile.id}</div>
                <div className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[10px] text-muted-foreground">
                  <span>Kubernetes {inventory.kubernetesVersion || "—"}</span>
                  <span>Gateway {inventory.gatewayVersion || "—"}</span>
                  <span>Session {inventory.session?.state || "unavailable"}</span>
                  <span className="truncate">{userName || "Authenticated user"}</span>
                </div>
                <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground" title={profile.baseUrl}>{profile.baseUrl}</div>
              </div>
            </div>
            <Button type="button" variant="outline" size="sm" className="shrink-0 self-end sm:self-auto" disabled={busy} onClick={onRefresh}>
              {busy ? <Spinner data-icon="inline-start" /> : <RefreshCw size={13} data-icon="inline-start" />}
              Refresh
            </Button>
          </CardContent>
        </Card>

        <Card className="gap-0 overflow-hidden py-0 shadow-none lg:col-span-2">
          <CardContent className="p-3">
            <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-4">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className={cn("size-2 rounded-full", ready ? "bg-success" : phase === "error" ? "bg-destructive" : phase === "starting-tunnel" ? "animate-pulse bg-primary" : "bg-muted-foreground/40")} />
                  <h2 className="text-lg font-semibold tracking-tight">
                    {ready ? dataPlane?.mode === "tun" ? "System TUN is connected" : "Remote cluster proxy is ready" : phase === "error" ? "Data Plane unavailable" : phase === "starting-tunnel" ? "Connecting Data Plane" : "Data Plane disconnected"}
                  </h2>
                </div>
                <p className="mt-1 text-[11px] text-muted-foreground">RelayTicket-bound Gateway transport</p>
              </div>
              <ConnectionOrb
                phase={phase}
                busy={busy}
                disabled={busy || !inventory.capabilities.includes("cluster.tunnel") || (!ready && selectedMode === "tun" && !helperCurrent)}
                ariaLabel={ready ? "Disconnect" : "Connect"}
                onClick={() => ready ? onDisconnect() : onConnect(selectedMode)}
              />
            </div>

            <div className="mt-2.5 grid gap-2.5 md:grid-cols-[220px_minmax(0,1fr)]">
              <ToggleGroup
                type="single"
                value={selectedMode}
                disabled={ready || busy}
                onValueChange={(mode) => { if (mode === "socks" || mode === "tun") setSelectedMode(mode); }}
                className="grid w-full grid-cols-2 gap-1 self-start rounded-lg bg-muted/70 p-1"
              >
                <ToggleGroupItem value="socks" className="h-8 rounded-md text-[11px] data-[state=on]:bg-background data-[state=on]:shadow-sm"><Network size={13} /> SOCKS</ToggleGroupItem>
                <ToggleGroupItem value="tun" className="h-8 rounded-md text-[11px] data-[state=on]:bg-background data-[state=on]:shadow-sm"><Cable size={13} /> TUN</ToggleGroupItem>
              </ToggleGroup>

              <div className="min-h-10 rounded-lg bg-muted/35 px-3 py-2">
                {selectedMode === "socks" ? (
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="min-w-0">
                      <div className="text-[9px] font-medium tracking-wide text-muted-foreground uppercase">SOCKS5 listener</div>
                      <code className="mt-1 block truncate text-[12px]" title={socksAddress}>{socksAddress}</code>
                    </div>
                    <Button
                      type="button"
                      variant="outline"
                      size="icon-xs"
                      disabled={!ready}
                      title={t("overview.socksCopy")}
                      aria-label={t("overview.socksCopy")}
                      onClick={() => void navigator.clipboard.writeText(socksAddress).then(() => toast.success(t("overview.socksCopied")), () => toast.error(t("overview.socksCopyFailed")))}
                    >
                      <Copy size={12} />
                    </Button>
                  </div>
                ) : (
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="min-w-0">
                      <div className="text-[9px] font-medium tracking-wide text-muted-foreground uppercase">System routing helper</div>
                      <div className="mt-1 truncate text-[11px] text-muted-foreground">{helperLabel} · Pod IP and Service IP routing</div>
                    </div>
                    <div className="flex shrink-0 items-center gap-1.5">
                      {!helperCurrent ? (
                        <Button type="button" size="sm" className="h-7 px-2 text-[10px]" disabled={busy || helperAction !== null || dataPlane?.mode === "tun"} onClick={() => void installHelper()}>
                          {helperAction === "install" ? <Spinner data-icon="inline-start" /> : <ShieldCheck size={12} data-icon="inline-start" />}
                          {t("settings.helperInstall")}
                        </Button>
                      ) : null}
                      {helper?.installed ? (
                        <Button type="button" variant="outline" size="icon-xs" disabled={busy || helperAction !== null || dataPlane?.mode === "tun"} title={t("settings.helperUninstall")} aria-label={t("settings.helperUninstall")} onClick={() => void uninstallHelper()}>
                          {helperAction === "uninstall" ? <Spinner /> : <Trash2 size={12} />}
                        </Button>
                      ) : null}
                    </div>
                  </div>
                )}
              </div>
            </div>

            {dataPlaneError ? (
              <Alert variant="destructive" className="mt-3">
                <AlertDescription className="truncate text-[11px]" title={dataPlaneError}>{failureTitle(dataPlaneReason)} · {dataPlaneError}</AlertDescription>
              </Alert>
            ) : null}
          </CardContent>
        </Card>

        <Card className="gap-0 overflow-hidden py-0 shadow-none">
          <div className="flex items-center justify-between gap-3 px-3 pt-2.5 pb-1.5">
            <span className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
              {t("overview.clusterNetwork")}
            </span>
            <div className="flex min-w-0 items-center gap-1.5">
              <span className="truncate font-mono text-[10px] text-muted-foreground">
                {inventory.network?.routingMode || "proxy"} · {inventory.network?.strictRoute ? "strict route" : "standard route"}
              </span>
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                disabled={!ready || busy || testingConnectivity}
                title="Test network connectivity"
                aria-label="Test network connectivity"
                onClick={() => void testConnectivity()}
              >
                {testingConnectivity ? <Spinner /> : <RadioTower size={12} />}
              </Button>
            </div>
          </div>
          {warnings.length > 0 ? (
            <div className="border-t px-4 py-3">
              <Alert className="py-2">
                <AlertDescription className="text-[11px]">{warnings.map((item) => item.message).join(" · ")}</AlertDescription>
              </Alert>
            </div>
          ) : null}
          <CardContent className="grid grid-cols-2 gap-x-3 gap-y-2 px-3 py-2.5">
            <NetworkValue label={t("overview.clusterDomain")} value={networkSpec?.clusterDomains.join(", ")} />
            <NetworkValue label={t("overview.podNetwork")} value={networkSpec?.podCIDRs.join(", ")} />
            <NetworkValue label={t("overview.serviceNetwork")} value={networkSpec?.serviceCIDRs.join(", ")} />
            <NetworkValue label={t("overview.clusterDns")} value={networkSpec?.dnsServer} />
          </CardContent>
        </Card>

        <SessionMetrics counts={counts} onNavigate={onNavigate} />
      </div>
    </div>
  );
}

function SessionMetrics({
  counts,
  onNavigate,
}: {
  counts: {
    podPortForwards: number;
    networkPortForwards: number;
    exchanges: number;
    mirrors: number;
    previews: number;
  };
  onNavigate(view: AppView): void;
}) {
  const { t } = useI18n();
  const rows: Array<{ group: string; icon: LucideIcon; label: string; value: number; view: AppView }> = [
    { group: t("overview.podGroup"), icon: Cable, label: t("network.tabPortForward"), value: counts.podPortForwards, view: "workload" },
    { group: t("overview.networkGroup"), icon: Cable, label: t("network.tabPortForward"), value: counts.networkPortForwards, view: "network" },
    { group: t("overview.networkGroup"), icon: ArrowRightLeft, label: t("network.tabExchange"), value: counts.exchanges, view: "network" },
    { group: t("overview.networkGroup"), icon: CopyPlus, label: t("network.tabMirror"), value: counts.mirrors, view: "network" },
    { group: t("overview.networkGroup"), icon: Eye, label: t("network.tabPreview"), value: counts.previews, view: "network" },
  ];
  return (
    <Card className="gap-0 overflow-hidden py-0 shadow-none">
      <CardContent className="px-3 py-2.5">
        <div className="mb-1.5 text-[10px] font-medium tracking-wide text-muted-foreground uppercase">{t("overview.sessionsPanel")}</div>
        <div className="grid grid-cols-2 gap-1.5 sm:grid-cols-5">
          {rows.map((row) => (
            <Button
              key={`${row.group}-${row.label}`}
              type="button"
              variant="ghost"
              title={`${row.group} · ${row.label}`}
              onClick={() => onNavigate(row.view)}
              className="flex h-9 min-w-0 items-center justify-start gap-1.5 rounded-md border bg-background/80 px-2 text-left hover:bg-accent/60"
            >
              <row.icon className="shrink-0 text-muted-foreground" size={12} />
              <span className="min-w-0 flex-1 truncate text-[9px] text-foreground">{row.label}</span>
              <span className="ml-auto font-mono text-[12px] font-semibold tabular-nums">{row.value}</span>
            </Button>
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

function NetworkValue({ label, value }: { label: string; value?: string }) {
  return (
    <div className="min-w-0 space-y-1 text-[11px]">
      <div className="truncate text-[10px] text-muted-foreground">{label}</div>
      <div className="h-6 truncate rounded-md border border-input bg-muted/25 px-2 py-1 font-mono text-[10px]" title={value || "—"}>
        {value || "—"}
      </div>
    </div>
  );
}

function failureTitle(reason?: DataPlaneStatusEvent["reason"]) {
  switch (reason) {
    case "authentication_required": return "Authentication required";
    case "access_denied": return "Access denied";
    case "session_expired": return "Session expired";
    case "network_unavailable": return "Network unavailable";
    default: return "Data Plane recovery stopped";
  }
}
