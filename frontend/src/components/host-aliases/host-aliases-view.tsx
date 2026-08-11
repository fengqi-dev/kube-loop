import { useEffect, useRef, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { backend } from "@/backend";
import { PageShell } from "@/components/shared/page-shell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useI18n } from "@/i18n";
import type { HostAlias } from "@/types";

type DraftAlias = HostAlias & { key: string };

function newDraft(domain = "", ip = ""): DraftAlias {
  return { key: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`, domain, ip };
}

export function HostAliasesView({
  contextName,
  ready,
}: {
  contextName: string;
  ready: boolean;
}) {
  const { t } = useI18n();
  const [rows, setRows] = useState<DraftAlias[]>([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const contextGeneration = useRef(0);

  useEffect(() => {
    const generation = ++contextGeneration.current;
    if (!contextName) {
      setRows([]);
      return;
    }
    let active = true;
    setLoading(true);
    backend
      .getHostAliases(contextName)
      .then((items) => {
        if (!active || generation !== contextGeneration.current) return;
        setRows((items ?? []).map((item) => newDraft(item.domain, item.ip)));
      })
      .catch((error) => {
        if (!active || generation !== contextGeneration.current) return;
        toast.error(t("hosts.loadFailed"), {
          description: error instanceof Error ? error.message : String(error),
        });
        setRows([]);
      })
      .finally(() => {
        if (active && generation === contextGeneration.current) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [contextName, t]);

  async function persist(next: DraftAlias[], cleared: boolean): Promise<boolean> {
    if (!contextName) return false;
    const targetContext = contextName;
    const generation = contextGeneration.current;
    const payload = next
      .map((row) => ({ domain: row.domain.trim(), ip: row.ip.trim() }))
      .filter((row) => row.domain || row.ip);
    if (payload.some((row) => !row.domain || !row.ip)) {
      toast.error(t("hosts.saveFailed"), {
        description: t("hosts.incompleteAlias"),
      });
      return false;
    }
    setSaving(true);
    try {
      await backend.setHostAliases(targetContext, payload);
      if (generation !== contextGeneration.current) return true;
      setRows(payload.map((item) => newDraft(item.domain, item.ip)));
      if (cleared || payload.length === 0) {
        toast.success(
          ready ? t("hosts.clearedReconnect") : t("hosts.cleared"),
        );
      } else {
        toast.success(ready ? t("hosts.savedReconnect") : t("hosts.saved"));
      }
      return true;
    } catch (error) {
      if (generation !== contextGeneration.current) return false;
      toast.error(t("hosts.saveFailed"), {
        description: error instanceof Error ? error.message : String(error),
      });
      return false;
    } finally {
      setSaving(false);
    }
  }

  async function removeRow(key: string) {
    const next = rows.filter((row) => row.key !== key);
    // Persist immediately so deleted aliases are cleared from stored config.
    await persist(next, next.length === 0);
  }

  async function clearAll() {
    await persist([], true);
  }

  return (
    <PageShell
      title={t("hosts.title")}
      description={t("hosts.description")}
      action={
        <div className="flex items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={!contextName || saving || rows.length === 0}
            onClick={() => void clearAll()}
          >
            {t("hosts.clearAll")}
          </Button>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={!contextName || saving}
            onClick={() => setRows((current) => [...current, newDraft()])}
          >
            <Plus size={14} />
            {t("hosts.add")}
          </Button>
          <Button
            type="button"
            size="sm"
            disabled={!contextName || saving || loading}
            onClick={() => void persist(rows, rows.length === 0)}
          >
            {t("hosts.save")}
          </Button>
        </div>
      }
    >
      {!contextName ? (
        <p className="text-[13px] text-muted-foreground">{t("hosts.needContext")}</p>
      ) : (
        <div className="space-y-3">
          <p className="text-[12px] text-muted-foreground">
            {t("hosts.contextHint").replace("{name}", contextName)}
            {ready ? ` ${t("hosts.reconnectHint")}` : ""}
          </p>
          <div className="overflow-hidden rounded-lg border border-border">
            <Table className="text-left text-[12px]">
              <TableHeader className="bg-muted/40 text-muted-foreground">
                <TableRow className="hover:bg-transparent">
                  <TableHead className="h-auto px-3 py-2 font-medium">{t("hosts.domain")}</TableHead>
                  <TableHead className="h-auto px-3 py-2 font-medium">{t("hosts.ip")}</TableHead>
                  <TableHead className="h-auto w-12 px-2 py-2" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.length === 0 ? (
                  <TableRow className="hover:bg-transparent">
                    <TableCell colSpan={3} className="px-3 py-8 text-center text-muted-foreground">
                      {loading ? t("hosts.loading") : t("hosts.empty")}
                    </TableCell>
                  </TableRow>
                ) : (
                  rows.map((row) => (
                    <TableRow key={row.key}>
                      <TableCell className="px-3 py-2">
                        <Input
                          className="h-8 w-full rounded-md border border-input bg-background px-2 font-mono text-[12px] outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/40"
                          value={row.domain}
                          placeholder="app.example.dev"
                          disabled={saving}
                          onChange={(event) =>
                            setRows((current) =>
                              current.map((item) =>
                                item.key === row.key
                                  ? { ...item, domain: event.target.value }
                                  : item,
                              ),
                            )
                          }
                        />
                      </TableCell>
                      <TableCell className="px-3 py-2">
                        <Input
                          className="h-8 w-full rounded-md border border-input bg-background px-2 font-mono text-[12px] outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/40"
                          value={row.ip}
                          placeholder="10.96.0.50"
                          disabled={saving}
                          onChange={(event) =>
                            setRows((current) =>
                              current.map((item) =>
                                item.key === row.key
                                  ? { ...item, ip: event.target.value }
                                  : item,
                              ),
                            )
                          }
                        />
                      </TableCell>
                      <TableCell className="px-2 py-2">
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          className="size-8 text-muted-foreground hover:text-destructive"
                          disabled={saving}
                          aria-label={t("hosts.delete")}
                          onClick={() => void removeRow(row.key)}
                        >
                          <Trash2 size={14} />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </div>
        </div>
      )}
    </PageShell>
  );
}
