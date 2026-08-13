import { backend } from "@/backend";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { ServerPodFileBrowser } from "@/components/server/server-pod-file-browser";
import type { RemotePod, ServerFileTransferTask } from "@/types";
import { FileArchive, FolderOpen, UploadCloud } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

export function ServerFileTransfer({
  profileId,
  pods,
  allowed,
  manageAllowed,
  onError,
  onTasksChange,
}: {
  profileId: string;
  pods: RemotePod[];
  allowed: boolean;
  manageAllowed: boolean;
  onError: (message: string) => void;
  onTasksChange?: (tasks: ServerFileTransferTask[]) => void;
}) {
  const [direction, setDirection] = useState<"upload" | "download">("upload");
  const [kind, setKind] = useState<"file" | "directory">("file");
  const [pod, setPod] = useState("");
  const [container, setContainer] = useState("");
  const [localPath, setLocalPath] = useState("");
  const [remotePath, setRemotePath] = useState("");
  const [overwrite, setOverwrite] = useState(false);
  const [tasks, setTasks] = useState<ServerFileTransferTask[]>([]);
  const [busy, setBusy] = useState(false);

  const selectedPod = useMemo(() => pods.find((item) => item.name === pod), [pod, pods]);

  useEffect(() => onTasksChange?.(tasks), [onTasksChange, tasks]);

  useEffect(() => {
    let active = true;
    void backend.listServerFileTransfers(profileId)
      .then((items) => { if (active) setTasks(items); })
      .catch((reason: unknown) => { if (active) onError(messageOf(reason)); });
    const unsubscribe = backend.onServerFileTransfer((task) => {
      if (task.profileId !== profileId) return;
      setTasks((current) => {
        const next = current.filter((item) => item.id !== task.id);
        next.unshift(task);
        return next;
      });
    });
    return () => {
      active = false;
      unsubscribe();
    };
  }, [onError, profileId]);

  useEffect(() => {
    if (!selectedPod) {
      setContainer("");
      return;
    }
    if (!selectedPod.containers.includes(container)) {
      setContainer(selectedPod.containers[0] ?? "");
    }
  }, [container, selectedPod]);

  async function pickLocalPath() {
    try {
      const selected = direction === "upload"
        ? await backend.pickServerUploadPath(kind)
        : await backend.pickServerDownloadPath(kind, remotePath.split("/").filter(Boolean).at(-1) || "download");
      if (selected) setLocalPath(selected);
    } catch (reason) {
      onError(messageOf(reason));
    }
  }

  async function start() {
    if (!pod || !container || !localPath || !remotePath.startsWith("/") || remotePath === "/" || busy) {
      onError("Choose a Pod, container and local path, then enter an absolute container path.");
      return;
    }
    setBusy(true);
    try {
      const task = await backend.startServerFileTransfer({
        profileId, direction, kind, pod, container, localPath,
        remotePath: remotePath.trim(), overwrite,
      });
      setTasks((current) => [task, ...current.filter((item) => item.id !== task.id)]);
    } catch (reason) {
      onError(messageOf(reason));
    } finally {
      setBusy(false);
    }
  }

  if (!allowed && !manageAllowed) {
    return (
      <div className="rounded-md border bg-muted/20 p-4">
        <div className="flex items-center gap-2 font-medium"><FileArchive size={16} />File transfer</div>
        <p className="mt-2 text-sm text-muted-foreground">Not allowed by Gateway Policy or Kubernetes RBAC.</p>
      </div>
    );
  }

  return (
    <div className="space-y-3 rounded-md border bg-muted/20 p-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2 font-medium"><FileArchive size={16} />File transfer</div>
          <div className="mt-1 text-xs text-muted-foreground">
            Local files stay on this device; the Gateway performs all Pod operations.
          </div>
          {!allowed ? <div className="mt-1 text-xs text-muted-foreground">Transfer is unavailable; remote browsing remains enabled.</div> : null}
        </div>
      </div>

      <div className="grid gap-2 md:grid-cols-[110px_110px_1fr_1fr]">
        <select
          className="h-9 rounded-md border border-input bg-background px-3 text-sm"
          value={direction}
          disabled={busy || !allowed}
          onChange={(event) => { setDirection(event.target.value as "upload" | "download"); setLocalPath(""); }}
        >
          <option value="upload">Upload</option>
          <option value="download">Download</option>
        </select>
        <select
          className="h-9 rounded-md border border-input bg-background px-3 text-sm"
          value={kind}
          disabled={busy || !allowed}
          onChange={(event) => { setKind(event.target.value as "file" | "directory"); setLocalPath(""); }}
        >
          <option value="file">File</option>
          <option value="directory">Directory</option>
        </select>
        <select
          className="h-9 min-w-0 rounded-md border border-input bg-background px-3 text-sm"
          value={pod}
          disabled={busy}
          onChange={(event) => setPod(event.target.value)}
        >
          <option value="">Select Pod</option>
          {pods.map((item) => <option key={item.name} value={item.name}>{item.name}</option>)}
        </select>
        <select
          className="h-9 min-w-0 rounded-md border border-input bg-background px-3 text-sm"
          value={container}
          disabled={busy || !selectedPod}
          onChange={(event) => setContainer(event.target.value)}
        >
          <option value="">Select container</option>
          {(selectedPod?.containers ?? []).map((item) => <option key={item} value={item}>{item}</option>)}
        </select>
      </div>

      <div className="grid gap-2 md:grid-cols-[1fr_auto]">
        <div className="space-y-1">
          <Label htmlFor="server-file-local-path">Local path</Label>
          <Input id="server-file-local-path" value={localPath} readOnly placeholder="Choose a local path" />
        </div>
        <Button type="button" variant="outline" className="self-end" disabled={busy || !allowed} onClick={() => void pickLocalPath()}>
          <FolderOpen size={14} />Choose
        </Button>
      </div>

      <div className="grid gap-2 md:grid-cols-[1fr_auto_auto]">
        <div className="space-y-1">
          <Label htmlFor="server-file-remote-path">Container path</Label>
          <Input
            id="server-file-remote-path"
            value={remotePath}
            disabled={busy || !allowed}
            placeholder="/workspace/data"
            onChange={(event) => setRemotePath(event.target.value)}
          />
        </div>
        <label className="flex h-9 items-center gap-2 self-end rounded-md border px-3 text-sm">
          <input type="checkbox" checked={overwrite} disabled={busy || !allowed} onChange={(event) => setOverwrite(event.target.checked)} />
          Overwrite
        </label>
        <Button type="button" className="self-end" disabled={busy || !allowed || !pod || !container || !localPath || !remotePath} onClick={() => void start()}>
          {busy ? <Spinner data-icon="inline-start" /> : <UploadCloud size={14} />}
          Start
        </Button>
      </div>

      <ServerPodFileBrowser
        profileId={profileId}
        pod={pod}
        container={container}
        allowed={manageAllowed}
        onError={onError}
        onSelect={(selectedPath, selectedKind) => { setRemotePath(selectedPath); setKind(selectedKind); setLocalPath(""); }}
      />

    </div>
  );
}

function messageOf(reason: unknown) {
  return reason instanceof Error ? reason.message : String(reason);
}
