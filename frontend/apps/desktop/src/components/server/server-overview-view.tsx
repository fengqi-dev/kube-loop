import { errorMessage } from "@/lib/errors";
import { useCallback, useEffect, useState } from "react";
import {
  Copy,
  Power,
  RefreshCw,
  Server,
  ShieldCheck,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { backend } from "@/backend";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { useI18n } from "@/i18n";
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
  tunnelBusy,
  dataPlaneError,
  onRefresh,
  onConnect,
  onDisconnect,
}: {
  profile: ServerProfile;
  discovery?: ServerDiscovery;
  inventory: RemoteInventory;
  userName?: string;
  busy: boolean;
  tunnelBusy?: boolean;
  dataPlaneError?: string;
  dataPlaneReason?: DataPlaneStatusEvent["reason"];
  onRefresh(): void;
  onConnect(mode: "socks" | "tun"): Promise<void>;
  onDisconnect(): void;
}) {
  const { t } = useI18n();
  const dataPlane = inventory.dataPlane;
  const ready = dataPlane?.state === "connected";
  const [mode, setMode] = useState<"tun" | "socks">("tun");
  useEffect(() => {
    setMode(ready && dataPlane?.mode === "socks" ? "socks" : "tun");
  }, [profile.id, ready, dataPlane?.mode]);
  const phase: SessionState["phase"] = ready ? "connected" : dataPlane?.state === "error" ? "error" : dataPlane?.state === "reconnecting" ? "starting-tunnel" : "idle";
  const networkSpec = inventory.session?.networkSpec;
  const [helper, setHelper] = useState<HelperStatus | null>(null);
  const [helperAction, setHelperAction] = useState<"install" | "uninstall" | null>(null);
  const [socksPort, setSocksPort] = useState(profile.socksPort || 1080);
  const [socksPortInput, setSocksPortInput] = useState(String(profile.socksPort || 1080));
  const [savingSocksPort, setSavingSocksPort] = useState(false);
  const refreshHelper = useCallback(async () => {
    try {
      setHelper(await backend.helperStatus());
    } catch (reason) {
      toast.error(t("settings.helperLoadFailed"), {
        description: errorMessage(reason),
      });
    }
  }, [t]);

  useEffect(() => { void refreshHelper(); }, [refreshHelper, ready]);
  useEffect(() => {
    let active = true;
    void backend.getServerNetworkSettings(profile.id).then((settings) => {
      if (!active) return;
      setSocksPort(settings.socksPort);
      setSocksPortInput(String(settings.socksPort));
    }).catch((reason) => {
      if (active) toast.error(t("overview.portLoadFailed"), {
        description: errorMessage(reason),
      });
    });
    return () => { active = false; };
  }, [profile.id]);
  async function installHelper(): Promise<boolean> {
    setHelperAction("install");
    try {
      await backend.installHelper();
      await refreshHelper();
      toast.success(t("settings.helperInstallOk"));
      return true;
    } catch (reason) {
      toast.error(t("settings.helperInstallFailed"), {
        description: errorMessage(reason),
      });
      return false;
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
        description: errorMessage(reason),
      });
    } finally {
      setHelperAction(null);
    }
  }

  async function saveSocksPort() {
    const port = Number(socksPortInput);
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      toast.error(t("overview.portInvalid"));
      return;
    }
    setSavingSocksPort(true);
    try {
      const settings = await backend.setServerSOCKSPort(profile.id, port);
      setSocksPort(settings.socksPort);
      setSocksPortInput(String(settings.socksPort));
      toast.success(t("overview.portSaved"));
    } catch (reason) {
      toast.error(t("overview.portSaveFailed"), {
        description: errorMessage(reason),
      });
    } finally {
      setSavingSocksPort(false);
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

  const socksAddress = dataPlane?.socksAddress || `127.0.0.1:${socksPort}`;
  const parsedSocksPort = Number(socksPortInput);
  const socksPortValid = Number.isInteger(parsedSocksPort) && parsedSocksPort >= 1 && parsedSocksPort <= 65535;
  const socksPortDirty = socksPortValid && parsedSocksPort !== socksPort;

  return (
    <div className="overview-workbench">
      <header className="overview-heading">
        <span className="overview-server-icon"><Server size={23} /></span>
        <div className="min-w-0 flex-1">
          <h1>{profile.displayName || discovery?.serviceId || profile.id}</h1>
          <p>{profile.baseUrl}</p>
        </div>
        <Button variant="ghost" size="sm" disabled={busy} onClick={onRefresh}>
          {busy ? <Spinner /> : <RefreshCw size={14} />}{t("network.refresh")}
        </Button>
      </header>
        <Tabs className="overview-section overview-connection-panel" value={mode} onValueChange={value => { if (!ready && !busy && helperAction === null && (value === "tun" || value === "socks")) setMode(value); }}>
          <h2>{t("overview.connectionMode")} · {t(mode === "tun" ? "overview.tunHelper" : "overview.socksProxy")}</h2>
      <div className="overview-connection-actions">
        <span className="overview-connection-state"><i className="connection-dot" data-connected={ready} />{t(tunnelBusy && !ready ? "overview.connecting" : `phase.${phase}`)}</span>
        <TabsList aria-label={t("overview.connectionMode")}>
          <TabsTrigger value="tun" disabled={ready || busy || helperAction !== null}>TUN</TabsTrigger>
          <TabsTrigger value="socks" disabled={ready || busy || helperAction !== null}>SOCKS</TabsTrigger>
        </TabsList>
        <Button className="overview-connect" size="lg" variant={ready ? "destructive" : "default"} disabled={busy || helperAction !== null || (!ready && !inventory.capabilities.includes("cluster.tunnel"))} onClick={() => ready ? onDisconnect() : void onConnect(mode)}>
          {busy ? <Spinner /> : <Power size={18} />}{t(ready ? "workspace.disconnect" : "workspace.connect")}
        </Button>
      </div>
      {tunnelBusy && !ready && mode === "tun" && <p className="overview-note" role="status">{t("overview.tunPreparing")}</p>}
      {dataPlaneError && <Alert variant="destructive"><AlertDescription className="break-words">{dataPlaneError}</AlertDescription></Alert>}
          <TabsContent value="tun">

          <p className="overview-note">{t("overview.tunHint")}</p>
          <dl>
            <ServerValue label={t("workspace.state")} value={helperLabel} />
            <ServerValue label={t("overview.helperVersion")} value={helper?.version} />
          </dl>
          <div className="overview-actions">
            {!helperCurrent && <Button size="sm" variant="outline" disabled={busy || helperAction !== null || (ready && dataPlane?.mode === "tun")} onClick={() => void installHelper()}>
              {helperAction === "install" ? <Spinner /> : <ShieldCheck size={13} />}{t("settings.helperInstall")}
            </Button>}
            {helper?.installed && <Button size="sm" variant="outline" disabled={busy || helperAction !== null || (ready && dataPlane?.mode === "tun")} onClick={() => void uninstallHelper()}>
              {helperAction === "uninstall" ? <Spinner /> : <Trash2 size={13} />}{t("settings.helperUninstall")}
            </Button>}
          </div>
          </TabsContent>
          <TabsContent value="socks">

          <p className="overview-note">{t("overview.socksHint")}</p>
          <label className="overview-port">
            <span>{t("overview.listenPort")}</span>
            <code>127.0.0.1:</code>
            <Input type="number" min={1} max={65535} step={1} inputMode="numeric" value={socksPortInput} disabled={ready || busy || savingSocksPort}
              onChange={event => setSocksPortInput(event.target.value)}
              onKeyDown={event => { if (event.key === "Enter" && socksPortDirty && !ready && !busy && !savingSocksPort) void saveSocksPort(); }} />
          </label>
          <div className="overview-actions">
            <Button size="sm" variant="outline" disabled={ready || busy || savingSocksPort || !socksPortDirty} onClick={() => void saveSocksPort()}>
              {savingSocksPort && <Spinner />}{t("overview.savePort")}
            </Button>
            <Button size="sm" variant="outline" disabled={!ready || dataPlane?.mode !== "socks"}
              onClick={() => void navigator.clipboard.writeText(proxyEnvironmentVariables(socksAddress)).then(() => toast.success(t("overview.socksCopied")), () => toast.error(t("overview.socksCopyFailed")))}>
              <Copy size={12} />{t("overview.socksCopy")}
            </Button>
          </div>
          </TabsContent>
        </Tabs>
      <div className="overview-sections">
        <section className="overview-section">
          <h2>{t("overview.environment")}</h2>
          <dl>
            <ServerValue label="Kubernetes" value={inventory.kubernetesVersion} />
            <ServerValue label="Gateway" value={inventory.gatewayVersion} />
            <ServerValue label={t("workspace.identity")} value={userName} />
            <ServerValue label={t("overview.connectionMode")} value={ready ? dataPlane?.mode?.toUpperCase() : t("overview.modeUnavailable")} />
          </dl>
        </section>
        <section className="overview-section">
          <h2>{t("overview.clusterNetwork")}</h2>
          <dl>
            <ServerValue label={t("overview.podNetwork")} value={networkSpec?.podCIDRs?.join(", ")} />
            <ServerValue label={t("overview.serviceNetwork")} value={networkSpec?.serviceCIDRs?.join(", ")} />
            <ServerValue label={t("overview.clusterDomain")} value={networkSpec?.clusterDomains?.join(", ")} />
          </dl>
          {!networkSpec && <p className="overview-note">{t("overview.waitingDiscovery")}</p>}
        </section>

      </div>
    </div>
  );
}

function ServerValue({ label, value }: { label: string; value?: string }) {
  return <div className="overview-value"><dt>{label}</dt><dd>{value || "—"}</dd></div>;
}

function proxyEnvironmentVariables(address: string): string {
  const proxy = `socks5h://${address}`;
  if (navigator.userAgent.toLowerCase().includes("windows")) {
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
