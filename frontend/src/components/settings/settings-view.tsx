import { useEffect, useState } from "react";
import {
  Check,
  CheckCircle2,
  Copy,
  Download,
  ExternalLink,
  FileJson,
  Globe2,
  RefreshCw,
  Shield,
  ShieldCheck,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { backend } from "@/backend";
import { JsonView } from "@/components/shared/json-view";
import { PageShell } from "@/components/shared/page-shell";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useTheme, type ThemePreference } from "@/hooks/use-theme";
import { useI18n, type Language } from "@/i18n";
import { cn } from "@/lib/utils";
import type { HelperStatus, UpdateInfo } from "@/types";
import { appVersion } from "@/version";

export function SettingsView({
  ready,
  coreVersion,
  update,
  checking,
  onCheck,
  onOpen,
}: {
  ready: boolean;
  coreVersion?: string;
  update: UpdateInfo;
  checking: boolean;
  onCheck(): void;
  onOpen(): void;
}) {
  const { language, locale, setLanguage, t } = useI18n();
  const { preference, setPreference } = useTheme();
  const checkedAt = update.checkedAt
    ? new Date(update.checkedAt).toLocaleString(locale, { hour12: false })
    : t("settings.checkOnStartup");
  const [helper, setHelper] = useState<HelperStatus | null>(null);
  const [helperBusy, setHelperBusy] = useState(false);
  const [configOpen, setConfigOpen] = useState(false);
  const [configBusy, setConfigBusy] = useState<"full" | "dns" | null>(null);
  const [configView, setConfigView] = useState<"full" | "dns">("full");
  const [configText, setConfigText] = useState("");

  async function refreshHelper() {
    try {
      setHelper(await backend.helperStatus());
    } catch (error) {
      toast.error(t("settings.helperLoadFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    }
  }

  useEffect(() => {
    void refreshHelper();
  }, []);

  async function onInstallHelper() {
    setHelperBusy(true);
    try {
      await backend.installHelper();
      await refreshHelper();
      toast.success(t("settings.helperInstallOk"));
    } catch (error) {
      toast.error(t("settings.helperInstallFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setHelperBusy(false);
    }
  }

  async function onUninstallHelper() {
    setHelperBusy(true);
    try {
      await backend.uninstallHelper();
      await refreshHelper();
      toast.success(t("settings.helperUninstallOk"));
    } catch (error) {
      toast.error(t("settings.helperUninstallFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setHelperBusy(false);
    }
  }

  async function onViewConfig() {
    if (!ready) {
      toast.error(t("settings.configUnavailable"));
      return;
    }
    setConfigBusy("full");
    try {
      const text = await backend.getSingBoxConfig();
      setConfigText(text);
      setConfigView("full");
      setConfigOpen(true);
    } catch (error) {
      toast.error(t("settings.configLoadFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setConfigBusy(null);
    }
  }

  async function onViewDNSConfig() {
    if (!ready) {
      toast.error(t("settings.configUnavailable"));
      return;
    }
    setConfigBusy("dns");
    try {
      const text = await backend.getSingBoxConfig();
      const config = JSON.parse(text) as { dns?: unknown };
      if (
        typeof config.dns !== "object" ||
        config.dns === null ||
        Array.isArray(config.dns)
      ) {
        throw new Error(t("settings.dnsConfigUnavailable"));
      }
      setConfigText(JSON.stringify(config.dns, null, 2));
      setConfigView("dns");
      setConfigOpen(true);
    } catch (error) {
      toast.error(t("settings.dnsConfigLoadFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setConfigBusy(null);
    }
  }

  async function onCopyConfig() {
    try {
      await navigator.clipboard.writeText(configText);
      toast.success(t("settings.configCopied"));
    } catch (error) {
      toast.error(t("settings.configLoadFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    }
  }

  const helperLabel = !helper
    ? t("settings.helperMissing")
    : helper.running
      ? t("settings.helperRunning")
      : helper.installed
        ? t("settings.helperStopped")
        : t("settings.helperMissing");

  return (
    <PageShell title={t("settings.title")} description={t("settings.description")}>
      <Card className="mb-5 gap-0 py-0 shadow-none">
        <CardContent className="flex items-center justify-between gap-6 p-5">
          <div>
            <h3 className="text-[13px] font-semibold">{t("settings.theme")}</h3>
            <p className="mt-1 text-[11px] text-muted-foreground">
              {t("settings.themeDescription")}
            </p>
          </div>
          <div className="w-44 shrink-0">
            <Select
              value={preference}
              onValueChange={(value) => {
                setPreference(value as ThemePreference);
                toast.success(t("settings.theme"), {
                  description: t(
                    value === "dark"
                      ? "settings.themeDark"
                      : value === "system"
                        ? "settings.themeSystem"
                        : "settings.themeLight",
                  ),
                });
              }}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="light">{t("settings.themeLight")}</SelectItem>
                <SelectItem value="dark">{t("settings.themeDark")}</SelectItem>
                <SelectItem value="system">{t("settings.themeSystem")}</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </CardContent>
      </Card>

      <Card className="mb-5 gap-0 py-0 shadow-none">
        <CardContent className="flex items-center justify-between gap-6 p-5">
          <div>
            <h3 className="text-[13px] font-semibold">{t("settings.language")}</h3>
            <p className="mt-1 text-[11px] text-muted-foreground">
              {t("settings.languageDescription")}
            </p>
          </div>
          <div className="w-44 shrink-0">
            <Select value={language} onValueChange={(value) => setLanguage(value as Language)}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="en">{t("settings.english")}</SelectItem>
                <SelectItem value="zh-CN">{t("settings.chinese")}</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </CardContent>
      </Card>

      <Card className="mb-5 gap-0 py-0 shadow-none">
        <CardContent className="flex items-start justify-between gap-6 p-5">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <ShieldCheck size={15} className="text-muted-foreground" />
              <h3 className="text-[13px] font-semibold">{t("settings.networkRuntimeTitle")}</h3>
            </div>
            <p className="mt-1 text-[11px] text-muted-foreground">
              {t("settings.networkRuntimeDescription")}
            </p>

            <div className="mt-4 space-y-2.5">
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
                <span className="text-[11px] font-medium text-muted-foreground">
                  {t("settings.coreTitle")}
                </span>
                <span className="font-mono text-[12px] text-foreground">
                  {coreVersion || t("settings.coreUnknown")}
                </span>
                <Badge
                  variant="secondary"
                  className={cn(
                    "rounded-md px-1.5 py-0 text-[10px] font-medium",
                    ready && "bg-success/15 text-success",
                  )}
                >
                  {ready ? t("core.running") : t("core.onDemand")}
                </Badge>
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  className={cn(
                    "h-7 px-2 text-[11px] text-muted-foreground",
                    ready && "text-foreground hover:text-foreground",
                  )}
                  disabled={!ready || configBusy !== null}
                  onClick={() => void onViewConfig()}
                >
                  {configBusy === "full" ? (
                    <Spinner data-icon="inline-start" />
                  ) : (
                    <FileJson data-icon="inline-start" />
                  )}
                  {t("settings.viewConfig")}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  className={cn(
                    "h-7 px-2 text-[11px] text-muted-foreground",
                    ready && "text-foreground hover:text-foreground",
                  )}
                  disabled={!ready || configBusy !== null}
                  onClick={() => void onViewDNSConfig()}
                >
                  {configBusy === "dns" ? (
                    <Spinner data-icon="inline-start" />
                  ) : (
                    <Globe2 data-icon="inline-start" />
                  )}
                  {t("settings.viewDNSConfig")}
                </Button>
              </div>
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <span className="text-[11px] font-medium text-muted-foreground">
                  {t("settings.helperTitle")}
                </span>
                <span className="text-[12px] font-medium">{helperLabel}</span>
                {helper?.version ? (
                  <span className="font-mono text-[10px] text-muted-foreground">
                    {t("settings.helperVersion", { version: helper.version })}
                  </span>
                ) : null}
              </div>
              {helper?.error ? (
                <p className="text-[11px] text-muted-foreground">{helper.error}</p>
              ) : null}
            </div>
          </div>
          <div className="flex shrink-0 flex-col gap-2">
            <Button
              type="button"
              size="sm"
              disabled={helperBusy || Boolean(helper?.running && helper.version === helper.expected)}
              onClick={() => void onInstallHelper()}
            >
              {helperBusy ? (
                <Spinner data-icon="inline-start" />
              ) : (
                <Shield data-icon="inline-start" />
              )}
              {helperBusy ? t("settings.helperInstalling") : t("settings.helperInstall")}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={helperBusy || !helper?.installed}
              onClick={() => void onUninstallHelper()}
            >
              <Trash2 data-icon="inline-start" />
              {helperBusy ? t("settings.helperUninstalling") : t("settings.helperUninstall")}
            </Button>
          </div>
        </CardContent>
      </Card>

      <div className="mb-3 flex items-end justify-between gap-4">
        <div>
          <h3 className="text-[13px] font-semibold">{t("settings.updateTitle")}</h3>
          <p className="mt-1 text-[11px] text-muted-foreground">{t("settings.updateDescription")}</p>
        </div>
        <Button type="button" variant="outline" size="sm" disabled={checking} onClick={onCheck}>
          <RefreshCw className={checking ? "animate-spin" : undefined} data-icon="inline-start" />
          {checking ? t("settings.checking") : t("settings.checkUpdates")}
        </Button>
      </div>

      <Card className="gap-0 overflow-hidden py-0 shadow-none">
        <CardContent className="flex items-start justify-between gap-6 p-6">
          <div className="min-w-0">
            <div className="flex items-center gap-3">
              <div className="grid size-11 place-items-center rounded-md border border-primary/20 bg-primary/10 text-primary">
                <Download size={20} />
              </div>
              <div>
                <h3 className="text-sm font-semibold">KubeLoop Desktop</h3>
                <p className="mt-1 font-mono text-[11px] text-muted-foreground">
                  {t("settings.currentVersion", {
                    version: update.currentVersion || appVersion,
                  })}
                </p>
              </div>
            </div>

            <div className="mt-5 text-xs">
              {update.available ? (
                <div className="flex items-center gap-2 text-success">
                  <CheckCircle2 size={15} />
                  {t("settings.newVersion", { version: update.latestVersion ?? "" })}
                </div>
              ) : update.latestVersion ? (
                <div className="flex items-center gap-2 text-muted-foreground">
                  <Check size={15} className="text-primary" />
                  {update.currentVersion === "dev"
                    ? t("settings.latestStable", { version: update.latestVersion })
                    : t("settings.upToDate")}
                </div>
              ) : (
                <div className="text-muted-foreground">{t("settings.noRelease")}</div>
              )}
            </div>

            {update.error && (
              <Alert className="mt-3 max-w-2xl border-amber-500/20 bg-amber-500/10 text-amber-800 dark:text-amber-200">
                <AlertDescription className="text-[11px]">{update.error}</AlertDescription>
              </Alert>
            )}
            <div className="mt-4 text-[10px] text-muted-foreground">
              {t("settings.lastChecked", { value: checkedAt })}
            </div>
          </div>

          {(update.available || (update.currentVersion === "dev" && update.latestVersion)) && (
            <Button type="button" onClick={onOpen} className="shrink-0">
              {update.available ? t("settings.download") : t("settings.releasePage")}
              <ExternalLink data-icon="inline-end" />
            </Button>
          )}
        </CardContent>
        <CardFooter className="px-6 py-4 text-[11px] leading-5 text-muted-foreground">
          {t("settings.updatePrivacy")} {t("settings.updateVerify")}
        </CardFooter>
      </Card>

      <Dialog open={configOpen} onOpenChange={setConfigOpen}>
        <DialogContent className="gap-3 sm:max-w-2xl">
          <div className="flex items-start justify-between gap-3 pr-8">
            <DialogHeader className="min-w-0 flex-1 gap-1 pr-0 text-left">
              <DialogTitle>
                {t(
                  configView === "dns"
                    ? "settings.dnsConfigTitle"
                    : "settings.configTitle",
                )}
              </DialogTitle>
              <DialogDescription>
                {t(
                  configView === "dns"
                    ? "settings.dnsConfigDescription"
                    : "settings.configDescription",
                )}
              </DialogDescription>
            </DialogHeader>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="shrink-0"
              onClick={() => void onCopyConfig()}
            >
              <Copy data-icon="inline-start" />
              {t("settings.configCopy")}
            </Button>
          </div>
          <div className="h-[min(60vh,28rem)] overflow-y-auto overscroll-contain rounded-md border border-input bg-muted/30">
            <JsonView value={configText} />
          </div>
        </DialogContent>
      </Dialog>
    </PageShell>
  );
}
