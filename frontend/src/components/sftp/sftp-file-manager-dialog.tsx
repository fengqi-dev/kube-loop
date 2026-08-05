import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { ContextMenu as ContextMenuPrimitive } from "radix-ui";
import {
  ArrowDownToLine,
  ArrowUpFromLine,
  Ban,
  ChevronUp,
  Eye,
  EyeOff,
  FilePlus2,
  Folder,
  FolderPlus,
  FolderOpen,
  History,
  Loader2,
  Pause,
  Pencil,
  Play,
  RefreshCw,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { backend } from "@/backend";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
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
import { useI18n } from "@/i18n";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import type {
  FileEntry,
  FileManagerTarget,
  FileTransferTask,
  PodInfo,
} from "@/types";

type PaneSide = "local" | "remote";
type TaskTab = "active" | "history";
type PendingOperation =
  | {
      kind: "create";
      side: PaneSide;
      entryType: "file" | "directory";
    }
  | { kind: "rename"; side: PaneSide; entry: FileEntry }
  | { kind: "delete"; side: PaneSide; entry: FileEntry }
  | {
      kind: "overwrite";
      direction: "upload" | "download";
      entry: FileEntry;
    };

export function SFTPFileManagerDialog({
  open,
  onOpenChange,
  contextName,
  pod,
}: {
  open: boolean;
  onOpenChange(open: boolean): void;
  contextName: string;
  pod: PodInfo | null;
}) {
  const { t } = useI18n();
  const [container, setContainer] = useState("");
  const [localPath, setLocalPath] = useState("");
  const [remotePath, setRemotePath] = useState("/");
  const [localEntries, setLocalEntries] = useState<FileEntry[]>([]);
  const [remoteEntries, setRemoteEntries] = useState<FileEntry[]>([]);
  const [localSelected, setLocalSelected] = useState<FileEntry | null>(null);
  const [remoteSelected, setRemoteSelected] = useState<FileEntry | null>(null);
  const [loadingLocal, setLoadingLocal] = useState(false);
  const [loadingRemote, setLoadingRemote] = useState(false);
  const [showLocalHidden, setShowLocalHidden] = useState(false);
  const [showRemoteHidden, setShowRemoteHidden] = useState(false);
  const [tasks, setTasks] = useState<FileTransferTask[]>([]);
  const [taskTab, setTaskTab] = useState<TaskTab>("active");
  const [pendingOperation, setPendingOperation] =
    useState<PendingOperation | null>(null);
  const [operationName, setOperationName] = useState("");
  const [operationBusy, setOperationBusy] = useState(false);

  const target = useMemo<FileManagerTarget | null>(() => {
    if (!pod || !container || !contextName) return null;
    return {
      context: contextName,
      namespace: pod.namespace,
      pod: pod.name,
      podUID: pod.uid,
      container,
    };
  }, [container, contextName, pod]);

  const loadLocal = useCallback(async (nextPath: string) => {
    setLoadingLocal(true);
    try {
      const entries = await backend.listLocalDirectory(nextPath);
      setLocalPath(nextPath);
      setLocalEntries(entries);
      setLocalSelected(null);
    } catch (error) {
      toast.error(t("sftp.localLoadFailed"), { description: errorMessage(error) });
    } finally {
      setLoadingLocal(false);
    }
  }, [t]);

  const loadRemote = useCallback(async (nextPath: string) => {
    if (!target) return;
    setLoadingRemote(true);
    try {
      const entries = await backend.listPodDirectory(target, nextPath);
      setRemotePath(nextPath);
      setRemoteEntries(entries);
      setRemoteSelected(null);
    } catch (error) {
      toast.error(t("sftp.remoteLoadFailed"), { description: errorMessage(error) });
    } finally {
      setLoadingRemote(false);
    }
  }, [t, target]);

  useEffect(() => {
    const unsubscribe = backend.onTransfer((task) => {
      setTasks((current) => {
        const next = current.filter((item) => item.id !== task.id);
        return [task, ...next];
      });
      if (task.status === "completed" && targetMatches(task, target)) {
        void loadLocal(localPath);
        void loadRemote(remotePath);
      }
    });
    void backend.listFileTransfers().then(setTasks).catch(() => undefined);
    return unsubscribe;
  }, [loadLocal, loadRemote, localPath, remotePath, target]);

  useEffect(() => {
    if (!open || !pod) return;
    setContainer(pod.containers[0] ?? "");
    setRemotePath("/");
    setRemoteEntries([]);
    setLocalSelected(null);
    setRemoteSelected(null);
    void backend.localHomeDirectory().then((home) => loadLocal(home));
    void backend.listFileTransfers().then(setTasks);
  }, [loadLocal, open, pod]);

  useEffect(() => {
    if (open && target) void loadRemote("/");
  }, [open, target, loadRemote]);

  async function chooseLocalDirectory() {
    try {
      const selected = await backend.pickLocalDirectory();
      if (selected) await loadLocal(selected);
    } catch (error) {
      toast.error(t("sftp.chooseFailed"), { description: errorMessage(error) });
    }
  }

  async function transfer(
    direction: "upload" | "download",
    selectedEntry?: FileEntry,
  ) {
    if (!target) return;
    const source =
      selectedEntry ??
      (direction === "upload" ? localSelected : remoteSelected);
    if (!source) return;
    const destinationEntries =
      direction === "upload" ? remoteEntries : localEntries;
    const exists = destinationEntries.some((item) => item.name === source.name);
    if (exists) {
      setPendingOperation({ kind: "overwrite", direction, entry: source });
      return;
    }
    try {
      await queueTransfer(direction, source, false);
    } catch (error) {
      toast.error(t("sftp.transferStartFailed"), {
        description: errorMessage(error),
      });
    }
  }

  async function queueTransfer(
    direction: "upload" | "download",
    source: FileEntry,
    overwrite: boolean,
  ) {
    if (!target) return;
    await backend.startFileTransfer({
      direction,
      target,
      sourcePath: source.path,
      destinationDir: direction === "upload" ? remotePath : localPath,
      overwrite,
    });
    toast.success(t("sftp.transferQueued"));
  }

  function createEntry(side: PaneSide, entryType: "file" | "directory") {
    if (side === "remote" && !target) return;
    setOperationName("");
    setPendingOperation({ kind: "create", side, entryType });
  }

  function renameSelected(side: PaneSide, selectedEntry?: FileEntry) {
    const selected =
      selectedEntry ?? (side === "local" ? localSelected : remoteSelected);
    if (!selected || (side === "remote" && !target)) return;
    setOperationName(selected.name);
    setPendingOperation({ kind: "rename", side, entry: selected });
  }

  function deleteSelected(side: PaneSide, selectedEntry?: FileEntry) {
    const selected =
      selectedEntry ?? (side === "local" ? localSelected : remoteSelected);
    if (!selected || (side === "remote" && !target)) return;
    setPendingOperation({ kind: "delete", side, entry: selected });
  }

  async function confirmPendingOperation() {
    const operation = pendingOperation;
    if (!operation || operationBusy) return;
    const name = operationName.trim();
    if (
      (operation.kind === "create" || operation.kind === "rename") &&
      !name
    ) {
      return;
    }
    setOperationBusy(true);
    try {
      switch (operation.kind) {
        case "create":
          if (operation.side === "local") {
            if (operation.entryType === "file") {
              await backend.createLocalFile(localPath, name);
            } else {
              await backend.createLocalDirectory(localPath, name);
            }
            await loadLocal(localPath);
          } else if (target) {
            if (operation.entryType === "file") {
              await backend.createPodFile(target, remotePath, name);
            } else {
              await backend.createPodDirectory(target, remotePath, name);
            }
            await loadRemote(remotePath);
          }
          break;
        case "rename":
          if (name === operation.entry.name) {
            setPendingOperation(null);
            return;
          }
          if (operation.side === "local") {
            await backend.renameLocalPath(operation.entry.path, name);
            await loadLocal(localPath);
          } else if (target) {
            await backend.renamePodPath(target, operation.entry.path, name);
            await loadRemote(remotePath);
          }
          break;
        case "delete":
          if (operation.side === "local") {
            await backend.deleteLocalPath(operation.entry.path);
            await loadLocal(localPath);
          } else if (target) {
            await backend.deletePodPath(target, operation.entry.path);
            await loadRemote(remotePath);
          }
          break;
        case "overwrite":
          await queueTransfer(operation.direction, operation.entry, true);
          break;
      }
      setPendingOperation(null);
    } catch (error) {
      toast.error(
        operation.kind === "overwrite"
          ? t("sftp.transferStartFailed")
          : t("sftp.operationFailed"),
        { description: errorMessage(error) },
      );
    } finally {
      setOperationBusy(false);
    }
  }

  const visibleTasks = tasks.filter((task) => {
    if (!targetMatches(task, target)) return false;
    const active = ["queued", "running", "paused"].includes(task.status);
    return taskTab === "active" ? active : !active;
  });

  return (
    <>
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) setPendingOperation(null);
        onOpenChange(nextOpen);
      }}
    >
      <DialogContent
        className="h-[calc(100vh-1.5rem)] max-h-none w-[calc(100vw-1.5rem)] max-w-[calc(100vw-1.5rem)] sm:max-w-[calc(100vw-1.5rem)] grid-rows-[auto_minmax(0,1fr)_220px] gap-3 p-4"
        onContextMenu={(event) => event.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>
            {t("sftp.managerTitle")} · {pod ? `${pod.namespace}/${pod.name}` : "—"}
          </DialogTitle>
          <DialogDescription className="flex items-center gap-2">
            <span>{t("sftp.container")}</span>
            <Select value={container} onValueChange={setContainer}>
              <SelectTrigger className="h-7 w-52">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(pod?.containers ?? []).map((item) => (
                  <SelectItem key={item} value={item}>{item}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </DialogDescription>
        </DialogHeader>

        <div className="grid min-h-0 grid-cols-[minmax(360px,1fr)_96px_minmax(360px,1fr)] gap-3 overflow-x-auto">
          <FilePane
            side="local"
            title={t("sftp.localComputer")}
            path={localPath}
            entries={localEntries}
            selected={localSelected}
            loading={loadingLocal}
            showHidden={showLocalHidden}
            onShowHiddenChange={(show) => {
              setShowLocalHidden(show);
              if (!show && localSelected?.name.startsWith(".")) {
                setLocalSelected(null);
              }
            }}
            onSelect={setLocalSelected}
            onOpen={(entry) => entry.dir && void loadLocal(entry.path)}
            onPathSubmit={(value) => void loadLocal(value)}
            onUp={() => void loadLocal(localParent(localPath))}
            onRefresh={() => void loadLocal(localPath)}
            onChoose={() => void chooseLocalDirectory()}
            onCreateFile={() => createEntry("local", "file")}
            onCreateDirectory={() => createEntry("local", "directory")}
            onTransfer={(entry) => void transfer("upload", entry)}
            onRename={(entry) => void renameSelected("local", entry)}
            onDelete={(entry) => void deleteSelected("local", entry)}
          />

          <div className="flex flex-col items-stretch justify-center gap-2">
            <Button
              variant="outline"
              disabled={!localSelected || !target}
              onClick={() => void transfer("upload")}
            >
              <ArrowUpFromLine />
              {t("sftp.upload")}
            </Button>
            <Button
              variant="outline"
              disabled={!remoteSelected || !target}
              onClick={() => void transfer("download")}
            >
              <ArrowDownToLine />
              {t("sftp.download")}
            </Button>
          </div>

          <FilePane
            side="remote"
            title={t("sftp.podContainer")}
            path={remotePath}
            entries={remoteEntries}
            selected={remoteSelected}
            loading={loadingRemote}
            showHidden={showRemoteHidden}
            onShowHiddenChange={(show) => {
              setShowRemoteHidden(show);
              if (!show && remoteSelected?.name.startsWith(".")) {
                setRemoteSelected(null);
              }
            }}
            onSelect={setRemoteSelected}
            onOpen={(entry) => entry.dir && void loadRemote(entry.path)}
            onPathSubmit={(value) => void loadRemote(value)}
            onUp={() => void loadRemote(remoteParent(remotePath))}
            onRefresh={() => void loadRemote(remotePath)}
            onCreateFile={() => createEntry("remote", "file")}
            onCreateDirectory={() => createEntry("remote", "directory")}
            onTransfer={(entry) => void transfer("download", entry)}
            onRename={(entry) => void renameSelected("remote", entry)}
            onDelete={(entry) => void deleteSelected("remote", entry)}
          />
        </div>

        <TransferPanel
          tab={taskTab}
          onTabChange={setTaskTab}
          tasks={visibleTasks}
          onClear={() =>
            void backend.clearFileTransferHistory().then(() =>
              setTasks((current) =>
                current.filter((task) =>
                  ["queued", "running", "paused"].includes(task.status),
                ),
              ),
            )
          }
        />
      </DialogContent>
    </Dialog>
    <FileOperationDialog
      operation={pendingOperation}
      name={operationName}
      busy={operationBusy}
      onNameChange={setOperationName}
      onCancel={() => setPendingOperation(null)}
      onConfirm={() => void confirmPendingOperation()}
    />
    </>
  );
}

function FileOperationDialog({
  operation,
  name,
  busy,
  onNameChange,
  onCancel,
  onConfirm,
}: {
  operation: PendingOperation | null;
  name: string;
  busy: boolean;
  onNameChange(value: string): void;
  onCancel(): void;
  onConfirm(): void;
}) {
  const { t } = useI18n();
  const requiresName =
    operation?.kind === "create" || operation?.kind === "rename";
  let title = "";
  let description = "";
  if (operation?.kind === "create") {
    title =
      operation.entryType === "file"
        ? t("sftp.newFile")
        : t("sftp.newFolder");
    description =
      operation.entryType === "file"
        ? t("sftp.newFilePrompt")
        : t("sftp.newFolderPrompt");
  } else if (operation?.kind === "rename") {
    title = t("sftp.rename");
    description = t("sftp.renamePrompt", { name: operation.entry.name });
  } else if (operation?.kind === "delete") {
    title = t("sftp.delete");
    description = t("sftp.deleteConfirm", { path: operation.entry.path });
  } else if (operation?.kind === "overwrite") {
    title = t("sftp.overwrite");
    description = t("sftp.overwriteConfirm", { name: operation.entry.name });
  }

  return (
    <Dialog
      open={Boolean(operation)}
      onOpenChange={(nextOpen) => {
        if (!nextOpen && !busy) onCancel();
      }}
    >
      <DialogContent
        className="sm:max-w-sm"
        onContextMenu={(event) => event.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            onConfirm();
          }}
        >
          {requiresName ? (
            <input
              autoFocus
              value={name}
              disabled={busy}
              onChange={(event) => onNameChange(event.target.value)}
              className="h-8 w-full rounded-md border bg-background px-2 text-sm outline-none focus:border-ring"
            />
          ) : null}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={onCancel}
            >
              {t("actions.cancel")}
            </Button>
            <Button
              type="submit"
              variant={operation?.kind === "delete" ? "destructive" : "default"}
              disabled={busy || (requiresName && !name.trim())}
            >
              {busy ? <Loader2 className="animate-spin" /> : null}
              {t("actions.confirm")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function FilePane({
  side,
  title,
  path,
  entries,
  selected,
  loading,
  showHidden,
  onShowHiddenChange,
  onSelect,
  onOpen,
  onPathSubmit,
  onUp,
  onRefresh,
  onChoose,
  onCreateFile,
  onCreateDirectory,
  onTransfer,
  onRename,
  onDelete,
}: {
  side: PaneSide;
  title: string;
  path: string;
  entries: FileEntry[];
  selected: FileEntry | null;
  loading: boolean;
  showHidden: boolean;
  onShowHiddenChange(show: boolean): void;
  onSelect(entry: FileEntry): void;
  onOpen(entry: FileEntry): void;
  onPathSubmit(path: string): void;
  onUp(): void;
  onRefresh(): void;
  onChoose?(): void;
  onCreateFile(): void;
  onCreateDirectory(): void;
  onTransfer(entry: FileEntry): void;
  onRename(entry?: FileEntry): void;
  onDelete(entry?: FileEntry): void;
}) {
  const { t, locale } = useI18n();
  const [draftPath, setDraftPath] = useState(path);
  const visibleEntries = showHidden
    ? entries
    : entries.filter((entry) => !entry.name.startsWith("."));
  useEffect(() => setDraftPath(path), [path]);
  return (
    <div className="flex min-h-0 flex-col overflow-hidden rounded-lg border bg-card">
      <div className="flex items-center gap-2 border-b px-3 py-2">
        {side === "local" ? <FolderOpen className="size-4" /> : <Folder className="size-4" />}
        <span className="text-xs font-semibold">{title}</span>
        {onChoose ? (
          <Button size="xs" variant="ghost" className="ml-auto" onClick={onChoose}>
            {t("sftp.choose")}
          </Button>
        ) : null}
      </div>
      <div className="flex gap-1 border-b p-2">
        <Button size="icon-sm" variant="ghost" onClick={onUp} aria-label={t("sftp.up")}>
          <ChevronUp />
        </Button>
        <Button size="icon-sm" variant="ghost" onClick={onRefresh} aria-label={t("network.refresh")}>
          <RefreshCw className={cn(loading && "animate-spin")} />
        </Button>
        <Button
          size="icon-sm"
          variant={showHidden ? "secondary" : "ghost"}
          onClick={() => onShowHiddenChange(!showHidden)}
          aria-label={showHidden ? t("sftp.hideHidden") : t("sftp.showHidden")}
          title={showHidden ? t("sftp.hideHidden") : t("sftp.showHidden")}
        >
          {showHidden ? <EyeOff /> : <Eye />}
        </Button>
        <form
          className="min-w-0 flex-1"
          onSubmit={(event) => {
            event.preventDefault();
            onPathSubmit(draftPath);
          }}
        >
          <input
            value={draftPath}
            onChange={(event) => setDraftPath(event.target.value)}
            className="h-7 w-full rounded-md border bg-background px-2 font-mono text-xs outline-none focus:border-ring"
            />
        </form>
        <Button
          size="icon-sm"
          variant="ghost"
          onClick={onCreateFile}
          aria-label={t("sftp.newFile")}
          title={t("sftp.newFile")}
        >
          <FilePlus2 />
        </Button>
        <Button
          size="icon-sm"
          variant="ghost"
          onClick={onCreateDirectory}
          aria-label={t("sftp.newFolder")}
          title={t("sftp.newFolder")}
        >
          <FolderPlus />
        </Button>
        <Button
          size="icon-sm"
          variant="ghost"
          disabled={!selected}
          onClick={() => onRename()}
          aria-label={t("sftp.rename")}
          title={t("sftp.rename")}
        >
          <Pencil />
        </Button>
        <Button
          size="icon-sm"
          variant="ghost"
          disabled={!selected}
          onClick={() => onDelete()}
          aria-label={t("sftp.delete")}
          title={t("sftp.delete")}
        >
          <Trash2 />
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        <div className="sticky top-0 grid grid-cols-[minmax(0,1fr)_90px_150px] border-b bg-muted/80 px-2 py-1.5 text-[11px] text-muted-foreground backdrop-blur">
          <span>{t("sftp.file")}</span>
          <span className="text-right">{t("sftp.size")}</span>
          <span className="text-right">{t("sftp.modified")}</span>
        </div>
        {visibleEntries.map((entry) => (
          <ContextMenuPrimitive.Root key={entry.path}>
            <ContextMenuPrimitive.Trigger asChild>
              <button
                type="button"
                className={cn(
                  "grid w-full grid-cols-[minmax(0,1fr)_90px_150px] items-center border-b px-2 py-1.5 text-left text-xs hover:bg-muted/60",
                  selected?.path === entry.path && "bg-primary/10 text-primary",
                )}
                onClick={() => onSelect(entry)}
                onDoubleClick={() => onOpen(entry)}
                onContextMenu={() => onSelect(entry)}
              >
                <span className="flex min-w-0 items-center gap-2">
                  <Folder className={cn("size-3.5 shrink-0", !entry.dir && "invisible")} />
                  <span className="truncate">{entry.name}</span>
                </span>
                <span className="text-right font-mono text-muted-foreground">
                  {entry.dir ? "—" : formatBytes(entry.size)}
                </span>
                <span className="text-right text-muted-foreground">
                  {new Date(entry.modTime).toLocaleString(locale)}
                </span>
              </button>
            </ContextMenuPrimitive.Trigger>
            <ContextMenuPrimitive.Portal>
              <ContextMenuPrimitive.Content
                className="z-[60] min-w-36 overflow-hidden rounded-lg bg-popover p-1 text-popover-foreground shadow-md ring-1 ring-foreground/10"
              >
                <FileContextMenuItem onSelect={() => onTransfer(entry)}>
                  {side === "local" ? <ArrowUpFromLine /> : <ArrowDownToLine />}
                  {side === "local" ? t("sftp.upload") : t("sftp.download")}
                </FileContextMenuItem>
                <FileContextMenuItem onSelect={() => onRename(entry)}>
                  <Pencil />
                  {t("sftp.rename")}
                </FileContextMenuItem>
                <ContextMenuPrimitive.Separator className="-mx-1 my-1 h-px bg-border" />
                <FileContextMenuItem
                  destructive
                  onSelect={() => onDelete(entry)}
                >
                  <Trash2 />
                  {t("sftp.delete")}
                </FileContextMenuItem>
              </ContextMenuPrimitive.Content>
            </ContextMenuPrimitive.Portal>
          </ContextMenuPrimitive.Root>
        ))}
        {!loading && visibleEntries.length === 0 ? (
          <div className="py-12 text-center text-xs text-muted-foreground">
            {t("sftp.emptyDirectory")}
          </div>
        ) : null}
      </div>
    </div>
  );
}

function FileContextMenuItem({
  children,
  destructive = false,
  onSelect,
}: {
  children: ReactNode;
  destructive?: boolean;
  onSelect(): void;
}) {
  return (
    <ContextMenuPrimitive.Item
      className={cn(
        "flex cursor-default select-none items-center gap-2 rounded-md px-2 py-1.5 text-xs outline-none focus:bg-accent focus:text-accent-foreground [&_svg]:size-3.5",
        destructive &&
          "text-destructive focus:bg-destructive/10 focus:text-destructive",
      )}
      onSelect={onSelect}
    >
      {children}
    </ContextMenuPrimitive.Item>
  );
}

function TransferPanel({
  tab,
  onTabChange,
  tasks,
  onClear,
}: {
  tab: TaskTab;
  onTabChange(tab: TaskTab): void;
  tasks: FileTransferTask[];
  onClear(): void;
}) {
  const { t } = useI18n();
  return (
    <div className="min-h-0 overflow-hidden rounded-lg border bg-card">
      <div className="flex h-9 items-center gap-1 border-b px-2">
        <Button size="sm" variant={tab === "active" ? "secondary" : "ghost"} onClick={() => onTabChange("active")}>
          <RefreshCw />{t("sftp.activeTasks")}
        </Button>
        <Button size="sm" variant={tab === "history" ? "secondary" : "ghost"} onClick={() => onTabChange("history")}>
          <History />{t("sftp.history")}
        </Button>
        {tab === "history" ? (
          <Button size="sm" variant="ghost" className="ml-auto" onClick={onClear}>
            {t("sftp.clearHistory")}
          </Button>
        ) : null}
      </div>
      <div className="h-[174px] overflow-auto">
        {tasks.length === 0 ? (
          <div className="py-14 text-center text-xs text-muted-foreground">{t("sftp.noTasks")}</div>
        ) : tasks.map((task) => <TransferRow key={task.id} task={task} />)}
      </div>
    </div>
  );
}

function TransferRow({ task }: { task: FileTransferTask }) {
  const { t } = useI18n();
  const unknownTotal =
    task.directory && task.direction === "download" && task.totalBytes === 0;
  const percent = task.totalBytes > 0
    ? Math.min(100, Math.round((task.doneBytes / task.totalBytes) * 100))
    : 100;
  const resumable = ["paused", "failed", "stale"].includes(task.status);
  return (
    <div className="grid grid-cols-[22px_minmax(0,1fr)_180px_130px] items-center gap-3 border-b px-3 py-2 text-xs">
      {task.direction === "upload" ? <ArrowUpFromLine /> : <ArrowDownToLine />}
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <span className="truncate font-medium">{baseName(task.sourcePath)}</span>
          <Badge variant={task.status === "failed" || task.status === "stale" ? "destructive" : "secondary"}>
            {t(`sftp.status.${task.status}` as Parameters<typeof t>[0])}
          </Badge>
        </div>
        <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-muted">
          <div
            className={cn(
              "h-full bg-primary transition-[width]",
              unknownTotal && task.status !== "completed" && "animate-pulse",
            )}
            style={{
              width: unknownTotal && task.status !== "completed"
                ? "35%"
                : `${percent}%`,
            }}
          />
        </div>
        {task.error ? <div className="mt-1 truncate text-destructive">{task.error}</div> : null}
      </div>
      <div className="font-mono text-muted-foreground">
        {unknownTotal
          ? formatBytes(task.doneBytes)
          : `${formatBytes(task.doneBytes)} / ${formatBytes(task.totalBytes)} · ${percent}%`}
      </div>
      <div className="flex justify-end gap-1">
        {task.status === "running" || task.status === "queued" ? (
          <Button size="icon-xs" variant="ghost" onClick={() => void backend.pauseFileTransfer(task.id)} aria-label={t("sftp.pause")}>
            <Pause />
          </Button>
        ) : null}
        {resumable ? (
          <Button size="icon-xs" variant="ghost" onClick={() => void backend.resumeFileTransfer(task.id)} aria-label={t("sftp.resume")}>
            <Play />
          </Button>
        ) : null}
        {!["completed", "cancelled"].includes(task.status) ? (
          <Button size="icon-xs" variant="ghost" onClick={() => void backend.cancelFileTransfer(task.id)} aria-label={t("sftp.cancel")}>
            <Ban />
          </Button>
        ) : null}
      </div>
    </div>
  );
}

function targetMatches(task: FileTransferTask, target: FileManagerTarget | null) {
  if (!target) return false;
  if (target.podUID && task.target.podUID) return target.podUID === task.target.podUID;
  return task.target.context === target.context &&
    task.target.namespace === target.namespace &&
    task.target.pod === target.pod;
}

function localParent(value: string) {
  const normalized = value.replace(/[\\/]+$/, "");
  if (/^[A-Za-z]:$/.test(normalized)) return `${normalized}\\`;
  const index = Math.max(normalized.lastIndexOf("/"), normalized.lastIndexOf("\\"));
  if (index <= 0) return value.includes("\\") ? value : "/";
  return normalized.slice(0, index);
}

function remoteParent(value: string) {
  if (value === "/") return "/";
  const parent = value.replace(/\/+$/, "").replace(/\/[^/]+$/, "");
  return parent || "/";
}

function baseName(value: string) {
  return value.split(/[\\/]/).filter(Boolean).pop() ?? value;
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
