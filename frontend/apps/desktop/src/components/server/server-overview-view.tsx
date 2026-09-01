import { useCallback, useEffect, useState } from "react";
import {
  Cable,
  Copy,
  Network,
  RefreshCw,
  Server,
  ShieldCheck,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { backend } from "@/backend";
import { ConnectionOrb } from "@/components/overview/connection-orb";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
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
  onRefresh,
  onConnect,
  onDisconnect,
}: {
  profile: ServerProfile;
  discovery?: ServerDiscovery;
  inventory: RemoteInventory;
  userName?: string;
  busy: boolean;
  dataPlaneError?: string;
  dataPlaneReason?: DataPlaneStatusEvent["reason"];
  onRefresh(): void;
  onConnect(mode: "socks" | "tun"): Promise<void>;
  onDisconnect(): void;
}) {
  const { t } = useI18n();
  const dataPlane = inventory.dataPlane;
  const [pendingMode, setPendingMode] = useState<"socks" | "tun">();
  const ready = dataPlane?.state === "connected" && !pendingMode;
  const phase: SessionState["phase"] = pendingMode
    ? "starting-tunnel"
    : ready
    ? "connected"
    : dataPlane?.state === "error"
      ? "error"
      : dataPlane?.state === "reconnecting"
        ? "starting-tunnel"
        : "idle";
  const networkSpec = inventory.session?.networkSpec;
  const [selectedMode, setSelectedMode] = useState<"socks" | "tun">(
    dataPlane?.state === "connected" && dataPlane.mode === "socks" ? "socks" : "tun",
  );
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
        description: reason instanceof Error ? reason.message : String(reason),
      });
    }
  }, [t]);

  useEffect(() => { void refreshHelper(); }, [refreshHelper]);
  useEffect(() => {
    let active = true;
    void backend.getServerNetworkSettings(profile.id).then((settings) => {
      if (!active) return;
      setSocksPort(settings.socksPort);
      setSocksPortInput(String(settings.socksPort));
    }).catch((reason) => {
      if (active) toast.error("Could not load SOCKS port", {
        description: reason instanceof Error ? reason.message : String(reason),
      });
    });
    return () => { active = false; };
  }, [profile.id]);
  useEffect(() => {
    if (busy || pendingMode) return;
    setSelectedMode(
      dataPlane?.state === "connected" && dataPlane.mode === "socks" ? "socks" : "tun",
    );
  }, [profile.id, busy, pendingMode, dataPlane?.state, dataPlane?.mode]);

  async function connectSelectedMode() {
    if (pendingMode) return;
    const mode = selectedMode;
    setPendingMode(mode);
    try {
      if (mode === "tun" && !helperCurrent && !await installHelper()) return;
      await onConnect(mode);
    } finally {
      setPendingMode(undefined);
    }
  }

  async function installHelper(): Promise<boolean> {
    setHelperAction("install");
    try {
      await backend.installHelper();
      await refreshHelper();
      toast.success(t("settings.helperInstallOk"));
      return true;
    } catch (reason) {
      toast.error(t("settings.helperInstallFailed"), {
        description: reason instanceof Error ? reason.message : String(reason),
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
        description: reason instanceof Error ? reason.message : String(reason),
      });
    } finally {
      setHelperAction(null);
    }
  }

  async function saveSocksPort() {
    const port = Number(socksPortInput);
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      toast.error("SOCKS port must be between 1 and 65535");
      return;
    }
    setSavingSocksPort(true);
    try {
      const settings = await backend.setServerSOCKSPort(profile.id, port);
      setSocksPort(settings.socksPort);
      setSocksPortInput(String(settings.socksPort));
      toast.success("SOCKS port saved", { description: `The next connection will listen on 127.0.0.1:${settings.socksPort}.` });
    } catch (reason) {
      toast.error("Could not save SOCKS port", {
        description: reason instanceof Error ? reason.message : String(reason),
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

  const socksAddress = dataPlane?.socksAddress || "127.0.0.1:1080";
  const parsedSocksPort = Number(socksPortInput);
  const socksPortValid = Number.isInteger(parsedSocksPort) && parsedSocksPort >= 1 && parsedSocksPort <= 65535;
  const socksPortDirty = socksPortValid && parsedSocksPort !== socksPort;

  return (
    <div className="mx-auto max-w-[1200px]">
      <div className="grid items-stretch gap-5 lg:grid-cols-12">
        <Card className="gap-0 overflow-hidden rounded-2xl border-border/60 bg-card py-0 shadow-[0_2px_16px_-4px_rgba(0,0,0,0.06)] lg:col-span-12">
          <CardContent className="p-5">
            <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-5">
              <div className="grid min-w-0 gap-5 sm:grid-cols-[minmax(0,0.9fr)_minmax(0,1.2fr)] sm:items-center">
                <div className="flex min-w-0 items-center gap-4">
                  <div className="grid size-12 shrink-0 place-items-center rounded-2xl bg-gradient-to-br from-primary/20 to-primary/5 text-primary ring-1 ring-primary/15">
                    <Server size={20} />
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="text-[10px] font-semibold tracking-[0.12em] text-muted-foreground uppercase">Target Server</div>
                    <div className="mt-1 truncate text-lg font-bold">{profile.displayName || discovery?.serviceId || profile.id}</div>
                    <div className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground" title={profile.baseUrl}>{profile.baseUrl}</div>
                  </div>
                  <Button type="button" variant="ghost" size="icon-xs" className="shrink-0 rounded-lg" title="Refresh server" aria-label="Refresh server" disabled={busy} onClick={onRefresh}>
                    {busy ? <Spinner /> : <RefreshCw size={14} />}
                  </Button>
                </div>

                <div className="min-w-0 sm:border-l sm:border-border/60 sm:pl-5">
                  <div className="text-[10px] font-semibold tracking-[0.12em] text-muted-foreground uppercase">Connection</div>
                  <div className="mt-1 flex items-center gap-2">
                    <span className={cn("size-2.5 shrink-0 rounded-full", ready ? "bg-success" : phase === "error" ? "bg-destructive" : phase === "starting-tunnel" ? "animate-pulse bg-primary" : "bg-muted-foreground/40")} />
                    <h2 className="truncate text-sm font-semibold tracking-tight">
                      {pendingMode ? pendingMode === "tun" ? "Connecting System TUN" : "Connecting cluster proxy" : ready ? dataPlane?.mode === "tun" ? "System TUN is connected" : "Remote cluster proxy is ready" : phase === "error" ? "Data Plane unavailable" : phase === "starting-tunnel" ? "Connecting Data Plane" : "Data Plane disconnected"}
                    </h2>
                  </div>
                  <p className="mt-1 text-[11px] text-muted-foreground">RelayTicket-bound Gateway transport</p>
                </div>
              </div>

              <ConnectionOrb
                phase={phase}
                busy={busy}
                variant="toggle"
                disabled={busy || Boolean(pendingMode) || helperAction !== null || !inventory.capabilities.includes("cluster.tunnel")}
                ariaLabel={ready ? "Disconnect" : "Connect"}
                onClick={() => ready ? onDisconnect() : void connectSelectedMode()}
              />
            </div>

            <div className="mt-5 grid items-stretch gap-4 lg:grid-cols-[minmax(260px,0.78fr)_minmax(0,1.5fr)]">
              <section className="rounded-2xl border border-border/50 bg-muted/15 p-4">
                <div className="mb-3 text-[10px] font-medium tracking-[0.12em] text-muted-foreground uppercase">
                  {t("overview.clusterNetwork")}
                </div>
                <div className="grid grid-cols-2 gap-x-5 gap-y-4">
                  <ServerValue label="Kubernetes" value={inventory.kubernetesVersion} />
                  <ServerValue label="Gateway" value={inventory.gatewayVersion} />
                  <ServerValue label="Pod CIDR" value={networkSpec?.podCIDRs?.join(", ")} />
                  <ServerValue label="Service CIDR" value={networkSpec?.serviceCIDRs?.join(", ")} />
                  <ServerValue label="Session" value={inventory.session?.state} />
                  <ServerValue label="Identity" value={userName || "Authenticated user"} />
                </div>
              </section>

              <section className="rounded-2xl border border-border/50 bg-muted/15 p-4">
                <div className="mb-3 flex items-center justify-between gap-3">
                  <div className="text-[10px] font-medium tracking-[0.12em] text-muted-foreground uppercase">
                    {t("overview.connectionMode")}
                  </div>
                  <div className="text-[10px] font-medium text-muted-foreground">
                    {selectedMode === "tun" ? t("overview.tunMode") : t("overview.socksProxy")}
                  </div>
                </div>
                <div className="overflow-hidden rounded-2xl border border-border/50 bg-muted/15 p-1.5">
                  <ToggleGroup
                  type="single"
                  value={selectedMode}
                  disabled={ready || busy}
                  onValueChange={(mode) => { if (mode === "socks" || mode === "tun") setSelectedMode(mode); }}
                  className="grid w-full grid-cols-1 gap-1 sm:grid-cols-2"
                >
                  <ToggleGroupItem
                    value="tun"
                    aria-label={t("overview.tunMode")}
                    className="group h-auto min-h-[68px] items-stretch justify-start whitespace-normal rounded-xl border border-transparent bg-transparent p-3.5 text-left shadow-none data-[state=on]:border-border/60 data-[state=on]:bg-background data-[state=on]:shadow-sm"
                  >
                    <span className="flex w-full min-w-0 items-start gap-3">
                      <span className="grid size-9 shrink-0 place-items-center rounded-xl bg-background text-muted-foreground shadow-xs ring-1 ring-border/60 group-data-[state=on]:bg-primary/10 group-data-[state=on]:text-primary group-data-[state=on]:ring-primary/20">
                        <Cable size={15} />
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="flex items-center justify-between gap-2">
                          <strong className="text-[12px] font-semibold">{t("overview.tunMode")}</strong>
                          {ready && dataPlane?.mode === "tun" ? <ModeActiveLabel label={t("overview.modeActive")} /> : null}
                        </span>
                        <span className="mt-1 flex items-center gap-1.5 text-[10px] leading-4 text-muted-foreground">
                          <i className={cn("size-1.5 shrink-0 rounded-full", helperCurrent ? "bg-success" : "bg-muted-foreground/40")} />
                          {t("overview.tunModeDesc")} · {helperLabel}
                        </span>
                      </span>
                    </span>
                  </ToggleGroupItem>
                  <ToggleGroupItem
                    value="socks"
                    aria-label={t("overview.socksProxy")}
                    className="group h-auto min-h-[68px] items-stretch justify-start whitespace-normal rounded-xl border border-transparent bg-transparent p-3.5 text-left shadow-none data-[state=on]:border-border/60 data-[state=on]:bg-background data-[state=on]:shadow-sm"
                  >
                    <span className="flex w-full min-w-0 items-start gap-3">
                      <span className="grid size-9 shrink-0 place-items-center rounded-xl bg-background text-muted-foreground shadow-xs ring-1 ring-border/60 group-data-[state=on]:bg-primary/10 group-data-[state=on]:text-primary group-data-[state=on]:ring-primary/20">
                        <Network size={15} />
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="flex items-center justify-between gap-2">
                          <strong className="text-[12px] font-semibold">{t("overview.socksProxy")}</strong>
                          {ready && dataPlane?.mode === "socks" ? <ModeActiveLabel label={t("overview.modeActive")} /> : null}
                        </span>
                        <span className="mt-1 block text-[10px] leading-4 text-muted-foreground">{t("overview.socksModeDesc")}</span>
                      </span>
                    </span>
                  </ToggleGroupItem>
                  </ToggleGroup>

                  <div className="mt-1 min-h-[60px] rounded-xl border border-border/50 bg-background/60 px-3.5 py-3">
                    {selectedMode === "socks" ? (
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="min-w-0 flex-1">
                      <div className="text-[10px] leading-4 text-muted-foreground">{t("overview.socksHint")}</div>
                      <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
                        <code className="text-[11px] font-medium text-muted-foreground">127.0.0.1:</code>
                        <Input
                          type="number"
                          inputMode="numeric"
                          min={1}
                          max={65535}
                          step={1}
                          value={socksPortInput}
                          disabled={ready || busy || savingSocksPort}
                          aria-label="SOCKS port"
                          className="h-7 w-[88px] px-2 font-mono text-[11px]"
                          onChange={(event) => setSocksPortInput(event.target.value)}
                          onKeyDown={(event) => {
                            if (event.key === "Enter" && socksPortDirty) void saveSocksPort();
                          }}
                        />
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          className="h-7 px-2.5 text-[10px]"
                          disabled={ready || busy || savingSocksPort || !socksPortDirty}
                          onClick={() => void saveSocksPort()}
                        >
                          {savingSocksPort ? <Spinner data-icon="inline-start" /> : null}
                          Save
                        </Button>
                        {ready ? (
                          <code className="ml-1 truncate text-[10px] text-muted-foreground" title={socksAddress}>Active: socks5h://{socksAddress}</code>
                        ) : null}
                      </div>
                    </div>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      className="h-7 px-2.5 text-[10px]"
                      disabled={!ready}
                      title={t("overview.socksCopy")}
                      aria-label={t("overview.socksCopy")}
                      onClick={() => void navigator.clipboard.writeText(proxyEnvironmentVariables(socksAddress)).then(() => toast.success(t("overview.socksCopied")), () => toast.error(t("overview.socksCopyFailed")))}
                    >
                      <Copy size={12} data-icon="inline-start" />
                      {t("overview.socksCopy")}
                    </Button>
                  </div>
                ) : (
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="min-w-0">
                      <div className="text-[10px] leading-4 text-muted-foreground">{t("overview.tunHint")}</div>
                      <div className="mt-1.5 flex items-center gap-1.5 text-[11px] font-medium">
                        <ShieldCheck size={12} className={helperCurrent ? "text-success" : "text-muted-foreground"} />
                        {t("overview.tunHelper")}: {helperLabel}
                      </div>
                    </div>
                    <div className="flex shrink-0 items-center gap-1.5">
                      {!helperCurrent ? (
                        <Button type="button" size="sm" className="h-7 px-2.5 text-[10px]" disabled={busy || helperAction !== null || dataPlane?.mode === "tun"} onClick={() => void installHelper()}>
                          {helperAction === "install" ? <Spinner data-icon="inline-start" /> : <ShieldCheck size={12} data-icon="inline-start" />}
                          {t("settings.helperInstall")}
                        </Button>
                      ) : null}
                      {helper?.installed ? (
                        <Button type="button" variant="outline" size="icon-xs" className="rounded-lg" disabled={busy || helperAction !== null || dataPlane?.mode === "tun"} title={t("settings.helperUninstall")} aria-label={t("settings.helperUninstall")} onClick={() => void uninstallHelper()}>
                          {helperAction === "uninstall" ? <Spinner /> : <Trash2 size={12} />}
                        </Button>
                      ) : null}
                    </div>
                  </div>
                    )}
                  </div>
                </div>
              </section>
            </div>

            {dataPlaneError ? (
              <Alert variant="destructive" className="mt-3">
                <AlertDescription className="truncate text-[11px]" title={dataPlaneError}>{failureTitle(dataPlaneReason)} · {dataPlaneError}</AlertDescription>
              </Alert>
            ) : null}

          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function ServerValue({ label, value }: { label: string; value?: string }) {
  return (
    <div className="min-w-0">
      <div className="text-[9px] font-medium tracking-[0.1em] text-muted-foreground uppercase">{label}</div>
      <div className="mt-1 truncate text-[11px] font-semibold" title={value || "—"}>{value || "—"}</div>
    </div>
  );
}

function ModeActiveLabel({ label }: { label: string }) {
  return (
    <span className="flex shrink-0 items-center gap-1 text-[9px] font-medium text-success">
      <i className="size-1.5 rounded-full bg-success" />
      {label}
    </span>
  );
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

function failureTitle(reason?: DataPlaneStatusEvent["reason"]) {
  switch (reason) {
    case "authentication_required": return "Authentication required";
    case "access_denied": return "Access denied";
    case "session_expired": return "Session expired";
    case "network_unavailable": return "Network unavailable";
    default: return "Data Plane recovery stopped";
  }
}
