import { backend } from "@/backend";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import type { RemotePod, ServerExecEvent, ServerExecTask } from "@/types";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { SquareTerminal, StopCircle } from "lucide-react";
import { useEffect, useRef, useState } from "react";

export function ServerExecTerminal({
  profileId,
  pods,
  allowed,
  onError,
}: {
  profileId: string;
  pods: RemotePod[];
  allowed: boolean;
  onError: (message: string) => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal>();
  const fitRef = useRef<FitAddon>();
  const activeTaskRef = useRef<ServerExecTask>();
  const pendingEventsRef = useRef<ServerExecEvent[]>([]);
  const inputQueueRef = useRef(Promise.resolve());
  const [podName, setPodName] = useState("");
  const [containerName, setContainerName] = useState("");
  const [shell, setShell] = useState("/bin/sh");
  const [task, setTask] = useState<ServerExecTask>();
  const [busy, setBusy] = useState(false);

  const pod = pods.find((item) => item.name === podName);

  useEffect(() => {
    if (!hostRef.current) return;
    const terminal = new Terminal({
      cursorBlink: true,
      convertEol: false,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
      fontSize: 13,
      scrollback: 5000,
      theme: { background: "#09090b", foreground: "#fafafa", cursor: "#fafafa" },
    });
    const fit = new FitAddon();
    terminal.loadAddon(fit);
    terminal.open(hostRef.current);
    fit.fit();
    terminal.writeln("KubeLoop Gateway terminal ready.\r\n");
    terminalRef.current = terminal;
    fitRef.current = fit;
    const dataSubscription = terminal.onData((data) => {
      const current = activeTaskRef.current;
      if (!current) return;
      inputQueueRef.current = inputQueueRef.current
        .then(() => backend.writeServerExecInput(profileId, current.id, data))
        .catch((reason: unknown) => onError(messageOf(reason)));
    });
    const resizeSubscription = terminal.onResize(({ cols, rows }) => {
      const current = activeTaskRef.current;
      if (current) void backend.resizeServerExec(profileId, current.id, cols, rows).catch((reason: unknown) => onError(messageOf(reason)));
    });
    const observer = new ResizeObserver(() => fit.fit());
    observer.observe(hostRef.current);
    return () => {
      observer.disconnect();
      dataSubscription.dispose();
      resizeSubscription.dispose();
      const current = activeTaskRef.current;
      activeTaskRef.current = undefined;
      if (current) void backend.stopServerExec(profileId, current.id).catch(() => undefined);
      terminal.dispose();
      terminalRef.current = undefined;
      fitRef.current = undefined;
    };
  }, [onError, profileId]);

  useEffect(() => backend.onServerExec((event) => {
    if (event.profileId !== profileId) return;
    const current = activeTaskRef.current;
    if (!current || current.id !== event.taskId) {
      pendingEventsRef.current = [...pendingEventsRef.current.slice(-31), event];
      return;
    }
    handleEvent(event);
  }), [profileId]);

  function handleEvent(event: ServerExecEvent) {
    const terminal = terminalRef.current;
    if (!terminal) return;
    if ((event.type === "stdout" || event.type === "stderr") && event.data) {
      terminal.write(decodeBase64(event.data));
      return;
    }
    if (event.type === "exit") {
      terminal.writeln(`\r\n[process exited with code ${event.exitCode ?? 0}${event.cancelled ? ", cancelled" : ""}]`);
    } else if (event.type === "error") {
      terminal.writeln(`\r\n[${event.error || "terminal stream failed"}]`);
    }
    activeTaskRef.current = undefined;
    setTask(undefined);
  }

  async function start() {
    if (!pod || !containerName || !shell.trim() || busy || !terminalRef.current) return;
    setBusy(true);
    onError("");
    try {
      terminalRef.current.reset();
      const created = await backend.startServerExec({
        profileId,
        pod: pod.name,
        container: containerName,
        command: [shell.trim()],
        tty: true,
        width: terminalRef.current.cols,
        height: terminalRef.current.rows,
      });
      activeTaskRef.current = created;
      setTask(created);
      const pending = pendingEventsRef.current.filter((event) => event.taskId === created.id);
      pendingEventsRef.current = pendingEventsRef.current.filter((event) => event.taskId !== created.id);
      pending.forEach(handleEvent);
      terminalRef.current.focus();
    } catch (reason) {
      onError(messageOf(reason));
      terminalRef.current.writeln(`\r\n[failed to start terminal: ${messageOf(reason)}]`);
    } finally {
      setBusy(false);
    }
  }

  async function stop() {
    const current = activeTaskRef.current;
    if (!current || busy) return;
    setBusy(true);
    try {
      await backend.stopServerExec(profileId, current.id);
      terminalRef.current?.writeln("\r\n[terminal stopped locally]");
      activeTaskRef.current = undefined;
      setTask(undefined);
    } catch (reason) {
      onError(messageOf(reason));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-3 rounded-md border bg-muted/20 p-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2 font-medium"><SquareTerminal size={16} /> Pod terminal</div>
          <div className="mt-1 text-xs text-muted-foreground">
            Interactive exec runs in the Control Plane and is authorized by Gateway Policy plus Kubernetes RBAC.
          </div>
        </div>
        {task ? <span className="font-mono text-xs text-muted-foreground">{task.id.slice(0, 8)}</span> : null}
      </div>
      <div className="grid gap-2 md:grid-cols-[1fr_1fr_160px_auto]">
        <select
          className="h-9 min-w-0 rounded-md border border-input bg-background px-3 text-sm"
          value={podName}
          disabled={busy || Boolean(task) || !allowed}
          onChange={(event) => {
            const nextPod = pods.find((item) => item.name === event.target.value);
            setPodName(event.target.value);
            setContainerName(nextPod?.containers[0] ?? "");
          }}
        >
          <option value="">Select Pod</option>
          {pods.map((item) => <option key={item.name} value={item.name}>{item.name}</option>)}
        </select>
        <select
          className="h-9 min-w-0 rounded-md border border-input bg-background px-3 text-sm"
          value={containerName}
          disabled={busy || Boolean(task) || !pod}
          onChange={(event) => setContainerName(event.target.value)}
        >
          <option value="">Select container</option>
          {pod?.containers.map((name) => <option key={name} value={name}>{name}</option>)}
        </select>
        <Input value={shell} disabled={busy || Boolean(task) || !allowed} aria-label="Shell command" onChange={(event) => setShell(event.target.value)} />
        {task ? (
          <Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => void stop()}>
            {busy ? <Spinner data-icon="inline-start" /> : <StopCircle size={14} />} Stop
          </Button>
        ) : (
          <Button type="button" size="sm" disabled={busy || !allowed || !pod || !containerName || !shell.trim()} onClick={() => void start()}>
            {busy ? <Spinner data-icon="inline-start" /> : <SquareTerminal size={14} />} Open
          </Button>
        )}
      </div>
      {!allowed ? <p className="text-xs text-muted-foreground">Pod exec is not allowed by Gateway Policy or Kubernetes RBAC.</p> : null}
      <div ref={hostRef} className="h-80 overflow-hidden rounded-md border bg-[#09090b] p-2" />
    </div>
  );
}

function decodeBase64(value: string) {
  const decoded = atob(value);
  const bytes = new Uint8Array(decoded.length);
  for (let index = 0; index < decoded.length; index += 1) bytes[index] = decoded.charCodeAt(index);
  return bytes;
}

function messageOf(reason: unknown) {
  return reason instanceof Error ? reason.message : String(reason);
}
