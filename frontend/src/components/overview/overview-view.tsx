import { useEffect, useState } from "react";
import {
  ArrowRightLeft,
  Cable,
  Copy,
  CopyPlus,
  Eye,
  EyeOff,
  Globe2,
  Network,
  RotateCcw,
  Server,
  Settings2,
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useI18n } from "@/i18n";
import type { AppView } from "@/hooks/use-session";
import { phaseKeys } from "@/lib/phase";
import { cn } from "@/lib/utils";
import type {
  ConnectionMode,
  Discovery,
  GatewayTransport,
  HelperStatus,
  NetworkDiagnostic,
  SessionState,
} from "@/types";

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
  shareGateway,
  gatewayNamespace,
  gatewayNamespaces,
  onToggle,
  onConnectionModeChange,
  onShareGatewayChange,
  onGatewayNamespaceChange,
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
  shareGateway: boolean;
  gatewayNamespace: string;
  gatewayNamespaces: string[];
  onToggle(): void;
  onConnectionModeChange(mode: ConnectionMode): void;
  onShareGatewayChange(shared: boolean): Promise<void>;
  onGatewayNamespaceChange(namespace: string): Promise<void>;
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
    <div className="mx-auto h-full max-w-[1040px]">
      <div className="grid h-full items-stretch gap-3 md:grid-cols-[1.08fr_0.92fr] md:grid-rows-[auto_auto_minmax(0,1fr)]">
        <Card className="gap-0 overflow-hidden py-0 shadow-none">
          <CardContent className="flex h-full min-h-[210px] flex-col p-4">
            <div className="flex items-start justify-between gap-4">
              <div className="flex min-w-0 items-center gap-3">
                <div className="grid size-10 shrink-0 place-items-center rounded-xl bg-primary/10 text-primary">
                  <Server size={19} />
                </div>
                <div className="min-w-0">
                  <div className="truncate text-[15px] font-semibold">
                    {contextName || t("overview.noContext")}
                  </div>
                  <div className="mt-0.5 truncate text-[11px] text-muted-foreground">
                    {[
                      clusterName && clusterName !== contextName
                        ? clusterName
                        : t("overview.noCluster"),
                      session.kubernetesVersion || "",
                    ].filter(Boolean).join(" · ")}
                  </div>
                </div>
              </div>
              <Button type="button" variant="ghost" size="sm" onClick={onManageClusters}>
                {t("overview.manageClusters")}
              </Button>
            </div>

            <div className="mt-5">
              <GatewaySettings
                contextName={contextName}
                ready={ready}
                busy={busy}
                shareGateway={shareGateway}
                gatewayNamespace={gatewayNamespace}
                gatewayNamespaces={gatewayNamespaces}
                issues={issues}
                gatewayManifest={gatewayManifest}
                onShareGatewayChange={onShareGatewayChange}
                onGatewayNamespaceChange={onGatewayNamespaceChange}
              />
            </div>

          </CardContent>
        </Card>

        <Card className="gap-0 overflow-hidden py-0 shadow-none">
          <CardContent className="flex h-full min-h-[210px] flex-col p-4">
            <div className="grid items-start gap-4 grid-cols-[minmax(0,1fr)_auto]">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                <span
                  className={cn(
                    "size-2 rounded-full",
                    ready ? "bg-success" : busy ? "animate-pulse bg-primary" : "bg-muted-foreground/40",
                  )}
                />
                <h2 className="text-lg font-semibold tracking-tight">
                  {loading
                    ? t("overview.loadingKubeconfig")
                    : disconnecting
                      ? t("overview.disconnecting")
                      : t(phaseKeys[session.phase])}
                </h2>
              </div>
                <p className="mt-1.5 truncate text-[12px] text-muted-foreground">
                  {contextName
                    ? t("overview.connectionMode")
                    : t("overview.selectClusterFirst")}
                </p>
              </div>
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
            </div>

            <div className="mt-auto pt-4">
              <ConnectionModePanel
                activeMode={activeMode}
                busy={busy}
                ready={ready}
                socksPort={session.socksPort}
                platform={platform}
                onConnectionModeChange={onConnectionModeChange}
              />
            </div>
            {error ? (
              <Alert variant="destructive" className="mt-3 w-full text-left">
                <AlertDescription className="truncate !whitespace-nowrap" title={error}>{error}</AlertDescription>
              </Alert>
            ) : null}
          </CardContent>
        </Card>

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

        <DiscoverySummary
          contextName={contextName}
          discovery={session.discovery}
          ready={ready}
          dnsWarning={session.dnsWarning}
          networkIssues={networkIssues}
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
              {resetting ? <Spinner data-icon="inline-start" /> : <RotateCcw data-icon="inline-start" />}
              {t("overview.resetSessionsConfirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

    </div>
  );
}

function GatewaySettings({
  contextName,
  ready,
  busy,
  shareGateway,
  gatewayNamespace,
  gatewayNamespaces,
  issues,
  gatewayManifest,
  onShareGatewayChange,
  onGatewayNamespaceChange,
}: {
  contextName: string;
  ready: boolean;
  busy: boolean;
  shareGateway: boolean;
  gatewayNamespace: string;
  gatewayNamespaces: string[];
  issues: string[];
  gatewayManifest: string;
  onShareGatewayChange(shared: boolean): Promise<void>;
  onGatewayNamespaceChange(namespace: string): Promise<void>;
}) {
  const { t } = useI18n();
  const [saving, setSaving] = useState(false);

  async function saveNamespace(namespace: string) {
    setSaving(true);
    try {
      await onGatewayNamespaceChange(namespace);
      toast.success(t("settings.gatewayNamespaceSaved"));
    } catch (error) {
      toast.error(t("settings.gatewayNamespaceFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setSaving(false);
    }
  }

  async function changeSharing(shared: boolean) {
    setSaving(true);
    try {
      await onShareGatewayChange(shared);
      toast.success(t("settings.shareGatewaySaved"));
    } catch (error) {
      toast.error(t("settings.shareGatewayFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setSaving(false);
    }
  }

  const disabled = ready || busy || saving;
  const namespaceOptions = Array.from(
    new Set([gatewayNamespace, ...gatewayNamespaces].filter(Boolean)),
  );
  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 gap-3">
        <div className="rounded-lg bg-muted/45 px-3 py-2.5">
          <span className="text-[10px] text-muted-foreground">
            {t("settings.gatewayNamespace")}
          </span>
          <Select
            value={gatewayNamespace}
            onValueChange={(namespace) => void saveNamespace(namespace)}
            disabled={disabled}
          >
            <SelectTrigger
              size="sm"
              className="mt-1.5 w-full min-w-0 bg-background font-mono text-[11px]"
              aria-label={t("settings.gatewayNamespace")}
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {namespaceOptions.map((namespace) => (
                <SelectItem key={namespace} value={namespace}>
                  {namespace}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="flex items-center justify-between gap-3 rounded-lg bg-muted/45 px-3 py-2.5">
          <div className="min-w-0">
            <div className="text-[10px] text-muted-foreground">
              {t("settings.shareGateway")}
            </div>
            <div className="mt-1.5 text-[12px] font-medium">
              {shareGateway ? t("overview.gatewayShared") : t("overview.gatewayPrivate")}
            </div>
          </div>
          <Switch
            checked={shareGateway}
            disabled={disabled}
            aria-label={t("settings.shareGateway")}
            onCheckedChange={(checked) => void changeSharing(checked)}
          />
        </div>
      </div>
      <GatewayTransportSetting contextName={contextName} disabled={ready || busy || saving} />
      {issues.length > 0 || gatewayManifest ? (
        <Alert className="w-full text-left">
          <AlertDescription className="space-y-2">
            {issues.length > 0 ? (
              <ul className="list-disc space-y-1 pl-4 text-[11px]">
                {issues.map((item) => <li key={item}>{item}</li>)}
              </ul>
            ) : (
              <p className="text-[11px]">{t("overview.rbacGatewayMissing")}</p>
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
    </div>
  );
}

function GatewayTransportSetting({
  contextName,
  disabled,
}: {
  contextName: string;
  disabled: boolean;
}) {
  const { t } = useI18n();
  const [saving, setSaving] = useState(false);
  const [transport, setTransport] = useState<GatewayTransport>({
    mode: "port-forward",
  });
  const [draft, setDraft] = useState<GatewayTransport>(defaultWebSocketTransport);
  const [open, setOpen] = useState(false);
  const [tokenVisible, setTokenVisible] = useState(false);

  useEffect(() => {
    if (!contextName) {
      setTransport({ mode: "port-forward" });
      setDraft(defaultWebSocketTransport());
      return;
    }
    let active = true;
    backend
      .getGatewayTransport(contextName)
      .then((config) => {
        if (!active) return;
        setTransport(config);
        if (config.mode === "websocket") {
          setDraft({
            ...config,
            poolSize: config.poolSize || 2,
            maxPhysical: config.maxPhysical || 4,
            maxStreams: config.maxStreams || 128,
          });
        } else {
          setDraft(defaultWebSocketTransport());
        }
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [contextName]);

  async function changeMode(mode: string) {
    if (mode === "websocket") {
      openWebSocketSettings();
      return;
    }
    setSaving(true);
    try {
      const config: GatewayTransport = { mode: "port-forward" };
      await backend.setGatewayTransport(contextName, config);
      setTransport(config);
      toast.success(t("overview.gatewayTransportSaved"));
    } catch (error) {
      toast.error(t("overview.gatewayTransportFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setSaving(false);
    }
  }

  function openWebSocketSettings() {
    if (transport.mode === "websocket") {
      setDraft({
        ...transport,
        poolSize: transport.poolSize || 2,
        maxPhysical: transport.maxPhysical || 4,
        maxStreams: transport.maxStreams || 128,
      });
    }
    setTokenVisible(false);
    setOpen(true);
  }

  async function saveWebSocket() {
    setSaving(true);
    try {
      await backend.setGatewayTransport(contextName, draft);
      setTransport(draft);
      setOpen(false);
      setTokenVisible(false);
      toast.success(t("overview.gatewayTransportSaved"));
    } catch (error) {
      toast.error(t("overview.gatewayTransportFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setSaving(false);
    }
  }

  return (
    <>
      <div className="flex min-w-0 items-center justify-between gap-3 rounded-lg bg-muted/45 px-3 py-2">
        <div className="flex min-w-0 items-center gap-2">
          <Globe2 size={14} className="shrink-0 text-muted-foreground" />
          <div className="min-w-0">
            <div className="whitespace-nowrap text-[10px] text-muted-foreground">
              {t("overview.gatewayTransport")}
            </div>
            <div className="truncate text-[11px] font-medium">
              {transport.mode === "websocket"
                ? t("overview.gatewayWebSocket")
                : t("overview.gatewayPortForward")}
            </div>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <Select
            value={transport.mode}
            onValueChange={(mode) => void changeMode(mode)}
            disabled={disabled || saving || !contextName}
          >
            <SelectTrigger size="sm" className="w-36 shrink-0 bg-background text-[11px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="port-forward">
                {t("overview.gatewayPortForward")}
              </SelectItem>
              <SelectItem value="websocket">
                {t("overview.gatewayWebSocket")}
              </SelectItem>
            </SelectContent>
          </Select>
          {transport.mode === "websocket" ? (
            <Button
              type="button"
              variant="outline"
              size="icon-sm"
              disabled={saving || !contextName}
              aria-label={t("overview.gatewayConfigure")}
              title={t("overview.gatewayConfigure")}
              onClick={openWebSocketSettings}
            >
              <Settings2 />
            </Button>
          ) : null}
        </div>
      </div>

      <Dialog
        open={open}
        onOpenChange={(value) => {
          if (saving) return;
          setOpen(value);
          if (!value) setTokenVisible(false);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("overview.gatewayWebSocketTitle")}</DialogTitle>
            <DialogDescription>
              {t("overview.gatewayWebSocketDescription")}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-3">
            <label className="grid gap-1.5 text-xs">
              <span className="text-muted-foreground">{t("overview.gatewayURL")}</span>
              <Input
                value={draft.url ?? ""}
                placeholder="ws://127.0.0.1:8080/v1/tunnel"
                disabled={disabled}
                onChange={(event) =>
                  setDraft((current) => ({ ...current, url: event.target.value }))
                }
              />
            </label>
            <div className="grid gap-1.5 text-xs">
              <span className="text-muted-foreground">{t("overview.gatewayToken")}</span>
              <div className="relative">
                <Input
                  aria-label={t("overview.gatewayToken")}
                  type={tokenVisible ? "text" : "password"}
                  value={draft.token ?? ""}
                  className="pr-9 font-mono"
                  disabled={disabled}
                  onChange={(event) =>
                    setDraft((current) => ({ ...current, token: event.target.value }))
                  }
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  className="absolute right-1 top-1/2 -translate-y-1/2 text-muted-foreground"
                  aria-label={t(
                    tokenVisible ? "overview.gatewayTokenHide" : "overview.gatewayTokenShow",
                  )}
                  onClick={() => setTokenVisible((visible) => !visible)}
                >
                  {tokenVisible ? <EyeOff /> : <Eye />}
                </Button>
              </div>
            </div>
            <div className="grid grid-cols-3 gap-2">
              {([
                ["poolSize", "overview.gatewayPoolSize", 2, 8],
                ["maxPhysical", "overview.gatewayMaxPhysical", 4, 16],
                ["maxStreams", "overview.gatewayMaxStreams", 128, 1024],
              ] as const).map(([key, label, fallback, maximum]) => (
                <label key={key} className="grid gap-1.5 text-xs">
                  <span className="truncate text-muted-foreground">{t(label)}</span>
                  <Input
                    type="number"
                    min={1}
                    max={maximum}
                    value={draft[key] || fallback}
                    disabled={disabled}
                    onChange={(event) =>
                      setDraft((current) => ({
                        ...current,
                        [key]: Number(event.target.value),
                      }))
                    }
                  />
                </label>
              ))}
            </div>
            <div className="flex items-center justify-between rounded-lg border px-3 py-2.5">
              <div className="flex items-center gap-2 text-xs">
                <Globe2 size={14} />
                <span>{t("overview.gatewayInsecureTLS")}</span>
              </div>
              <Switch
                checked={Boolean(draft.insecureSkipVerify)}
                disabled={disabled}
                onCheckedChange={(checked) =>
                  setDraft((current) => ({
                    ...current,
                    insecureSkipVerify: checked,
                  }))
                }
              />
            </div>
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={saving}
              onClick={() => {
                setOpen(false);
                setTokenVisible(false);
              }}
            >
              {t("actions.cancel")}
            </Button>
            <Button
              type="button"
              disabled={saving || disabled}
              onClick={() => void saveWebSocket()}
            >
              {saving ? <Spinner data-icon="inline-start" /> : null}
              {t("settings.gatewayNamespaceSave")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function defaultWebSocketTransport(): GatewayTransport {
  return {
    mode: "websocket",
    url: "ws://127.0.0.1:8080/v1/tunnel",
    token: generateGatewayToken(),
    poolSize: 2,
    maxPhysical: 4,
    maxStreams: 128,
  };
}

function generateGatewayToken(): string {
  const bytes = new Uint8Array(16);
  globalThis.crypto.getRandomValues(bytes);
  return Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("");
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
    <div className="flex flex-col gap-2.5">
      <ToggleGroup
        type="single"
        value={activeMode}
        disabled={busy || ready}
        onValueChange={(value) => {
          if (value) onConnectionModeChange(value as ConnectionMode);
        }}
        className="grid w-full shrink-0 grid-cols-2 gap-1 rounded-lg bg-muted/70 p-1"
      >
        <ToggleGroupItem
          value="socks"
          className="h-8 rounded-md px-2 text-[12px] font-medium data-[state=on]:bg-background data-[state=on]:text-foreground data-[state=on]:shadow-sm data-[state=on]:ring-1 data-[state=on]:ring-border"
        >
          <Network size={15} />
          <span className="truncate">{t("overview.socksProxy")}</span>
        </ToggleGroupItem>
        <ToggleGroupItem
          value="tun"
          className="h-8 rounded-md px-2 text-[12px] font-medium data-[state=on]:bg-background data-[state=on]:text-foreground data-[state=on]:shadow-sm data-[state=on]:ring-1 data-[state=on]:ring-border"
        >
          <Cable size={15} />
          <span className="truncate">{t("overview.tunMode")}</span>
        </ToggleGroupItem>
      </ToggleGroup>

      <div className="min-w-0 rounded-lg bg-muted/35 px-3 py-2">
        {activeMode === "socks" ? (
          <div className="flex flex-wrap items-center gap-2">
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
            <span className="hidden truncate text-[10px] text-muted-foreground xl:inline">
              {t("overview.socksHint")}
            </span>
          </div>
        ) : (
          <div className="flex flex-wrap items-center gap-2">
            <span className="whitespace-nowrap text-[10px] text-muted-foreground">
              {t("overview.tunHelper")}: <strong className="font-medium text-foreground">{helperLabel}</strong>
            </span>
            <div className="flex gap-1.5">
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
    </div>
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

function DiscoverySummary({
  contextName,
  discovery,
  ready,
  dnsWarning,
  networkIssues,
}: {
  contextName: string;
  discovery?: Discovery;
  ready: boolean;
  dnsWarning?: string;
  networkIssues: NetworkDiagnostic[];
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

  const summaryItems = [podCIDRs, serviceCIDRs, dnsServer]
    .map((value) => value.split(",")[0]?.trim())
    .filter(Boolean);

  return (
    <Card className="h-full min-h-[145px] gap-0 overflow-hidden py-0 shadow-none md:col-span-2">
        <div className="flex items-center justify-between gap-3 px-4 pt-3 pb-2">
          <span className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
            {t("overview.clusterNetwork")}
          </span>
          <span className="truncate font-mono text-[10px] text-muted-foreground">
            {summaryItems.length > 0 ? summaryItems.join(" · ") : t("overview.networkUnavailable")}
          </span>
        </div>
        {dnsWarning || networkIssues.length > 0 ? (
          <div className="space-y-2 border-t px-4 py-3">
            {dnsWarning ? (
              <Alert className="w-full text-left">
                <AlertDescription className="break-words">{dnsWarning}</AlertDescription>
              </Alert>
            ) : null}
            {networkIssues.length > 0 ? (
              <Alert className="w-full text-left">
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
          </div>
        ) : null}
        <CardContent className="flex min-h-0 flex-1 flex-col px-4 py-3">
            <dl className="grid gap-x-4 gap-y-2 sm:grid-cols-2 md:grid-cols-4">
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
            <div className="mt-auto flex flex-wrap items-end justify-between gap-3 pt-3">
              <p className="min-w-0 flex-1 truncate text-[10px] leading-snug text-muted-foreground" title={t("overview.networkHint")}>
                {t("overview.networkHint")}
              </p>
              <Button
                type="button"
                variant="default"
                size="sm"
                className="min-w-32"
                disabled={!contextName || saving}
                onClick={() => void save()}
              >
                {t("overview.networkSave")}
              </Button>
            </div>
        </CardContent>
    </Card>
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
    <div className="min-w-0 space-y-1.5 text-[12px]">
      <dt className="truncate text-[11px] text-muted-foreground">{label}</dt>
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
  const rows: {
    group: string;
    icon: LucideIcon;
    label: string;
    value: number;
    view: AppView;
  }[] = [
    {
      group: t("overview.podGroup"),
      icon: Cable,
      label: t("network.tabPortForward"),
      value: podPortForwards,
      view: "workload",
    },
    {
      group: t("overview.networkGroup"),
      icon: Cable,
      label: t("network.tabPortForward"),
      value: networkPortForwards,
      view: "network",
    },
    {
      group: t("overview.networkGroup"),
      icon: ArrowRightLeft,
      label: t("network.tabExchange"),
      value: exchanges,
      view: "network",
    },
    {
      group: t("overview.networkGroup"),
      icon: CopyPlus,
      label: t("network.tabMirror"),
      value: mirrors,
      view: "network",
    },
    {
      group: t("overview.networkGroup"),
      icon: Eye,
      label: t("network.tabPreview"),
      value: previews,
      view: "network",
    },
  ];

  return (
    <Card className="gap-0 overflow-hidden py-0 shadow-none md:col-span-2">
      <CardContent className="px-4 py-3">
      <div className="mb-2 flex items-center justify-between gap-3">
        <div className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
          {t("overview.sessionsPanel")}
        </div>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-6 px-2 text-[10px]"
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
      </div>
      <div className="grid grid-cols-2 gap-1.5 md:grid-cols-5">
        {rows.map((row) => (
          <Button
            key={`${row.group}-${row.label}`}
            type="button"
            variant="ghost"
            title={`${row.group} · ${row.label}`}
            onClick={() => onNavigate(row.view)}
            className={cn(
              "flex h-auto min-w-0 items-center justify-start gap-2 rounded-md border bg-background/80 px-2.5 py-2 text-left",
              "transition-colors hover:bg-accent/60 focus-visible:outline-none",
              "focus-visible:ring-2 focus-visible:ring-ring/50",
            )}
          >
            <div className="grid size-6 shrink-0 place-items-center rounded-md text-muted-foreground">
              <row.icon size={13} strokeWidth={1.7} />
            </div>
            <span className="min-w-0 flex-1 leading-tight">
              <span className="block truncate text-[9px] text-muted-foreground">
                {row.group}
              </span>
              <span className="mt-0.5 block truncate text-[10px] text-foreground">
                {row.label}
              </span>
            </span>
            <span className="ml-auto font-mono text-[13px] font-semibold tabular-nums">
              {row.value}
            </span>
          </Button>
        ))}
      </div>
      </CardContent>
    </Card>
  );
}
