import { useEffect, useState, type ReactNode } from "react";
import {
  ArrowRightLeft,
  Cable,
  Copy,
  CopyPlus,
  Eye,
  Network,
  RotateCcw,
  Server,
  ShieldCheck,
  Trash2,
  type LucideIcon,
} from "lucide-react";
import { toast } from "sonner";
import { backend } from "@/backend";
import { ConnectionOrb } from "@/components/overview/connection-orb";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useI18n, type TranslationKey } from "@/i18n";
import type { AppView } from "@/hooks/use-session";
import { cn } from "@/lib/utils";
import type { ConnectionMode, Discovery, HelperStatus, SessionState } from "@/types";

const overviewPhaseKeys: Record<SessionState["phase"], TranslationKey> = {
  idle: "overview.phaseIdle",
  checking: "overview.phaseChecking",
  "installing-gateway": "overview.phaseInstalling",
  "discovering-network": "overview.phaseDiscovering",
  "starting-tunnel": "overview.phaseStarting",
  connected: "overview.phaseConnected",
  error: "overview.phaseError",
};

export function OverviewView({
  contextName,
  clusterName,
  session,
  connectionMode,
  platform,
  loading,
  error,
  busy,
  disconnecting,
  ready,
  onToggle,
  onConnectionModeChange,
  onManageClusters,
  onNavigate,
}: {
  contextName: string;
  clusterName: string;
  session: SessionState;
  connectionMode: ConnectionMode;
  platform?: string;
  loading: boolean;
  error: string;
  busy: boolean;
  disconnecting: boolean;
  ready: boolean;
  onToggle(): void;
  onConnectionModeChange(mode: ConnectionMode): void;
  onManageClusters(): void;
  onNavigate(view: AppView): void;
}) {
  const { t } = useI18n();
  const [podPortForwards, setPodPortForwards] = useState(0);
  const [networkPortForwards, setNetworkPortForwards] = useState(0);
  const [exchanges, setExchanges] = useState(0);
  const [mirrors, setMirrors] = useState(0);
  const [previews, setPreviews] = useState(0);
  const [metricsTick, setMetricsTick] = useState(0);
  const [resetOpen, setResetOpen] = useState(false);
  const [resetting, setResetting] = useState(false);

  useEffect(() => {
    let active = true;
    const refresh = () => {
      Promise.all([
        backend.listPortForwards(),
        backend.listIntercepts(),
        backend.listMirrors(),
        backend.listPreviews(),
        backend.sessionIntentCounts(),
      ])
        .then(([forwards, intercepts, mirrorItems, previewItems, intents]) => {
          if (!active) return;
          // Prefer live counts; fall back to persisted intents when the cluster is down.
          setPodPortForwards(
            Math.max(
              forwards.filter((item) => item.kind === "pod").length,
              intents.podPortForwards ?? 0,
            ),
          );
          setNetworkPortForwards(
            Math.max(
              forwards.filter((item) => item.kind === "service").length,
              intents.networkPortForwards ?? 0,
            ),
          );
          setExchanges(Math.max(intercepts.length, intents.exchanges ?? 0));
          setMirrors(Math.max(mirrorItems.length, intents.mirrors ?? 0));
          setPreviews(previewItems.length);
        })
        .catch(() => undefined);
    };
    refresh();
    return () => {
      active = false;
    };
  }, [session.inventoryRevision, ready, metricsTick]);

  async function confirmReset() {
    setResetting(true);
    try {
      await backend.resetSessions();
      setPodPortForwards(0);
      setNetworkPortForwards(0);
      setExchanges(0);
      setMirrors(0);
      setMetricsTick((value) => value + 1);
      setResetOpen(false);
      toast.success(t("overview.resetSessionsDone"));
    } catch (error) {
      toast.error(t("overview.resetSessionsFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setResetting(false);
    }
  }

  const issues = session.capabilities?.issues ?? [];
  const networkIssues = session.network?.issues ?? [];
  const gatewayManifest = session.gatewayManifest ?? "";
  const activeMode = ready ? (session.mode ?? connectionMode) : connectionMode;

  return (
    <div className="mx-auto max-w-[880px] space-y-4">
      <Card className="gap-0 overflow-hidden border-border py-0 shadow-none">
        <div className="flex flex-wrap items-center gap-3 border-b px-5 py-3">
          <div className="min-w-0 flex-1">
            <div className="truncate text-sm font-medium">
              {contextName || t("overview.noContext")}
            </div>
            <div className="mt-0.5 truncate text-[11px] text-muted-foreground">
              {session.kubernetesVersion
                ? t("overview.kubernetesVersion", { version: session.kubernetesVersion })
                : clusterName && clusterName !== contextName
                  ? clusterName
                  : t("overview.noCluster")}
            </div>
          </div>
          <Button type="button" variant="outline" size="sm" onClick={onManageClusters}>
            <Server size={14} data-icon="inline-start" />
            {t("overview.manageClusters")}
          </Button>
          <div className="hidden items-center gap-1.5 text-[11px] text-muted-foreground sm:flex">
            <ShieldCheck size={13} className="text-success" />
            {t("overview.localCredentials")}
          </div>
        </div>

        <CardContent className="px-5 py-4">
          <div className="grid min-h-[14rem] items-stretch gap-4 sm:grid-cols-[minmax(0,1.15fr)_auto_minmax(0,0.85fr)]">
            <ConnectionModePanel
              activeMode={activeMode}
              busy={busy}
              ready={ready}
              socksPort={session.socksPort}
              platform={platform}
              onConnectionModeChange={onConnectionModeChange}
            />

            <div className="flex w-32 shrink-0 flex-col items-center justify-center self-center px-2 text-center">
              <ConnectionOrb
                phase={session.phase}
                busy={busy}
                disconnecting={disconnecting}
                disabled={loading || !contextName}
                ariaLabel={
                  busy
                    ? t("overview.cancel")
                    : ready
                      ? t("overview.disconnect")
                      : t("overview.connect")
                }
                onClick={onToggle}
              />
              <h2 className="mt-2.5 min-h-5 w-full whitespace-nowrap text-[14px] font-semibold tracking-tight">
                {loading
                  ? t("overview.loadingKubeconfig")
                  : disconnecting
                    ? t("overview.disconnecting")
                    : t(overviewPhaseKeys[session.phase])}
              </h2>
              <p className="mt-0.5 max-w-[9rem] truncate text-[11px] text-muted-foreground">
                {contextName
                  ? [contextName, clusterName || t("overview.noCluster")].join(" · ")
                  : t("overview.selectClusterFirst")}
              </p>
            </div>

            <SessionMetrics
              podPortForwards={podPortForwards}
              networkPortForwards={networkPortForwards}
              exchanges={exchanges}
              mirrors={mirrors}
              previews={previews}
              onNavigate={onNavigate}
              onReset={() => setResetOpen(true)}
              resetting={resetting}
            />
          </div>

          <Dialog open={resetOpen} onOpenChange={(open) => !resetting && setResetOpen(open)}>
            <DialogContent showCloseButton={!resetting}>
              <DialogHeader>
                <DialogTitle>{t("overview.resetSessionsTitle")}</DialogTitle>
                <DialogDescription>{t("overview.resetSessionsDesc")}</DialogDescription>
              </DialogHeader>
              <DialogFooter>
                <Button
                  type="button"
                  variant="outline"
                  disabled={resetting}
                  onClick={() => setResetOpen(false)}
                >
                  {t("actions.cancel")}
                </Button>
                <Button
                  type="button"
                  variant="destructive"
                  disabled={resetting}
                  onClick={() => void confirmReset()}
                >
                  {resetting ? (
                    <Spinner data-icon="inline-start" />
                  ) : (
                    <RotateCcw data-icon="inline-start" />
                  )}
                  {t("overview.resetSessionsConfirm")}
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>

          {error ? (
            <Alert variant="destructive" className="mt-4 w-full text-left">
              <AlertDescription className="break-words">{error}</AlertDescription>
            </Alert>
          ) : null}
          {session.dnsWarning ? (
            <Alert className="mt-4 w-full text-left">
              <AlertDescription className="break-words">{session.dnsWarning}</AlertDescription>
            </Alert>
          ) : null}
          {networkIssues.length > 0 ? (
            <Alert className="mt-4 w-full text-left">
              <AlertDescription>
                <ul className="list-disc space-y-1 pl-4 text-[12px]">
                  {networkIssues.map((item) => (
                    <li key={`${item.code}:${item.target}:${item.conflict}:${item.interface}`}>
                      {item.message}
                    </li>
                  ))}
                </ul>
              </AlertDescription>
            </Alert>
          ) : null}

          {issues.length > 0 || gatewayManifest ? (
            <Alert className="mt-4 w-full text-left">
              <AlertDescription className="space-y-2">
                {issues.length > 0 ? (
                  <ul className="list-disc space-y-1 pl-4 text-[12px]">
                    {issues.map((item) => (
                      <li key={item}>{item}</li>
                    ))}
                  </ul>
                ) : (
                  <p className="text-[12px]">{t("overview.rbacGatewayMissing")}</p>
                )}
                {gatewayManifest ? (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      void navigator.clipboard.writeText(gatewayManifest).then(
                        () => toast.success(t("overview.rbacCopied")),
                        () => toast.error(t("overview.rbacCopyFailed")),
                      );
                    }}
                  >
                    <Copy size={14} data-icon="inline-start" />
                    {t("overview.rbacCopyYaml")}
                  </Button>
                ) : null}
              </AlertDescription>
            </Alert>
          ) : null}
        </CardContent>
      </Card>

      <DiscoverySummary
        contextName={contextName}
        discovery={session.discovery}
        ready={ready}
      />

    </div>
  );
}

function ConnectionModePanel({
  activeMode,
  busy,
  ready,
  socksPort,
  platform,
  onConnectionModeChange,
}: {
  activeMode: ConnectionMode;
  busy: boolean;
  ready: boolean;
  socksPort?: number;
  platform?: string;
  onConnectionModeChange(mode: ConnectionMode): void;
}) {
  const { t } = useI18n();
  const address = `127.0.0.1:${socksPort || 7890}`;
  const [helper, setHelper] = useState<HelperStatus | null>(null);
  const [helperAction, setHelperAction] = useState<
    "install" | "uninstall" | null
  >(null);

  async function refreshHelper(showError = false) {
    try {
      setHelper(await backend.helperStatus());
    } catch (error) {
      if (showError) {
        toast.error(t("settings.helperLoadFailed"), {
          description: error instanceof Error ? error.message : String(error),
        });
      }
    }
  }

  useEffect(() => {
    if (activeMode === "tun") {
      void refreshHelper();
    }
  }, [activeMode]);

  async function installHelper() {
    setHelperAction("install");
    try {
      await backend.installHelper();
      await refreshHelper(true);
      toast.success(t("settings.helperInstallOk"));
    } catch (error) {
      toast.error(t("settings.helperInstallFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setHelperAction(null);
    }
  }

  async function uninstallHelper() {
    setHelperAction("uninstall");
    try {
      await backend.uninstallHelper();
      await refreshHelper(true);
      toast.success(t("settings.helperUninstallOk"));
    } catch (error) {
      toast.error(t("settings.helperUninstallFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setHelperAction(null);
    }
  }

  const helperLabel = !helper
    ? t("overview.helperChecking")
    : helper.running
      ? t("settings.helperRunning")
      : helper.installed
        ? t("settings.helperStopped")
        : t("settings.helperMissing");
  const helperCurrent = Boolean(
    helper?.running && helper.version === helper.expected,
  );

  return (
    <SidePanel title={t("overview.connectionMode")}>
      <ToggleGroup
        type="single"
        value={activeMode}
        disabled={busy || ready}
        onValueChange={(value) => {
          if (value) onConnectionModeChange(value as ConnectionMode);
        }}
        className="grid w-full grid-cols-2 gap-1 rounded-xl bg-muted/70 p-1"
      >
        <ToggleGroupItem
          value="socks"
          className="min-h-10 rounded-lg px-3 text-sm font-medium data-[state=on]:bg-background data-[state=on]:text-foreground data-[state=on]:shadow-sm data-[state=on]:ring-1 data-[state=on]:ring-border"
        >
          <Network size={15} />
          <span className="truncate">{t("overview.socksProxy")}</span>
        </ToggleGroupItem>
        <ToggleGroupItem
          value="tun"
          className="min-h-10 rounded-lg px-3 text-sm font-medium data-[state=on]:bg-background data-[state=on]:text-foreground data-[state=on]:shadow-sm data-[state=on]:ring-1 data-[state=on]:ring-border"
        >
          <Cable size={15} />
          <span className="truncate">{t("overview.tunMode")}</span>
        </ToggleGroupItem>
      </ToggleGroup>

      <div className="mt-2.5 flex min-h-0 flex-1 flex-col rounded-xl border border-primary/15 bg-primary/[0.035] p-2.5">
        <div className="flex items-start gap-2.5">
          <div className="flex size-7 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
            {activeMode === "socks" ? <Network size={15} /> : <Cable size={15} />}
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-[12px] font-medium">
                {activeMode === "socks"
                  ? t("overview.socksModeDesc")
                  : t("overview.tunModeDesc")}
              </span>
              <span
                className={cn(
                  "rounded-full px-2 py-0.5 text-[9px] font-medium",
                  ready
                    ? "bg-success/10 text-success"
                    : "bg-muted text-muted-foreground",
                )}
              >
                {ready ? t("overview.modeActive") : t("overview.modeUnavailable")}
              </span>
            </div>
            <p className="mt-1 text-[10px] leading-4 text-muted-foreground">
              {activeMode === "socks"
                ? t("overview.socksHint")
                : t("overview.tunHint")}
            </p>
          </div>
        </div>

        {activeMode === "socks" ? (
          <div className="mt-auto flex flex-wrap items-center gap-2 pt-3">
            <code className="whitespace-nowrap rounded-md border bg-background px-2 py-1.5 text-[11px]">
              {address}
            </code>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-7 px-2 text-[10px]"
              disabled={!ready || !socksPort}
              aria-label={t("overview.socksCopy")}
              title={t("overview.socksCopy")}
              onClick={() => {
                const environment = proxyEnvironmentVariables(platform, address);
                void navigator.clipboard.writeText(environment).then(
                  () => toast.success(t("overview.socksCopied")),
                  () => toast.error(t("overview.socksCopyFailed")),
                );
              }}
            >
              <Copy size={12} data-icon="inline-start" />
              {t("overview.socksCopy")}
            </Button>
          </div>
        ) : (
          <div className="mt-auto pt-3">
            <div className="mb-2 flex items-center justify-between gap-2 text-[10px]">
              <span className="text-muted-foreground">{t("overview.tunHelper")}</span>
              <span className="font-medium">{helperLabel}</span>
            </div>
            <div className="grid grid-cols-2 gap-2">
              <Button
                type="button"
                size="sm"
                className="h-7 px-2 text-[10px]"
                disabled={ready || busy || helperAction !== null || helperCurrent}
                onClick={() => void installHelper()}
              >
                {helperAction === "install" ? (
                  <Spinner data-icon="inline-start" />
                ) : (
                  <ShieldCheck size={12} data-icon="inline-start" />
                )}
                {helperAction === "install"
                  ? t("settings.helperInstalling")
                  : t("settings.helperInstall")}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-7 px-2 text-[10px]"
                disabled={
                  ready || busy || helperAction !== null || !helper?.installed
                }
                onClick={() => void uninstallHelper()}
              >
                <Trash2 size={12} data-icon="inline-start" />
                {helperAction === "uninstall"
                  ? t("settings.helperUninstalling")
                  : t("settings.helperUninstall")}
              </Button>
            </div>
          </div>
        )}
      </div>
    </SidePanel>
  );
}

function proxyEnvironmentVariables(platform: string | undefined, address: string): string {
  const proxy = `socks5h://${address}`;
  if (platform === "windows") {
    return [
      `$env:HTTP_PROXY = "${proxy}"`,
      `$env:HTTPS_PROXY = "${proxy}"`,
      `$env:ALL_PROXY = "${proxy}"`,
    ].join("\r\n");
  }
  return [
    `export HTTP_PROXY="${proxy}"`,
    `export HTTPS_PROXY="${proxy}"`,
    `export ALL_PROXY="${proxy}"`,
  ].join("\n");
}

function SidePanel({
  title,
  children,
  footer,
}: {
  title: string;
  children: ReactNode;
  footer?: ReactNode;
}) {
  return (
    <div className="flex h-full min-w-0 flex-col rounded-lg border bg-muted/25 px-3 py-2.5">
      <div className="mb-1.5 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
        {title}
      </div>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">{children}</div>
      {footer ? <div className="mt-2.5">{footer}</div> : null}
    </div>
  );
}

function DiscoverySummary({
  contextName,
  discovery,
  ready,
}: {
  contextName: string;
  discovery?: Discovery;
  ready: boolean;
}) {
  const { t } = useI18n();
  const [podCIDRs, setPodCIDRs] = useState("");
  const [serviceCIDRs, setServiceCIDRs] = useState("");
  const [dnsServer, setDnsServer] = useState("");
  const [clusterDomains, setClusterDomains] = useState("cluster.local");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!contextName) {
      setPodCIDRs("");
      setServiceCIDRs("");
      setDnsServer("");
      setClusterDomains("cluster.local");
      return;
    }
    let active = true;
    backend
      .getManualNetwork(contextName)
      .then((manual) => {
        if (!active) return;
        setPodCIDRs(
          (manual.podCIDRs?.length ? manual.podCIDRs : discovery?.podCIDRs)?.join(", ") ?? "",
        );
        setServiceCIDRs(
          (manual.serviceCIDRs?.length
            ? manual.serviceCIDRs
            : discovery?.serviceCIDRs
          )?.join(", ") ?? "",
        );
        setDnsServer(manual.dnsServer || discovery?.dnsServer || "");
        setClusterDomains(
          (manual.clusterDomains?.length
            ? manual.clusterDomains
            : discovery?.clusterDomains?.length
              ? discovery.clusterDomains
              : ["cluster.local"]
          ).join(", "),
        );
      })
      .catch(() => {
        if (!active) return;
        setPodCIDRs(discovery?.podCIDRs?.join(", ") ?? "");
        setServiceCIDRs(discovery?.serviceCIDRs?.join(", ") ?? "");
        setDnsServer(discovery?.dnsServer || "");
        setClusterDomains((discovery?.clusterDomains ?? ["cluster.local"]).join(", "));
      });
    return () => {
      active = false;
    };
  }, [
    contextName,
    discovery?.dnsServer,
    discovery?.podCIDRs,
    discovery?.serviceCIDRs,
    discovery?.clusterDomains,
  ]);

  async function save() {
    if (!contextName) return;
    setSaving(true);
    try {
      await backend.setManualNetwork(contextName, {
        podCIDRs: splitCIDRs(podCIDRs),
        serviceCIDRs: splitCIDRs(serviceCIDRs),
        dnsServer: dnsServer.trim(),
        clusterDomains: splitCIDRs(clusterDomains),
      });
      toast.success(ready ? t("overview.networkSavedReconnect") : t("overview.networkSaved"));
    } catch (error) {
      toast.error(t("overview.networkSaveFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setSaving(false);
    }
  }

  return (
    <SidePanel
      title={t("overview.clusterNetwork")}
      footer={
        <div className="flex justify-end">
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="min-w-32"
            disabled={!contextName || saving}
            onClick={() => void save()}
          >
            {t("overview.networkSave")}
          </Button>
        </div>
      }
    >
      <dl className="grid gap-x-6 gap-y-2 sm:grid-cols-2">
        <NetworkField
          label={t("overview.clusterDomain")}
          value={clusterDomains}
          placeholder="cluster.local, corp.local"
          onChange={setClusterDomains}
          disabled={!contextName}
        />
        <NetworkField
          label={t("overview.podNetwork")}
          value={podCIDRs}
          placeholder="10.244.0.0/16"
          onChange={setPodCIDRs}
          disabled={!contextName}
        />
        <NetworkField
          label={t("overview.serviceNetwork")}
          value={serviceCIDRs}
          placeholder="10.96.0.0/12"
          onChange={setServiceCIDRs}
          disabled={!contextName}
        />
        <NetworkField
          label={t("overview.clusterDns")}
          value={dnsServer}
          placeholder="10.96.0.10"
          onChange={setDnsServer}
          disabled={!contextName}
        />
      </dl>
      <p className="mt-2 text-[10px] leading-snug text-muted-foreground">
        {t("overview.networkHint")}
      </p>
    </SidePanel>
  );
}

function NetworkField({
  label,
  value,
  placeholder,
  onChange,
  disabled,
}: {
  label: string;
  value: string;
  placeholder: string;
  onChange(value: string): void;
  disabled?: boolean;
}) {
  return (
    <div className="grid grid-cols-[6.5rem_minmax(0,1fr)] items-center gap-x-2 text-[12px]">
      <dt className="whitespace-nowrap text-muted-foreground">{label}</dt>
      <dd className="min-w-0">
        <Input
          className="h-7 w-full min-w-0 rounded-md border border-input bg-background px-2 font-mono text-[11px] outline-none transition-colors focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/40 disabled:opacity-50"
          value={value}
          placeholder={placeholder}
          disabled={disabled}
          onChange={(event) => onChange(event.target.value)}
        />
      </dd>
    </div>
  );
}

function splitCIDRs(raw: string): string[] {
  return raw
    .split(/[\n,;]+/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function SessionMetrics({
  podPortForwards,
  networkPortForwards,
  exchanges,
  mirrors,
  previews,
  onNavigate,
  onReset,
  resetting,
}: {
  podPortForwards: number;
  networkPortForwards: number;
  exchanges: number;
  mirrors: number;
  previews: number;
  onNavigate(view: AppView): void;
  onReset(): void;
  resetting: boolean;
}) {
  const { t } = useI18n();
  const resettable = podPortForwards + networkPortForwards + exchanges + mirrors;
  const groups: {
    title: string;
    rows: { icon: LucideIcon; label: string; value: number; view: AppView }[];
  }[] = [
    {
      title: t("overview.podGroup"),
      rows: [
        {
          icon: Cable,
          label: t("network.tabPortForward"),
          value: podPortForwards,
          view: "workload",
        },
      ],
    },
    {
      title: t("overview.networkGroup"),
      rows: [
        {
          icon: Cable,
          label: t("network.tabPortForward"),
          value: networkPortForwards,
          view: "network",
        },
        {
          icon: ArrowRightLeft,
          label: t("network.tabExchange"),
          value: exchanges,
          view: "network",
        },
        {
          icon: CopyPlus,
          label: t("network.tabMirror"),
          value: mirrors,
          view: "network",
        },
        {
          icon: Eye,
          label: t("network.tabPreview"),
          value: previews,
          view: "network",
        },
      ],
    },
  ];

  return (
    <SidePanel
      title={t("overview.sessionsPanel")}
      footer={
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="w-full"
          disabled={resettable === 0 || resetting}
          onClick={onReset}
        >
          {resetting ? (
            <Spinner data-icon="inline-start" />
          ) : (
            <RotateCcw data-icon="inline-start" />
          )}
          {t("overview.resetSessions")}
        </Button>
      }
    >
      <div className="flex min-h-0 flex-1 flex-col justify-between">
        {groups.map((group) => (
          <div key={group.title} className="min-w-0">
            <div className="mb-1.5 text-[11px] font-medium text-foreground/85">{group.title}</div>
            <div className="space-y-1.5 pl-0.5">
              {group.rows.map((row) => (
                <Button
                  key={`${group.title}-${row.label}`}
                  type="button"
                  variant="ghost"
                  onClick={() => onNavigate(row.view)}
                  className={cn(
                    "flex h-auto w-full items-center justify-start gap-2 rounded-md px-1 py-0.5 text-left",
                    "transition-colors hover:bg-accent/60 focus-visible:outline-none",
                    "focus-visible:ring-2 focus-visible:ring-ring/50",
                  )}
                >
                  <div className="grid size-6 shrink-0 place-items-center rounded-md border bg-background text-muted-foreground">
                    <row.icon size={13} strokeWidth={1.7} />
                  </div>
                  <span className="text-[11px] text-muted-foreground">{row.label}</span>
                  <span className="ml-auto font-mono text-[13px] font-semibold tabular-nums">
                    {row.value}
                  </span>
                </Button>
              ))}
            </div>
          </div>
        ))}
      </div>
    </SidePanel>
  );
}
