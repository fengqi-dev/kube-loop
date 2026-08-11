import { useEffect, useState } from "react";
import { Copy, Minus, Plus, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { backend } from "@/backend";
import { PageShell } from "@/components/shared/page-shell";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
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
import { cn } from "@/lib/utils";
import type { MCPClient, MCPStatus } from "@/types";

const mcpClients: MCPClient[] = ["claude", "codex", "cursor", "vscode"];

export function MCPView() {
  const { t } = useI18n();
  const [mcp, setMcp] = useState<MCPStatus | null>(null);
  const [mcpBusy, setMcpBusy] = useState(false);
  const [mcpPort, setMcpPort] = useState("30808");
  const [mcpClient, setMcpClient] = useState<MCPClient>("cursor");

  async function refreshMCP() {
    try {
      const status = await backend.getMCPStatus();
      setMcp(status);
      setMcpPort(String(status.port || 30808));
    } catch (error) {
      toast.error(t("mcp.loadFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    }
  }

  useEffect(() => {
    void refreshMCP();
  }, []);

  async function onToggleMCP() {
    if (!mcp) return;
    setMcpBusy(true);
    try {
      await backend.setMCPEnabled(!mcp.enabled);
      await refreshMCP();
      toast.success(mcp.enabled ? t("mcp.disabledOk") : t("mcp.enabledOk"));
    } catch (error) {
      toast.error(t("mcp.failed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setMcpBusy(false);
    }
  }

  async function onToggleToken() {
    if (!mcp) return;
    setMcpBusy(true);
    try {
      await backend.setMCPTokenEnabled(!mcp.tokenEnabled);
      await refreshMCP();
      toast.success(
        mcp.tokenEnabled ? t("mcp.tokenDisabledOk") : t("mcp.tokenEnabledOk"),
      );
    } catch (error) {
      toast.error(t("mcp.failed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setMcpBusy(false);
    }
  }

  const portLocked = Boolean(mcp?.enabled || mcp?.listening);

  function clampPort(value: number) {
    return Math.min(65535, Math.max(1, value));
  }

  async function commitPort(nextPort: number) {
    if (!mcp || portLocked) return;
    const port = clampPort(nextPort);
    setMcpPort(String(port));
    if (port === mcp.port) return;
    setMcpBusy(true);
    try {
      await backend.setMCPPort(port);
      await refreshMCP();
    } catch (error) {
      setMcpPort(String(mcp.port || 30808));
      toast.error(t("mcp.failed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setMcpBusy(false);
    }
  }

  function nudgePort(delta: number) {
    if (portLocked) return;
    const current = Number(mcpPort);
    const base = Number.isInteger(current) ? current : mcp?.port || 30808;
    void commitPort(base + delta);
  }

  function onPortBlur() {
    if (portLocked) return;
    const port = Number(mcpPort);
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      setMcpPort(String(mcp?.port || 30808));
      toast.error(t("mcp.invalidPort"));
      return;
    }
    void commitPort(port);
  }

  async function onRegenerateMCPToken() {
    setMcpBusy(true);
    try {
      await backend.regenerateMCPToken();
      await refreshMCP();
      toast.success(t("mcp.tokenOk"));
    } catch (error) {
      toast.error(t("mcp.failed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setMcpBusy(false);
    }
  }

  async function onInstallMCPClient() {
    setMcpBusy(true);
    try {
      const result = await backend.installMCPClient(mcpClient);
      await refreshMCP();
      toast.success(t("mcp.installedOk", { client: mcpClientLabel(mcpClient) }), {
        description: result.path,
      });
    } catch (error) {
      toast.error(t("mcp.installFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setMcpBusy(false);
    }
  }

  async function copyText(
    value: string,
    okKey: "mcp.copiedToken" | "mcp.copiedConfig" | "mcp.copiedUrl",
  ) {
    try {
      await navigator.clipboard.writeText(value);
      toast.success(t(okKey));
    } catch (error) {
      toast.error(t("mcp.failed"), {
        description: error instanceof Error ? error.message : String(error),
      });
    }
  }

  function endpointURL() {
    const port = Number(mcpPort);
    const resolved =
      Number.isInteger(port) && port >= 1 && port <= 65535
        ? port
        : mcp?.port || 30808;
    return `http://127.0.0.1:${resolved}/mcp`;
  }

  function formatServerSnippet(entry: Record<string, unknown>) {
    const body = JSON.stringify(entry, null, 2)
      .split("\n")
      .map((line, index) => (index === 0 ? line : `  ${line}`))
      .join("\n");
    return `"kubeloop": ${body}`;
  }

  function clientConfigSnippet() {
    if (!mcp?.url) return "";
    const token = mcp.tokenEnabled ? mcp.token : "";
    if (mcpClient === "codex") {
      if (!token) {
        return `[mcp_servers.kubeloop]
url = ${JSON.stringify(mcp.url)}
`;
      }
      return `[mcp_servers.kubeloop]
url = ${JSON.stringify(mcp.url)}

[mcp_servers.kubeloop.http_headers]
Authorization = ${JSON.stringify(`Bearer ${token}`)}
`;
    }
    const entry: Record<string, unknown> =
      mcpClient === "claude" || mcpClient === "vscode"
        ? { type: "http", url: mcp.url }
        : { url: mcp.url };
    if (token) {
      entry.headers = { Authorization: `Bearer ${token}` };
    }
    return formatServerSnippet(entry);
  }

  function mcpClientLabel(client: MCPClient) {
    switch (client) {
      case "claude":
        return t("mcp.clientClaude");
      case "codex":
        return t("mcp.clientCodex");
      case "vscode":
        return t("mcp.clientVSCode");
      default:
        return t("mcp.clientCursor");
    }
  }

  return (
    <PageShell title={t("mcp.title")} description={t("mcp.description")}>
      <Card className="gap-0 py-0 shadow-none">
        <CardContent className="space-y-4 p-5">
          <div className="flex items-start justify-between gap-6">
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <h3 className="text-[13px] font-semibold">{t("mcp.serverTitle")}</h3>
                <Badge
                  variant="secondary"
                  className={cn(
                    "rounded-md px-1.5 py-0 text-[10px] font-medium",
                    mcp?.listening && "bg-success/15 text-success",
                  )}
                >
                  {mcp?.listening ? t("mcp.enabled") : t("mcp.disabled")}
                </Badge>
              </div>
              <p className="mt-1 text-[11px] text-muted-foreground">{t("mcp.serverDescription")}</p>
            </div>
            <Button
              type="button"
              size="sm"
              variant={mcp?.enabled ? "outline" : "default"}
              disabled={mcpBusy || !mcp}
              onClick={() => void onToggleMCP()}
            >
              {mcpBusy ? <Spinner data-icon="inline-start" /> : null}
              {mcp?.enabled ? t("mcp.disable") : t("mcp.enable")}
            </Button>
          </div>

          {mcp?.error ? (
            <Alert className="border-amber-500/20 bg-amber-500/10 text-amber-800 dark:text-amber-200">
              <AlertDescription className="text-[11px]">{mcp.error}</AlertDescription>
            </Alert>
          ) : null}

          <div className="grid gap-4 sm:grid-cols-[auto_minmax(0,1fr)] sm:items-end">
            <div className="space-y-1.5">
              <div className="text-[11px] font-medium text-muted-foreground">{t("mcp.port")}</div>
              <div
                className={cn(
                  "flex items-center rounded-md border border-input bg-background",
                  portLocked && "opacity-60",
                )}
              >
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  className="h-9 w-9 shrink-0 rounded-none rounded-l-md px-0"
                  disabled={mcpBusy || portLocked || Number(mcpPort) <= 1}
                  onClick={() => nudgePort(-1)}
                  aria-label="-1"
                >
                  <Minus size={14} />
                </Button>
                <Input
                  className="h-9 w-[5.5rem] border-x border-input bg-transparent px-2 text-center font-mono text-[12px] outline-none focus-visible:bg-muted/40 disabled:cursor-not-allowed"
                  value={mcpPort}
                  disabled={mcpBusy || portLocked}
                  onChange={(event) => setMcpPort(event.target.value)}
                  onBlur={onPortBlur}
                  inputMode="numeric"
                />
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  className="h-9 w-9 shrink-0 rounded-none rounded-r-md px-0"
                  disabled={mcpBusy || portLocked || Number(mcpPort) >= 65535}
                  onClick={() => nudgePort(1)}
                  aria-label="+1"
                >
                  <Plus size={14} />
                </Button>
              </div>
            </div>

            <div className="space-y-1.5">
              <div className="text-[11px] font-medium text-muted-foreground">{t("mcp.url")}</div>
              <div className="flex items-center gap-2">
                <div className="flex h-9 min-w-0 flex-1 items-center truncate rounded-md border border-input bg-muted/30 px-3 font-mono text-[12px] text-foreground">
                  {endpointURL()}
                </div>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  className="shrink-0"
                  onClick={() => void copyText(endpointURL(), "mcp.copiedUrl")}
                >
                  <Copy data-icon="inline-start" />
                  {t("mcp.copyUrl")}
                </Button>
              </div>
            </div>
          </div>

          <div className="flex items-start justify-between gap-6 rounded-md border border-border/70 px-3 py-3">
            <div className="min-w-0">
              <h4 className="text-[12px] font-semibold">{t("mcp.tokenAuth")}</h4>
              <p className="mt-1 text-[11px] text-muted-foreground">{t("mcp.tokenAuthDescription")}</p>
            </div>
            <Button
              type="button"
              size="sm"
              variant={mcp?.tokenEnabled ? "outline" : "default"}
              disabled={mcpBusy || !mcp}
              onClick={() => void onToggleToken()}
            >
              {mcpBusy ? <Spinner data-icon="inline-start" /> : null}
              {mcp?.tokenEnabled ? t("mcp.tokenDisable") : t("mcp.tokenEnable")}
            </Button>
          </div>

          {mcp?.tokenEnabled ? (
            <div className="space-y-2">
              <div className="text-[11px] font-medium text-muted-foreground">{t("mcp.token")}</div>
              <div className="flex items-start gap-2">
                <div className="min-w-0 flex-1 break-all font-mono text-[11px] text-muted-foreground">
                  {mcp.token || "—"}
                </div>
                <div className="flex shrink-0 flex-col gap-2">
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={!mcp.token}
                    onClick={() => void copyText(mcp.token || "", "mcp.copiedToken")}
                  >
                    <Copy data-icon="inline-start" />
                    {t("mcp.copyToken")}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={mcpBusy}
                    onClick={() => void onRegenerateMCPToken()}
                  >
                    <RefreshCw data-icon="inline-start" />
                    {t("mcp.regenerate")}
                  </Button>
                </div>
              </div>
            </div>
          ) : null}

          <div className="grid gap-3 sm:grid-cols-[1fr_auto_auto] sm:items-end">
            <label className="block min-w-0">
              <span className="text-[11px] font-medium text-muted-foreground">
                {t("mcp.installClient")}
              </span>
              <div className="mt-1.5">
                <Select value={mcpClient} onValueChange={(value) => setMcpClient(value as MCPClient)}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {mcpClients.map((client) => (
                      <SelectItem key={client} value={client}>
                        {mcpClientLabel(client)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </label>
            <Button
              type="button"
              size="sm"
              disabled={mcpBusy || !mcp}
              onClick={() => void onInstallMCPClient()}
            >
              {mcpBusy ? <Spinner data-icon="inline-start" /> : null}
              {t("mcp.install")}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={!mcp?.url || (Boolean(mcp.tokenEnabled) && !mcp.token)}
              onClick={() => void copyText(clientConfigSnippet(), "mcp.copiedConfig")}
            >
              <Copy data-icon="inline-start" />
              {t("mcp.copyConfig")}
            </Button>
          </div>
        </CardContent>
      </Card>
    </PageShell>
  );
}
