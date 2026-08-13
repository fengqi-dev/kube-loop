import { backend } from "@/backend";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { ServerPodFileEntry } from "@/types";
import { ArrowUp, File, Folder, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";

export function ServerPodFileBrowser({
  profileId,
  pod,
  container,
  allowed,
  onSelect,
  onError,
}: {
  profileId: string;
  pod: string;
  container: string;
  allowed: boolean;
  onSelect: (path: string, kind: "file" | "directory") => void;
  onError: (message: string) => void;
}) {
  const [path, setPath] = useState("/");
  const [items, setItems] = useState<ServerPodFileEntry[]>([]);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setPath("/");
    setItems([]);
  }, [container, pod]);

  async function load(nextPath = path) {
    if (!allowed || !pod || !container || busy) return;
    setBusy(true);
    try {
      const listing = await backend.listServerPodFiles({ profileId, pod, container, path: nextPath.trim() });
      setPath(listing.path);
      setItems(listing.items);
    } catch (reason) {
      onError(messageOf(reason));
    } finally {
      setBusy(false);
    }
  }

  async function create(kind: "file" | "directory") {
    const name = window.prompt(`New ${kind} name`);
    if (!validName(name)) return;
    await mutate(() => backend.createServerPodFile({ profileId, pod, container, path: joinPath(path, name!), kind }));
  }

  async function rename(entry: ServerPodFileEntry) {
    const name = window.prompt("New name", entry.name);
    if (!validName(name) || name === entry.name) return;
    await mutate(() => backend.renameServerPodFile({
      profileId, pod, container, path: entry.path, destination: joinPath(path, name!),
    }));
  }

  async function remove(entry: ServerPodFileEntry) {
    if (!window.confirm(`Delete ${entry.path}?`)) return;
    await mutate(() => backend.deleteServerPodFile({
      profileId, pod, container, path: entry.path, recursive: entry.kind === "directory",
    }));
  }

  async function mutate(operation: () => Promise<{ state: string; result: { error?: string } }>) {
    if (busy) return;
    setBusy(true);
    try {
      const task = await operation();
      if (task.state !== "stopped") throw new Error(task.result.error || "Remote file operation failed.");
      setBusy(false);
      await load(path);
    } catch (reason) {
      onError(messageOf(reason));
      setBusy(false);
    }
  }

  if (!allowed) {
    return <p className="text-xs text-muted-foreground">Remote browsing is not allowed by Gateway Policy or Kubernetes RBAC.</p>;
  }

  return (
    <div className="space-y-2 rounded-md border bg-background p-3">
      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" size="icon-sm" variant="outline" disabled={busy || path === "/" || !pod || !container} onClick={() => void load(parentPath(path))} aria-label="Parent directory">
          <ArrowUp size={14} />
        </Button>
        <Input className="min-w-48 flex-1" value={path} disabled={busy} onChange={(event) => setPath(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") void load(); }} />
        <Button type="button" size="sm" variant="outline" disabled={busy || !pod || !container} onClick={() => void load()}>
          <RefreshCw size={14} className={busy ? "animate-spin" : ""} />Browse
        </Button>
        <Button type="button" size="sm" variant="outline" disabled={busy || !pod || !container} onClick={() => void create("directory")}>
          <Plus size={14} />Folder
        </Button>
        <Button type="button" size="sm" variant="outline" disabled={busy || !pod || !container} onClick={() => void create("file")}>
          <Plus size={14} />File
        </Button>
      </div>
      <div className="max-h-56 divide-y overflow-y-auto rounded-md border">
        {items.length === 0 ? <div className="p-3 text-xs text-muted-foreground">Browse a directory to load its contents.</div> : null}
        {items.map((entry) => {
          const selectable = entry.kind === "file" || entry.kind === "directory";
          return (
            <div key={entry.path} className="flex items-center gap-2 px-2 py-1.5 text-sm" onDoubleClick={() => { if (entry.kind === "directory") void load(entry.path); }}>
              {entry.kind === "directory" ? <Folder size={15} /> : <File size={15} />}
              <button type="button" className="min-w-0 flex-1 truncate text-left" disabled={!selectable} onClick={() => { if (selectable) onSelect(entry.path, entry.kind as "file" | "directory"); }}>
                {entry.name}
              </button>
              <span className="hidden text-xs text-muted-foreground sm:inline">{entry.mode}</span>
              {selectable ? (
                <Button type="button" size="icon-xs" variant="ghost" disabled={busy} onClick={() => void rename(entry)} aria-label={`Rename ${entry.name}`}><Pencil size={12} /></Button>
              ) : null}
              {selectable ? (
                <Button type="button" size="icon-xs" variant="ghost" disabled={busy} onClick={() => void remove(entry)} aria-label={`Delete ${entry.name}`}><Trash2 size={12} /></Button>
              ) : null}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function joinPath(parent: string, name: string) {
  return parent === "/" ? `/${name}` : `${parent.replace(/\/$/, "")}/${name}`;
}

function parentPath(value: string) {
  const parts = value.split("/").filter(Boolean);
  parts.pop();
  return parts.length ? `/${parts.join("/")}` : "/";
}

function validName(value: string | null): value is string {
  return Boolean(value && value !== "." && value !== ".." && !/[\\/\u0000-\u001f\u007f]/.test(value));
}

function messageOf(reason: unknown) {
  return reason instanceof Error ? reason.message : String(reason);
}
