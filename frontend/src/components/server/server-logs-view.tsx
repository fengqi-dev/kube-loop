import { useCallback, useEffect, useMemo, useState } from "react";
import { Download, RefreshCw, Search } from "lucide-react";
import { backend } from "@/backend";
import { EmptyState } from "@/components/shared/empty-state";
import { PageShell } from "@/components/shared/page-shell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";

export function ServerLogsView({ profileId }: { profileId: string }) {
  const [lines, setLines] = useState<string[]>([]);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    if (!profileId) {
      setLines([]);
      return;
    }
    setLoading(true);
    try {
      setLines(await backend.serverDataPlaneLogs(profileId));
      setError("");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setLoading(false);
    }
  }, [profileId]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const visible = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    return normalized
      ? lines.filter((line) => line.toLocaleLowerCase().includes(normalized))
      : lines;
  }, [lines, query]);

  function download() {
    const blob = new Blob([visible.join("\n")], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `kubeloop-${new Date().toISOString().replaceAll(":", "-")}.log`;
    anchor.click();
    URL.revokeObjectURL(url);
  }

  return (
    <PageShell
      title="Logs"
      description="Logs from the active Gateway Data Plane session."
      action={
        <div className="flex gap-2">
          <Button type="button" variant="outline" size="sm" disabled={loading || !profileId} onClick={() => void refresh()}>
            <RefreshCw className={loading ? "animate-spin" : ""} size={14} /> Refresh
          </Button>
          <Button type="button" variant="outline" size="sm" disabled={visible.length === 0} onClick={download}>
            <Download size={14} /> Download
          </Button>
        </div>
      }
    >
      <div className="relative mb-3 max-w-md">
        <Search className="absolute top-1/2 left-3 -translate-y-1/2 text-muted-foreground" size={14} />
        <Input className="pl-9" value={query} placeholder="Filter logs" onChange={(event) => setQuery(event.target.value)} />
      </div>
      {!profileId || error ? (
        <EmptyState
          icon={Search}
          title={profileId ? "Logs unavailable" : "Select a Server"}
          detail={error || "Connect to a Gateway before viewing Data Plane logs."}
        />
      ) : (
        <ScrollArea className="h-[calc(100vh-250px)] rounded-lg border bg-zinc-950 p-4">
          <pre className="whitespace-pre-wrap break-all font-mono text-xs leading-5 text-zinc-200">
            {visible.length > 0 ? visible.join("\n") : "No log entries."}
          </pre>
        </ScrollArea>
      )}
    </PageShell>
  );
}
