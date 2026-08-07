import { useEffect, useMemo, useRef, useState } from "react";
import { backend } from "@/backend";
import type {
  BootstrapData,
  ClusterInventory,
  KubeconfigFileInfo,
  ProbeResult,
  SessionState,
  UpdateInfo,
  ConnectionMode,
} from "@/types";
import { appVersion } from "@/version";

export type AppView =
  | "overview"
  | "clusters"
  | "connections"
  | "workload"
  | "network"
  | "host-aliases"
  | "logs"
  | "mcp"
  | "settings";

const minimumDisconnectAnimationMs = 900;

async function waitForDisconnectAnimation(startedAt: number) {
  const remaining = minimumDisconnectAnimationMs - (Date.now() - startedAt);
  if (remaining > 0) {
    await new Promise<void>((resolve) => window.setTimeout(resolve, remaining));
  }
}

const emptySession: SessionState = {
  phase: "idle",
  context: "",
  namespace: "default",
  message: "Disconnected",
  updatedAt: new Date().toISOString(),
};

const emptyUpdate: UpdateInfo = {
  currentVersion: appVersion,
  available: false,
  url: "https://github.com/fengqi-dev/kube-loop/releases",
};

function applyInventory(
  current: BootstrapData,
  inventory: ClusterInventory,
): BootstrapData {
  return {
    ...current,
    contexts: inventory.contexts,
    kubeconfigFiles: inventory.files,
  };
}

export function useSession() {
  const [data, setData] = useState<BootstrapData>({
    contexts: [],
    namespaces: ["default"],
    session: emptySession,
    update: emptyUpdate,
    kubeconfigFiles: [],
  });
  const [contextName, setContextName] = useState("");
  const [namespace, setNamespace] = useState("default");
  const [connectionMode, setConnectionMode] = useState<ConnectionMode>("tun");
  const [view, setView] = useState<AppView>("overview");
  const [loading, setLoading] = useState(true);
  const [uiError, setUIError] = useState("");
  const [updateBusy, setUpdateBusy] = useState(false);
  const [namespacesByContext, setNamespacesByContext] = useState<
    Record<string, string[]>
  >({});
  const contextRequest = useRef(0);
  const latestSelection = useRef({ context: "", namespace: "default" });
  const pendingConnectionAction = useRef<{
    kind: "start" | "stop" | "switch";
    target: string;
  } | null>(null);
  const [connectionActionBusy, setConnectionActionBusy] = useState(false);

  function finishConnectionAction() {
    pendingConnectionAction.current = null;
    setConnectionActionBusy(false);
  }

  function beginConnectionAction(
    kind: "start" | "stop" | "switch",
    target: string,
  ) {
    if (pendingConnectionAction.current) return false;
    pendingConnectionAction.current = { kind, target };
    setConnectionActionBusy(true);
    return true;
  }

  useEffect(() => {
    let active = true;
    const unsubscribe = backend.onSession((session) => {
      if (!active) return;
      setData((current) => ({
        ...current,
        session:
          session.phase === "connected" && current.session.metrics
            ? { ...session, metrics: current.session.metrics }
            : session,
      }));
      const pending = pendingConnectionAction.current;
      if (!pending) return;
      if (
        pending.kind !== "stop" &&
        session.context === pending.target &&
        session.phase !== "idle"
      ) {
        finishConnectionAction();
      }
    });
    const unsubscribeMetrics = backend.onSessionMetrics((metrics) => {
      if (!active) return;
      setData((current) => ({
        ...current,
        session: { ...current.session, metrics },
      }));
    });
    const unsubscribeUpdate = backend.onUpdate((update) => {
      if (active) setData((current) => ({ ...current, update }));
    });
    backend
      .bootstrap()
      .then((initial) => {
        if (!active) return;
        setData(initial);
        const selected =
          initial.preferredContext ||
          initial.contexts.find((item) => item.current)?.name ||
          initial.contexts[0]?.name ||
          "";
        const nextNamespace =
          initial.preferredNamespace ||
          (initial.namespaces.includes("default")
            ? "default"
            : (initial.namespaces[0] ?? "default"));
        setContextName(selected);
        setNamespace(nextNamespace);
        setConnectionMode(initial.session.mode ?? initial.preferredMode ?? "tun");
        if (selected) {
          setNamespacesByContext({ [selected]: initial.namespaces });
        }
        latestSelection.current = {
          context: selected,
          namespace: nextNamespace,
        };
        if (selected) {
          void backend.probeContext(selected).catch(() => undefined);
        }
      })
      .catch((error: Error) => setUIError(error.message))
      .finally(() => setLoading(false));
    return () => {
      active = false;
      unsubscribe();
      unsubscribeMetrics();
      unsubscribeUpdate();
    };
  }, []);

  const session = data.session;
  const sessionBusy =
    session.phase === "checking" ||
    session.phase === "installing-gateway" ||
    session.phase === "discovering-network" ||
    session.phase === "starting-tunnel";
  const busy = sessionBusy || connectionActionBusy;
  const disconnecting =
    connectionActionBusy && pendingConnectionAction.current?.kind === "stop";
  const ready = session.phase === "connected";
  const discovery = session.discovery;
  const activeContextName =
    (connectionActionBusy || sessionBusy || ready) && session.context
      ? session.context
      : contextName;
  const activeNamespaces =
    namespacesByContext[activeContextName] ??
    (activeContextName === contextName ? data.namespaces : []);
  const currentContext = useMemo(
    () => data.contexts.find((item) => item.name === activeContextName),
    [activeContextName, data.contexts],
  );
  const kubeconfigFiles: KubeconfigFileInfo[] = data.kubeconfigFiles ?? [];

  useEffect(() => {
    if (!activeContextName || namespacesByContext[activeContextName]) return;
    let active = true;
    backend
      .namespaces(activeContextName)
      .then((items) => {
        if (!active) return;
        setNamespacesByContext((current) => ({
          ...current,
          [activeContextName]: items,
        }));
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [activeContextName, namespacesByContext]);

  async function changeContext(next: string) {
    const request = ++contextRequest.current;
    setContextName(next);
    setUIError("");
    try {
      const namespaces = await backend.namespaces(next);
      if (request !== contextRequest.current) return;
      setData((current) => ({ ...current, namespaces }));
      setNamespacesByContext((current) => ({ ...current, [next]: namespaces }));
      const nextNamespace = namespaces.includes("default")
        ? "default"
        : (namespaces[0] ?? "default");
      setNamespace(nextNamespace);
      latestSelection.current = { context: next, namespace: nextNamespace };
      await backend.rememberSelection(next, nextNamespace);
      if (request !== contextRequest.current) {
        const latest = latestSelection.current;
        if (latest.context && latest.context !== next) {
          await backend
            .rememberSelection(latest.context, latest.namespace)
            .catch(() => undefined);
        }
        return;
      }
      void backend.probeContext(next).catch(() => undefined);
    } catch (error) {
      if (request === contextRequest.current) {
        setUIError((error as Error).message);
      }
    }
  }

  async function changeNamespace(next: string) {
    setNamespace(next);
    if (!contextName) return;
    latestSelection.current = { context: contextName, namespace: next };
    try {
      await backend.rememberSelection(contextName, next);
    } catch (error) {
      setUIError((error as Error).message);
    }
  }

  async function toggleConnection() {
    const stopping = sessionBusy || ready;
    const target = stopping ? session.context : contextName;
    if (!beginConnectionAction(stopping ? "stop" : "start", target)) return;
    setUIError("");
    try {
      if (stopping) {
        const startedAt = Date.now();
        await backend.disconnect();
        await waitForDisconnectAnimation(startedAt);
        finishConnectionAction();
      } else {
        if (session.phase === "error") {
          await backend.disconnect();
        }
        // Namespace seeds DNS short-name search (default.svc.cluster.local…).
        await backend.connectMode(contextName, "default", connectionMode);
      }
    } catch (error) {
      finishConnectionAction();
      setUIError((error as Error).message);
    }
  }

  async function connectContext(next: string) {
    const stopping = sessionBusy || (ready && session.context === next);
    const switching = ready && session.context !== next;
    const kind = stopping ? "stop" : switching ? "switch" : "start";
    if (!beginConnectionAction(kind, stopping ? session.context : next)) return;
    setUIError("");
    try {
      if (sessionBusy) {
        const startedAt = Date.now();
        await backend.disconnect();
        await waitForDisconnectAnimation(startedAt);
        finishConnectionAction();
        return;
      }
      if (ready && session.context === next) {
        const startedAt = Date.now();
        await backend.disconnect();
        await waitForDisconnectAnimation(startedAt);
        finishConnectionAction();
        return;
      }
      if (ready && session.context !== next) {
        await backend.disconnect();
      }
      if (session.phase === "error") {
        await backend.disconnect();
      }
      if (next !== contextName) {
        await changeContext(next);
      }
      await backend.connectMode(next, "default", connectionMode);
    } catch (error) {
      finishConnectionAction();
      setUIError((error as Error).message);
    }
  }

  async function reloadContexts() {
    setUIError("");
    const inventory = await backend.reloadContexts();
    setData((current) => applyInventory(current, inventory));
    if (contextName && !inventory.contexts.some((item) => item.name === contextName)) {
      const fallback =
        inventory.contexts.find((item) => item.current)?.name ||
        inventory.contexts[0]?.name ||
        "";
      if (fallback) {
        await changeContext(fallback);
      } else {
        setContextName("");
      }
    }
    return inventory;
  }

  async function addKubeconfig() {
    setUIError("");
    const inventory = await backend.addKubeconfig();
    setData((current) => applyInventory(current, inventory));
    return inventory;
  }

  async function removeKubeconfig(path: string) {
    setUIError("");
    const inventory = await backend.removeKubeconfig(path);
    setData((current) => applyInventory(current, inventory));
    if (contextName && !inventory.contexts.some((item) => item.name === contextName)) {
      const fallback =
        inventory.contexts.find((item) => item.current)?.name ||
        inventory.contexts[0]?.name ||
        "";
      if (fallback) {
        await changeContext(fallback);
      } else {
        setContextName("");
      }
    }
    return inventory;
  }

  async function probeContext(name: string): Promise<ProbeResult> {
    return backend.probeContext(name);
  }

  async function checkForUpdates() {
    setUpdateBusy(true);
    try {
      const update = await backend.checkForUpdates();
      setData((current) => ({ ...current, update }));
    } catch (error) {
      setUIError((error as Error).message);
    } finally {
      setUpdateBusy(false);
    }
  }

  async function openUpdatePage() {
    try {
      await backend.openUpdatePage();
    } catch (error) {
      setUIError((error as Error).message);
    }
  }

  return {
    data,
    contextName,
    activeContextName,
    activeNamespaces,
    namespace,
    connectionMode,
    view,
    setView,
    loading,
    uiError,
    updateBusy,
    session,
    busy,
    disconnecting,
    ready,
    discovery,
    currentContext,
    kubeconfigFiles,
    setNamespace: changeNamespace,
    setConnectionMode,
    changeContext,
    toggleConnection,
    connectContext,
    reloadContexts,
    addKubeconfig,
    removeKubeconfig,
    probeContext,
    checkForUpdates,
    openUpdatePage,
  };
}
