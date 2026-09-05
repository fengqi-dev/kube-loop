import { errorMessage } from "@/lib/errors";
import { useRequestGeneration } from "@/components/workspace/use-request-generation";
import { backend } from "@/backend";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { toast } from "sonner";
import { ServerFileTransfer } from "@/components/server/server-file-transfer";
import { ServerOverviewView } from "@/components/server/server-overview-view";
import { ServerListView } from "@/components/server/server-list-view";
import type { AppView } from "@/components/layout/navigation";
import type {
  AuthSession,
  DataPlaneStatusEvent,
  RemoteInventory,
  ServerDiscovery,
	ServerExchangeInfo,
	ServerMirrorInfo,
	ServerPreviewInfo,
  ServerPortForwardInfo,
  ServerInventoryEvent,
  ServerProfile,
  ServerProfileState,
} from "@/types";
import { ArrowRightLeft, Boxes, Copy, Globe2, LogIn, LogOut, Network, RefreshCw, Server, ShieldCheck, SquareTerminal, Trash2, UserRound } from "lucide-react";
import { lazy, Suspense, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

const ServerExecTerminal = lazy(() => import("@/components/server/server-exec-terminal").then((module) => ({
  default: module.ServerExecTerminal,
})));

export function useServerConnection({
  profiles,
  authSession,
  overviewVisible = true,
  onProfilesChange,
  onAuthChange,
  onNavigate,
  onConnectionChange,
}: {
  profiles: ServerProfileState;
  authSession?: AuthSession;
  overviewVisible?: boolean;
  onProfilesChange?(profiles: ServerProfileState): void;
  onAuthChange?(auth: AuthSession): void;
  onNavigate?(view: AppView): void;
  onConnectionChange?(connected: boolean): void;
}) {
  const inventoryRequests = useRequestGeneration();
  const connectionEpoch = useRef(0);
  const [profileState, setProfileState] = useState(() => normalizeProfileState(profiles));
  const initialProfile = useMemo(
    () => profileState.profiles.find((item) => item.id === profileState.activeProfileId),
    [profileState],
  );
  const [profile, setProfile] = useState<ServerProfile | undefined>(initialProfile);
  const [address, setAddress] = useState(initialProfile?.baseUrl ?? "");
  const [discovery, setDiscovery] = useState<ServerDiscovery>();
  const [auth, setAuth] = useState<AuthSession>({ authenticated: false });
  const [inventory, setInventory] = useState<RemoteInventory>();
	const [forwards, setForwards] = useState<ServerPortForwardInfo[]>([]);
	const [exchanges, setExchanges] = useState<ServerExchangeInfo[]>([]);
	const [exchangeService, setExchangeService] = useState("");
	const [exchangePort, setExchangePort] = useState("");
	const [exchangeLocalHost, setExchangeLocalHost] = useState("127.0.0.1");
	const [exchangeLocalPort, setExchangeLocalPort] = useState("");
	const [mirrors, setMirrors] = useState<ServerMirrorInfo[]>([]);
	const [mirrorService, setMirrorService] = useState("");
	const [mirrorPort, setMirrorPort] = useState("");
	const [mirrorLocalHost, setMirrorLocalHost] = useState("127.0.0.1");
	const [mirrorLocalPort, setMirrorLocalPort] = useState("");
	const [previews, setPreviews] = useState<ServerPreviewInfo[]>([]);
	const [previewName, setPreviewName] = useState("");
	const [previewProtocol, setPreviewProtocol] = useState<"tcp" | "udp">("tcp");
	const [previewServicePort, setPreviewServicePort] = useState("");
	const [previewLocalHost, setPreviewLocalHost] = useState("127.0.0.1");
	const [previewLocalPort, setPreviewLocalPort] = useState("");
	const [sshPod, setSSHPod] = useState("");
	const [sshContainer, setSSHContainer] = useState("");
	const [forwardKind, setForwardKind] = useState<"pod" | "service">("service");
	const [forwardName, setForwardName] = useState("");
	const [forwardRemotePort, setForwardRemotePort] = useState("");
	const [forwardLocalPort, setForwardLocalPort] = useState("");
  const [providerId, setProviderId] = useState("");
  const [busy, setBusy] = useState<"discover" | "switch" | "login" | "refresh-login" | "logout" | "delete" | "inventory" | "tunnel" | "port-forward" | "exchange" | "mirror" | "preview" | "pod-ssh">();
  const [loginCancelBusy, setLoginCancelBusy] = useState(false);
  const loginInFlight = useRef(false);
  const loginCancelled = useRef(false);
  const wasOverviewVisible = useRef(overviewVisible);
  const [error, setError] = useState("");
  const [dataPlaneError, setDataPlaneError] = useState("");
  const [dataPlaneReason, setDataPlaneReason] = useState<DataPlaneStatusEvent["reason"]>();
  const [dataPlaneRetryable, setDataPlaneRetryable] = useState(false);
  const authenticated = authSession?.authenticated ?? auth.authenticated;
  const previousAuth = useRef(authSession?.authenticated ?? false);

  useEffect(() => {
    const next = normalizeProfileState(profiles);
    setProfileState((current) => JSON.stringify(current) === JSON.stringify(next) ? current : next);
  }, [profiles]);

  useEffect(() => {
    onProfilesChange?.(profileState);
  }, [onProfilesChange, profileState]);

  useEffect(() => {
    onAuthChange?.(auth);
  }, [auth, onAuthChange]);

  useEffect(() => {
    const wasAuthenticated = previousAuth.current;
    previousAuth.current = authSession?.authenticated ?? false;
    if (!authSession || authSession.authenticated || !wasAuthenticated) return;
    connectionEpoch.current += 1;
    inventoryRequests.invalidate();
    setBusy(undefined);
    setAuth(authSession);
    setInventory(undefined);
    setDataPlaneError("");
    setDataPlaneReason(undefined);
    setDataPlaneRetryable(false);
    setForwards([]);
    setExchanges([]);
    setMirrors([]);
    setPreviews([]);
  }, [authSession]);

  useEffect(() => {
    if (!initialProfile) return;
    let active = true;
    const epoch = connectionEpoch.current;
    setProfile(initialProfile);
    setAddress(initialProfile.baseUrl);
    setBusy("discover");
    Promise.all([
      backend.testServerAddress(initialProfile.baseUrl),
      backend.serverAuthStatus(initialProfile.id),
    ])
      .then(async ([document, session]) => {
        if (!active || epoch !== connectionEpoch.current) return;
        setDiscovery(document);
        setAuth(session);
        setProviderId(supportedAuthMethods(document.authMethods)[0]?.id ?? "");
        if (session.authenticated) {
          const remoteInventory = await backend.loadServerInventory(
            initialProfile.id,
            initialProfile.lastNamespace ?? "",
          );
          if (!active || epoch !== connectionEpoch.current) return;
          setInventory(remoteInventory);
          setBusy(undefined);

          const sessionResources = await Promise.allSettled([
            backend.listServerPortForwards(initialProfile.id),
            backend.listServerExchanges(initialProfile.id),
            backend.listServerMirrors(initialProfile.id),
            backend.listServerPreviews(initialProfile.id),
          ]);
          if (!active || epoch !== connectionEpoch.current) return;
          const [remoteForwards, remoteExchanges, remoteMirrors, remotePreviews] = sessionResources;
          if (remoteForwards.status === "fulfilled") setForwards(remoteForwards.value);
          if (remoteExchanges.status === "fulfilled") setExchanges(remoteExchanges.value);
          if (remoteMirrors.status === "fulfilled") setMirrors(remoteMirrors.value);
          if (remotePreviews.status === "fulfilled") setPreviews(remotePreviews.value);
        }
      })
      .catch((reason: unknown) => {
        if (active && epoch === connectionEpoch.current) setError(errorMessage(reason));
      })
      .finally(() => {
        if (active && epoch === connectionEpoch.current) setBusy(undefined);
      });
    return () => {
      active = false;
    };
  }, [initialProfile]);

  useEffect(() => {
    const unsubscribe = window.runtime?.EventsOn("dataplane:status", (value: unknown) => {
      const event = value as DataPlaneStatusEvent;
      if (!profile || event.profileId !== profile.id) return;
      setInventory((current) => current ? { ...current, dataPlane: event.status } : current);
      setDataPlaneError(event.status.state === "error"
        ? event.error || "The Data Plane connection could not be restored."
        : "");
      setDataPlaneReason(event.status.state === "error" ? event.reason : undefined);
      setDataPlaneRetryable(event.status.state === "error" && Boolean(event.retryable));
      if (event.status.state === "error") {
        toast.error(dataPlaneFailureTitle(event.reason), {
          description: event.error || "The Data Plane connection could not be restored.",
        });
      }
    });
    return () => unsubscribe?.();
  }, [profile]);

  useEffect(() => {
    if (!onConnectionChange) return;
    onConnectionChange(inventory?.dataPlane?.state === "connected");
  }, [inventory?.dataPlane?.state, onConnectionChange]);

  useEffect(() => {
    const becameVisible = overviewVisible && !wasOverviewVisible.current;
    wasOverviewVisible.current = overviewVisible;
    if (!becameVisible || !authenticated || !profile) return;

    let active = true;
    void Promise.allSettled([
      backend.listServerPortForwards(profile.id),
      backend.listServerExchanges(profile.id),
      backend.listServerMirrors(profile.id),
      backend.listServerPreviews(profile.id),
    ]).then(([remoteForwards, remoteExchanges, remoteMirrors, remotePreviews]) => {
      if (!active) return;
      if (remoteForwards.status === "fulfilled") setForwards(remoteForwards.value);
      if (remoteExchanges.status === "fulfilled") setExchanges(remoteExchanges.value);
      if (remoteMirrors.status === "fulfilled") setMirrors(remoteMirrors.value);
      if (remotePreviews.status === "fulfilled") setPreviews(remotePreviews.value);
    });
    return () => { active = false; };
  }, [authenticated, overviewVisible, profile]);

  useEffect(() => () => {
    if (loginInFlight.current) {
      loginCancelled.current = true;
      void backend.cancelServerLogin();
    }
  }, []);

  useEffect(() => {
    const unsubscribe = window.runtime?.EventsOn("server-inventory:snapshot", (value: unknown) => {
      const event = value as ServerInventoryEvent;
      if (!profile || event.profileId !== profile.id || !event.snapshot) return;
      const snapshot = event.snapshot;
      setInventory((current) => {
        if (!current || current.namespace !== event.namespace) return current;
        if (event.resource === "pods") {
          return { ...current, pods: snapshot.pods ?? [] };
        }
        return { ...current, services: snapshot.services ?? [] };
      });
    });
    return () => unsubscribe?.();
  }, [profile]);

  const authMethods = useMemo(
    () => supportedAuthMethods(discovery?.authMethods ?? []),
    [discovery],
  );
  const selectedProvider = authMethods.find((item) => item.id === providerId);
  const selectedSSHPod = inventory?.pods.find((item) => item.name === sshPod);
  const selectedExchangeService = inventory?.services.find((item) => item.name === exchangeService);
  const selectedExchangePort = selectedExchangeService?.ports.find(
    (item) => `${item.protocol.toLowerCase()}/${item.port}` === exchangePort,
  );
  const selectedMirrorService = inventory?.services.find((item) => item.name === mirrorService);
  const selectedMirrorPort = selectedMirrorService?.ports.find(
    (item) => `${item.protocol.toLowerCase()}/${item.port}` === mirrorPort,
  );

  async function discoverAndSave() {
    if (busy) return;
    setBusy("discover");
    setError("");
    try {
      const normalizedAddress = address.trim();
      const document = await backend.testServerAddress(normalizedAddress);
      const result = await backend.saveServerProfile({
        baseUrl: normalizedAddress,
        displayName: document.serviceId,
        activate: true,
      });
      setAddress(result.profile.baseUrl);
      setProfile(result.profile);
      setProfileState(normalizeProfileState(await backend.serverProfiles()));
      setDiscovery(result.discovery);
      setProviderId(supportedAuthMethods(result.discovery.authMethods)[0]?.id ?? "");
      const session = await backend.serverAuthStatus(result.profile.id);
      setAuth(session);
	  onAuthChange?.(session);
      setProfileState(normalizeProfileState(await backend.serverProfiles()));
      setInventory(session.authenticated
        ? await backend.loadServerInventory(result.profile.id, result.profile.lastNamespace ?? "")
        : undefined);
	  if (session.authenticated) {
		const [remoteForwards, remoteExchanges, remoteMirrors, remotePreviews] = await Promise.all([
		  backend.listServerPortForwards(result.profile.id),
		  backend.listServerExchanges(result.profile.id),
		  backend.listServerMirrors(result.profile.id),
		  backend.listServerPreviews(result.profile.id),
		]);
		setForwards(remoteForwards);
		setExchanges(remoteExchanges);
		setMirrors(remoteMirrors);
		setPreviews(remotePreviews);
	  } else {
		setForwards([]);
		setExchanges([]);
		setMirrors([]);
		setPreviews([]);
	  }
    } catch (reason) {
      setError(errorMessage(reason));
      throw reason;
    } finally {
      setBusy(undefined);
    }
  }

  async function login() {
    if (!profile || !selectedProvider || busy) return;
    setBusy("login");
    setError("");
    loginInFlight.current = true;
    loginCancelled.current = false;
    try {
      let session: AuthSession;
      if (selectedProvider.type === "oidc" || selectedProvider.type === "local") {
        session = await backend.loginServerOIDC(profile.id, selectedProvider.id);
      } else {
        throw new Error("This Gateway advertises an unsupported login method.");
      }
      setAuth(session);
	  onAuthChange?.(session);
	  const nextProfileState = normalizeProfileState(await backend.serverProfiles());
	  const nextProfile = nextProfileState.profiles.find((item) => item.id === profile.id) ?? profile;
	  setProfileState(nextProfileState);
	  setProfile(nextProfile);
      setInventory(await backend.loadServerInventory(profile.id, nextProfile.lastNamespace ?? ""));
	  const [remoteForwards, remoteExchanges, remoteMirrors, remotePreviews] = await Promise.all([
		backend.listServerPortForwards(profile.id),
		backend.listServerExchanges(profile.id),
		backend.listServerMirrors(profile.id),
		backend.listServerPreviews(profile.id),
	  ]);
	  setForwards(remoteForwards);
	  setExchanges(remoteExchanges);
	  setMirrors(remoteMirrors);
	  setPreviews(remotePreviews);
	  onNavigate?.("overview");
    } catch (reason) {
      if (!loginCancelled.current) setError(errorMessage(reason));
    } finally {
      loginInFlight.current = false;
      loginCancelled.current = false;
      setLoginCancelBusy(false);
      setBusy(undefined);
    }
  }

  async function cancelLogin() {
    if (busy !== "login" || loginCancelBusy) return;
    loginCancelled.current = true;
    setLoginCancelBusy(true);
    setError("");
    try {
      await backend.cancelServerLogin();
    } catch (reason) {
      loginCancelled.current = false;
      setLoginCancelBusy(false);
      setError(errorMessage(reason));
    }
  }

  async function logout() {
    if (!profile || busy) return;
    setBusy("logout");
    setError("");
    try {
      await backend.logoutServer(profile.id);
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      clearWorkspaceState();
      onAuthChange?.({ authenticated: false });
      setSSHPod("");
      setSSHContainer("");
      setBusy(undefined);
    }
  }

  async function refreshLogin() {
    if (!profile || busy) return;
    setBusy("refresh-login");
    setError("");
    try {
      const session = await backend.refreshServerLogin(profile.id);
      setAuth(session);
	  onAuthChange?.(session);
	  const nextProfileState = normalizeProfileState(await backend.serverProfiles());
	  setProfileState(nextProfileState);
	  setProfile(nextProfileState.profiles.find((item) => item.id === profile.id) ?? profile);
      setInventory(await backend.loadServerInventory(profile.id, inventory?.namespace ?? profile.lastNamespace ?? ""));
    } catch (reason) {
      setError(errorMessage(reason));
      try {
        const session = await backend.serverAuthStatus(profile.id);
        setAuth(session);
        onAuthChange?.(session);
        if (!session.authenticated) clearWorkspaceState();
      } catch {
        // Preserve the refresh error when credential-state reconciliation also fails.
      }
    } finally {
      setBusy(undefined);
    }
  }

  async function selectProfile(id: string) {
    if (!id || id === profile?.id || busy) return;
    setBusy("switch");
    setError("");
    try {
      const state = normalizeProfileState(await backend.selectServerProfile(id));
      const selected = state.profiles.find((item) => item.id === state.activeProfileId);
      setProfileState(state);
      setProfile(selected);
      setAddress(selected?.baseUrl ?? "");
      setDiscovery(undefined);
      clearWorkspaceState();
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setBusy(undefined);
    }
  }

  function addServer() {
    if (busy) return;
    setProfile(undefined);
    setAddress("");
    setDiscovery(undefined);
    setProviderId("");
    clearWorkspaceState();
  }

  function clearWorkspaceState() {
    connectionEpoch.current += 1;
    inventoryRequests.invalidate();
    setAuth({ authenticated: false });
    setInventory(undefined);
    setDataPlaneError("");
    setDataPlaneReason(undefined);
    setDataPlaneRetryable(false);
    setForwards([]);
    setExchanges([]);
    setMirrors([]);
    setPreviews([]);
  }

  async function removeProfile() {
    if (!profile || busy) return;
    setBusy("delete");
    setError("");
    try {
      const state = normalizeProfileState(await backend.deleteServerProfile(profile.id));
      const selected = state.profiles.find((item) => item.id === state.activeProfileId);
      setProfileState(state);
      setProfile(selected);
      setDiscovery(undefined);
      clearWorkspaceState();
      setProviderId("");
      setAddress(selected?.baseUrl ?? "");
    } catch (reason) {
      setError(errorMessage(reason));
      try {
        const state = normalizeProfileState(await backend.serverProfiles());
        const selected = state.profiles.find((item) => item.id === state.activeProfileId);
        setProfileState(state);
        setProfile(selected);
        setAddress(selected?.baseUrl ?? "");
        setDiscovery(undefined);
        clearWorkspaceState();
      } catch {
        // Preserve the original cleanup error when the local state cannot be reloaded.
      }
    } finally {
      setBusy(undefined);
    }
  }

  async function loadInventory(namespace = inventory?.namespace ?? "") {
    if (!profile || busy) return;
    const isCurrent = inventoryRequests.begin();
    setBusy("inventory");
    setError("");
    setDataPlaneError("");
    setDataPlaneReason(undefined);
    setDataPlaneRetryable(false);
    try {
	  const remoteInventory = await backend.loadServerInventory(profile.id, namespace);
      if (!isCurrent()) return;
	  setInventory(remoteInventory);
	  const [remoteForwards, remoteExchanges, remoteMirrors, remotePreviews] = await Promise.all([
		backend.listServerPortForwards(profile.id),
		backend.listServerExchanges(profile.id),
		backend.listServerMirrors(profile.id),
		backend.listServerPreviews(profile.id),
	  ]);
      if (!isCurrent()) return;
	  setForwards(remoteForwards);
	  setExchanges(remoteExchanges);
	  setMirrors(remoteMirrors);
	  setPreviews(remotePreviews);
	  setForwardName("");
	  setForwardRemotePort("");
	  setExchangeService("");
	  setExchangePort("");
	  setExchangeLocalPort("");
	  setMirrorService("");
	  setMirrorPort("");
	  setMirrorLocalPort("");
	  setPreviewName("");
	  setPreviewServicePort("");
	  setPreviewLocalPort("");
	  setSSHPod("");
	  setSSHContainer("");
    } catch (reason) {
      if (isCurrent()) setError(errorMessage(reason));
    } finally {
      if (isCurrent()) setBusy(undefined);
    }
  }

  async function connectDataPlane(mode: "socks" | "tun") {
    if (!profile || !inventory || inventory.dataPlane?.state === "connected" || busy) return;
    const epoch = connectionEpoch.current;
    setBusy("tunnel");
    setError("");
    try {
      // TUN startup owns helper readiness and installation; avoid forcing a second install here.
      const dataPlane = await backend.connectServerDataPlane(profile.id, mode);
      if (epoch !== connectionEpoch.current) return;
      setInventory({ ...inventory, dataPlane });
      // The Data Plane connect restores local listeners for tasks that were
      // still running on the Gateway, so surface them in the session lists.
      await refreshSessionResources(profile.id);
    } catch (reason) {
      if (epoch === connectionEpoch.current) setError(errorMessage(reason));
    } finally {
      if (epoch === connectionEpoch.current) setBusy(undefined);
    }
  }

  async function refreshSessionResources(profileId: string) {
    const epoch = connectionEpoch.current;
    const [remoteForwards, remoteExchanges, remoteMirrors, remotePreviews] = await Promise.allSettled([
      backend.listServerPortForwards(profileId),
      backend.listServerExchanges(profileId),
      backend.listServerMirrors(profileId),
      backend.listServerPreviews(profileId),
    ]);
    if (epoch !== connectionEpoch.current) return;
    if (remoteForwards.status === "fulfilled") setForwards(remoteForwards.value);
    if (remoteExchanges.status === "fulfilled") setExchanges(remoteExchanges.value);
    if (remoteMirrors.status === "fulfilled") setMirrors(remoteMirrors.value);
    if (remotePreviews.status === "fulfilled") setPreviews(remotePreviews.value);
  }

  async function disconnectDataPlane() {
    if (!profile || !inventory?.dataPlane || inventory.dataPlane.state !== "connected" || busy) return;
    const epoch = connectionEpoch.current;
    setBusy("tunnel");
    setError("");
    try {
      const dataPlane = await backend.disconnectServerDataPlane(profile.id);
      if (epoch !== connectionEpoch.current) return;
      setInventory({ ...inventory, dataPlane });
      // The Data Plane disconnect releases the local listeners while the
      // gateway tasks stay running, so surface the released state locally.
      await refreshSessionResources(profile.id);
    } catch (reason) {
      if (epoch === connectionEpoch.current) setError(errorMessage(reason));
    } finally {
      if (epoch === connectionEpoch.current) setBusy(undefined);
    }
  }

	async function startPortForward() {
		if (!profile || !inventory?.session || busy) return;
		const remotePort = Number(forwardRemotePort);
		const localPort = forwardLocalPort.trim() ? Number(forwardLocalPort) : 0;
		if (!forwardName || !Number.isInteger(remotePort) || remotePort < 1 || remotePort > 65535 ||
			!Number.isInteger(localPort) || localPort < 0 || localPort > 65535) {
			setError("Choose a target and enter valid remote/local ports.");
			return;
		}
		setBusy("port-forward");
		setError("");
		try {
			const service = inventory.services.find((item) => forwardKind === "service" && item.name === forwardName);
			const servicePort = service?.ports.find((item) => item.port === remotePort);
			const info = await backend.startServerPortForward({
				profileId: profile.id,
				kind: forwardKind,
				name: forwardName,
				protocol: servicePort?.protocol.toLowerCase() === "udp" ? "udp" : "tcp",
				remotePort,
				localPort,
			});
			setForwards((current) => [...current, info]);
			setForwardLocalPort("");
		} catch (reason) {
			setError(errorMessage(reason));
		} finally {
			setBusy(undefined);
		}
	}

	async function startExchange() {
		if (!profile || !inventory?.session || !selectedExchangeService || !selectedExchangePort || busy) return;
		if (mirrors.some((item) => item.namespace === inventory.namespace && item.service === selectedExchangeService.name)) {
			setError("Stop the active Mirror for this Service before starting Exchange.");
			return;
		}
		const localPort = Number(exchangeLocalPort);
		if (!exchangeLocalHost.trim() || !Number.isInteger(localPort) || localPort < 1 || localPort > 65535) {
			setError("Enter a valid local host and port for Exchange.");
			return;
		}
		setBusy("exchange");
		setError("");
		try {
			const info = await backend.startServerExchange({
				profileId: profile.id,
				service: selectedExchangeService.name,
				targets: [{
					servicePort: selectedExchangePort.port,
					protocol: selectedExchangePort.protocol.toLowerCase() === "udp" ? "udp" : "tcp",
					localHost: exchangeLocalHost.trim(),
					localPort,
				}],
			});
			setExchanges((current) => [...current.filter((item) => item.id !== info.id), info]);
		} catch (reason) {
			setError(errorMessage(reason));
		} finally {
			setBusy(undefined);
		}
	}

	async function startMirror() {
		if (!profile || !inventory?.session || !selectedMirrorService || !selectedMirrorPort || busy) return;
		if (exchanges.some((item) => item.namespace === inventory.namespace && item.service === selectedMirrorService.name)) {
			setError("Stop the active Exchange for this Service before starting Mirror.");
			return;
		}
		const localPort = Number(mirrorLocalPort);
		if (!mirrorLocalHost.trim() || !Number.isInteger(localPort) || localPort < 1 || localPort > 65535) {
			setError("Enter a valid local host and port for Mirror.");
			return;
		}
		setBusy("mirror");
		setError("");
		try {
			const info = await backend.startServerMirror({
				profileId: profile.id,
				service: selectedMirrorService.name,
				targets: [{
					servicePort: selectedMirrorPort.port,
					protocol: selectedMirrorPort.protocol.toLowerCase() === "udp" ? "udp" : "tcp",
					localHost: mirrorLocalHost.trim(),
					localPort,
				}],
			});
			setMirrors((current) => [...current.filter((item) => item.id !== info.id), info]);
		} catch (reason) {
			setError(errorMessage(reason));
		} finally {
			setBusy(undefined);
		}
	}

	async function startPreview() {
		if (!profile || !inventory?.session || busy) return;
		const servicePort = Number(previewServicePort);
		const localPort = Number(previewLocalPort);
		if (!previewName.trim()) {
			setError("Enter a Kubernetes Service name for Preview.");
			return;
		}
		if (!Number.isInteger(servicePort) || servicePort < 1 || servicePort > 65535 ||
			!previewLocalHost.trim() || !Number.isInteger(localPort) || localPort < 1 || localPort > 65535) {
			setError("Enter valid Service and local target ports for Preview.");
			return;
		}
		setBusy("preview");
		setError("");
		try {
			const info = await backend.startServerPreview({
				profileId: profile.id,
				namespace: inventory.namespace ?? "",
				name: previewName.trim(),
				targets: [{
					servicePort,
					protocol: previewProtocol,
					localHost: previewLocalHost.trim(),
					localPort,
				}],
			});
			setPreviews((current) => [...current.filter((item) => item.id !== info.id), info]);
		} catch (reason) {
			setError(errorMessage(reason));
		} finally {
			setBusy(undefined);
		}
	}

	async function startPodSSH() {
		if (!profile || !inventory?.session || !selectedSSHPod || busy) return;
		setBusy("pod-ssh");
		setError("");
		try {
			await backend.startServerPodSSH({
				profileId: profile.id,
				pod: selectedSSHPod.name,
				container: sshContainer,
			});
		} catch (reason) {
			setError(errorMessage(reason));
		} finally {
			setBusy(undefined);
		}
	}

  async function addServerFromList(serviceAddress: string) {
    if (busy) return;
    setBusy("discover");
    setError("");
    try {
      const document = await backend.testServerAddress(serviceAddress);
      const result = await backend.saveServerProfile({
        baseUrl: serviceAddress,
        displayName: document.serviceId,
        activate: true,
      });
      const state = normalizeProfileState(await backend.serverProfiles());
      setProfileState(state);
      setProfile(result.profile);
      setAddress(result.profile.baseUrl);
      setDiscovery(result.discovery);
      setProviderId(supportedAuthMethods(result.discovery.authMethods)[0]?.id ?? "");
      clearWorkspaceState();
      const session = await backend.serverAuthStatus(result.profile.id);
      setAuth(session);
	  onAuthChange?.(session);
      if (session.authenticated) {
        setInventory(await backend.loadServerInventory(result.profile.id, result.profile.lastNamespace ?? ""));
      }
    } catch (reason) {
      setError(errorMessage(reason));
      throw reason;
    } finally {
      setBusy(undefined);
    }
  }

  async function retestServer(item: ServerProfile) {
    if (busy) return;
    setBusy("discover");
    setError("");
    try {
      const document = await backend.testServerAddress(item.baseUrl);
      if (item.id === profile?.id) setDiscovery(document);
    } catch (reason) {
      setError(errorMessage(reason));
      throw reason;
    } finally {
      setBusy(undefined);
    }
  }

  async function removeServerFromList(id: string) {
    if (busy) return;
    setBusy("delete");
    setError("");
    try {
      const removedActive = id === profile?.id;
      const state = normalizeProfileState(await backend.deleteServerProfile(id));
      setProfileState(state);
      if (removedActive) {
        const selected = state.profiles.find((item) => item.id === state.activeProfileId);
        setProfile(selected);
        setAddress(selected?.baseUrl ?? "");
        setDiscovery(undefined);
        clearWorkspaceState();
      }
    } catch (reason) {
      setError(errorMessage(reason));
      throw reason;
    } finally {
      setBusy(undefined);
    }
  }

  async function editServerFromList(item: ServerProfile, displayName: string, serviceAddress: string) {
    if (busy) return;
    setBusy("discover");
    setError("");
    try {
      const active = item.id === profileState.activeProfileId;
      const result = await backend.saveServerProfile({
        id: item.id,
        baseUrl: serviceAddress,
        displayName,
        activate: active,
      });
      const state = normalizeProfileState(await backend.serverProfiles());
      setProfileState(state);
      if (active) {
        setProfile(result.profile);
        setAddress(result.profile.baseUrl);
        setDiscovery(result.discovery);
      }
    } catch (reason) {
      setError(errorMessage(reason));
      throw reason;
    } finally {
      setBusy(undefined);
    }
  }
  return {
    onNavigate, profileState, profile, address, setAddress, discovery,
    auth, inventory, exchangeService, setExchangeService, exchangePort,
    setExchangePort, exchangeLocalHost, setExchangeLocalHost, exchangeLocalPort, setExchangeLocalPort,
    mirrorService, setMirrorService, mirrorPort, setMirrorPort, mirrorLocalHost,
    setMirrorLocalHost, mirrorLocalPort, setMirrorLocalPort, previewName, setPreviewName,
    previewProtocol, setPreviewProtocol, previewServicePort, setPreviewServicePort, previewLocalHost,
    setPreviewLocalHost, previewLocalPort, setPreviewLocalPort, sshPod, setSSHPod,
    sshContainer, setSSHContainer, forwardKind, setForwardKind, forwardName,
    setForwardName, forwardRemotePort, setForwardRemotePort, forwardLocalPort, setForwardLocalPort,
    providerId, setProviderId, busy, loginCancelBusy, error,
    setError, dataPlaneError, dataPlaneReason, dataPlaneRetryable, authenticated,
    authMethods, selectedProvider, selectedSSHPod, selectedExchangeService, selectedExchangePort,
    selectedMirrorService, selectedMirrorPort, discoverAndSave, login, cancelLogin,
    logout, refreshLogin, selectProfile, addServer, removeProfile,
    loadInventory, connectDataPlane, disconnectDataPlane, startPortForward, startExchange,
    startMirror, startPreview, startPodSSH, addServerFromList, retestServer,
    removeServerFromList, editServerFromList,
  };
}

export type ServerConnection = ReturnType<typeof useServerConnection>;

export function ServerAccessView({ connection, management = false }: {
  connection: ServerConnection;
  management?: boolean;
}) {
  const {
    onNavigate, profileState, profile, address, setAddress, discovery,
    auth, inventory, exchangeService, setExchangeService, exchangePort,
    setExchangePort, exchangeLocalHost, setExchangeLocalHost, exchangeLocalPort, setExchangeLocalPort,
    mirrorService, setMirrorService, mirrorPort, setMirrorPort, mirrorLocalHost,
    setMirrorLocalHost, mirrorLocalPort, setMirrorLocalPort, previewName, setPreviewName,
    previewProtocol, setPreviewProtocol, previewServicePort, setPreviewServicePort, previewLocalHost,
    setPreviewLocalHost, previewLocalPort, setPreviewLocalPort, sshPod, setSSHPod,
    sshContainer, setSSHContainer, forwardKind, setForwardKind, forwardName,
    setForwardName, forwardRemotePort, setForwardRemotePort, forwardLocalPort, setForwardLocalPort,
    providerId, setProviderId, busy, loginCancelBusy, error,
    setError, dataPlaneError, dataPlaneReason, dataPlaneRetryable, authenticated,
    authMethods, selectedProvider, selectedSSHPod, selectedExchangeService, selectedExchangePort,
    selectedMirrorService, selectedMirrorPort, discoverAndSave, login, cancelLogin,
    logout, refreshLogin, selectProfile, addServer, removeProfile,
    loadInventory, connectDataPlane, disconnectDataPlane, startPortForward, startExchange,
    startMirror, startPreview, startPodSSH, addServerFromList, retestServer,
    removeServerFromList, editServerFromList,
  } = connection;


  if (management) {
    return (
      <ServerListView
        profiles={profileState.profiles}
        activeProfileId={profileState.activeProfileId}
        authenticated={authenticated}
        busy={Boolean(busy)}
        error={error}
        onSelect={(id) => selectProfile(id)}
        onAdd={addServerFromList}
        onRetest={retestServer}
        onEdit={editServerFromList}
        onRemove={removeServerFromList}
      />
    );
  }

  if (!management && authenticated && !inventory) {
    const loadingEnvironment = Boolean(profile) && (Boolean(busy) || !error);
    return (
      <div className="mx-auto max-w-[1360px]">
        <Card className="gap-0 overflow-hidden border-border/60 py-0 shadow-sm">
          <CardContent className="grid min-h-[360px] place-items-center p-8 text-center">
            <div className="max-w-sm">
              <div className="mx-auto grid size-14 place-items-center rounded-2xl border border-border/40 bg-primary/5 text-primary ring-1 ring-primary/10">
                {loadingEnvironment ? <Spinner className="size-6" /> : <Server size={22} strokeWidth={1.6} />}
              </div>
              <h2 className="mt-5 text-[15px] font-semibold tracking-tight">
                {loadingEnvironment ? "Loading server environment" : "Server environment unavailable"}
              </h2>
              <p className="mt-2 text-[13px] leading-5 text-muted-foreground">
                {error || (profile
                  ? "Your sign-in is complete. KubeLoop is loading namespaces and the active Gateway session."
                  : "Your sign-in is complete, but no active Server is selected.")}
              </p>
              {!loadingEnvironment ? (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="mt-5 rounded-lg"
                  onClick={() => profile ? void loadInventory(profile.lastNamespace ?? "") : onNavigate?.("clusters")}
                >
                  {profile ? <RefreshCw size={13} data-icon="inline-start" /> : <Server size={13} data-icon="inline-start" />}
                  {profile ? "Retry loading" : "Manage servers"}
                </Button>
              ) : null}
            </div>
          </CardContent>
        </Card>
      </div>
    );
  }

  if (!management && authenticated && inventory && profile && !inventory.namespace) {
    return (
      <div className="mx-auto max-w-[1360px]">
        <Card className="gap-0 overflow-hidden border-border/60 py-0 shadow-sm">
          <CardContent className="grid min-h-[360px] place-items-center p-8 text-center">
            <div className="max-w-sm">
              <div className="mx-auto grid size-14 place-items-center rounded-2xl border border-border/40 bg-primary/5 text-primary ring-1 ring-primary/10">
                <Boxes size={22} strokeWidth={1.6} />
              </div>
              <h2 className="mt-5 text-[15px] font-semibold tracking-tight">Namespace unavailable</h2>
              <p className="mt-2 text-[13px] leading-5 text-muted-foreground">
                No namespace is available under the current Gateway policy. Refresh after access is granted or the namespace list finishes loading.
              </p>
              <Button type="button" variant="outline" size="sm" className="mt-5 rounded-lg" disabled={Boolean(busy)} onClick={() => void loadInventory()}>
                {busy ? <Spinner data-icon="inline-start" /> : <RefreshCw size={13} data-icon="inline-start" />}
                Refresh namespaces
              </Button>
            </div>
          </CardContent>
        </Card>
      </div>
    );
  }

  if (!management && authenticated && inventory && profile) {
    return (
      <ServerOverviewView
        profile={profile}
        discovery={discovery}
        inventory={inventory}
        userName={auth.userName}
        busy={Boolean(busy)}
        tunnelBusy={busy === "tunnel"}
        dataPlaneError={dataPlaneError}
        dataPlaneReason={dataPlaneReason}
        onRefresh={() => void loadInventory()}
        onConnect={connectDataPlane}
        onDisconnect={() => void disconnectDataPlane()}
      />
    );
  }

  return (
    <main className="grid min-h-full content-start justify-items-center bg-background text-foreground">
      <div className={`w-full space-y-5 transition-[max-width] ${authenticated ? "max-w-5xl" : "max-w-xl"}`}>
        <Card className="overflow-hidden border-border/60 shadow-lg">
          <CardHeader className="gap-1.5 border-b border-border/40 bg-muted/20 px-6 py-5">
            <CardTitle className="text-[16px] font-bold tracking-tight">{management ? "Servers" : authenticated ? "Server environment" : profile ? "Sign in to your Gateway" : "Connect to a Gateway"}</CardTitle>
            <CardDescription className="text-[13px] leading-5">
              Enter the service address provided by your administrator. Kubernetes access and
              identity configuration stay in the Gateway.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-5 p-6">
            {profileState.profiles.length > 0 ? (
              <div className="grid gap-2 sm:grid-cols-[1fr_auto]">
                <div className="space-y-2">
                  <Label htmlFor="server-profile" className="text-[12px] font-medium">Server</Label>
                  <select
                    id="server-profile"
                    className="h-9 w-full rounded-lg border border-input bg-transparent px-3 text-sm outline-none transition-colors focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/50"
                    value={profile?.id ?? ""}
                    disabled={Boolean(busy)}
                    onChange={(event) => void selectProfile(event.target.value)}
                  >
                    <option value="" disabled>Select a Server</option>
                    {profileState.profiles.map((item) => (
                      <option key={item.id} value={item.id}>{item.displayName || item.id}</option>
                    ))}
                  </select>
                </div>
                <Button type="button" variant="outline" className="self-end rounded-lg" disabled={Boolean(busy)} onClick={addServer}>
                  <Server size={15} /> Add server
                </Button>
              </div>
            ) : null}

            <div className="space-y-2">
              <Label htmlFor="gateway-address" className="text-[12px] font-medium">Service address</Label>
              <div className="flex gap-2">
                <Input
                  id="gateway-address"
                  type="url"
                  inputMode="url"
                  autoCapitalize="none"
                  autoCorrect="off"
                  placeholder="https://gateway.example.com"
                  value={address}
                  disabled={Boolean(busy)}
                  onChange={(event) => setAddress(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") void discoverAndSave();
                  }}
                />
                <Button
                  type="button"
                  variant={profile ? "outline" : "default"}
                  className="rounded-lg"
                  disabled={Boolean(busy) || !address.trim()}
                  onClick={() => void discoverAndSave()}
                >
                  {busy === "discover" ? <Spinner data-icon="inline-start" /> : <Server size={15} />}
                  {profile ? "Retest" : "Connect"}
                </Button>
              </div>
              <p className="text-[12px] leading-5 text-muted-foreground">
                Use http:// for an unencrypted connection or https:// to enable TLS certificate verification.
              </p>
            </div>

            {discovery ? (
              <div className="rounded-xl border border-border/60 bg-muted/20 p-4 text-sm">
                <div className="flex items-center justify-between gap-3">
                  <div className="min-w-0">
                    <div className="truncate font-semibold">{profile?.displayName || discovery.serviceId}</div>
                    <div className="truncate font-mono text-[11px] text-muted-foreground">
                      {discovery.publicUrl}
                    </div>
                  </div>
                  <Badge variant="outline" className="shrink-0">v{discovery.serverVersion}</Badge>
                </div>
              </div>
            ) : null}

            {discovery && !authenticated ? (
              <div className="space-y-4 border-t border-border/40 pt-5">
                {authMethods.length > 1 ? (
                  <div className="space-y-2">
                    <Label htmlFor="auth-provider" className="text-[12px] font-medium">Sign-in method</Label>
                    <select
                      id="auth-provider"
                      className="h-9 w-full rounded-lg border border-input bg-transparent px-3 text-sm outline-none transition-colors focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/50"
                      value={providerId}
                      disabled={Boolean(busy)}
                      onChange={(event) => setProviderId(event.target.value)}
                    >
                      {authMethods.map((method) => (
                        <option key={method.id} value={method.id}>
                          {method.displayName || method.id}
                        </option>
                      ))}
                    </select>
                  </div>
                ) : null}

                {authMethods.length === 0 ? (
                  <p className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">
                    This Gateway has no login method configured.
                  </p>
                ) : (
                  busy === "login" ? (
                    <Button
                      type="button"
                      variant="outline"
                      className="w-full rounded-lg"
                      disabled={loginCancelBusy}
                      onClick={() => void cancelLogin()}
                    >
                      {loginCancelBusy ? <Spinner data-icon="inline-start" /> : null}
                      {loginCancelBusy ? "Cancelling…" : "Cancel sign-in"}
                    </Button>
                  ) : (
                    <Button
                      type="button"
                      className="w-full rounded-lg"
                      disabled={Boolean(busy) || !selectedProvider}
                      onClick={() => void login()}
                    >
                      <LogIn size={15} />
					  {selectedProvider?.type === "oidc"
					    ? "Continue in browser"
					    : selectedProvider?.type === "local"
						  ? "Continue with local account"
						  : "Sign in"}
                    </Button>
                  )
                )}
              </div>
            ) : null}

            {authenticated ? (
              <div className="flex items-start gap-3 rounded-xl border border-success/30 bg-success/5 p-4">
                <ShieldCheck className="mt-0.5 text-success" size={20} />
                <div>
                  <div className="font-semibold text-success">Signed in securely</div>
                  <div className="mt-1 text-[12px] leading-5 text-muted-foreground">
                    Tokens are stored in the operating system credential vault. Kubernetes data is
                    read through the Gateway; this device does not use kubeconfig.
                  </div>
                </div>
              </div>
            ) : null}

            {!management && authenticated && inventory ? (
              <div className="space-y-4 border-t border-border/40 pt-5">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
                  <div className="grid flex-1 gap-2 sm:max-w-xs">
                    <Label htmlFor="remote-namespace" className="text-[12px] font-medium">Namespace</Label>
                    <select
                      id="remote-namespace"
                      className="h-9 rounded-lg border border-input bg-transparent px-3 text-sm outline-none transition-colors focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/50"
                      value={inventory.namespace ?? ""}
                      disabled={Boolean(busy)}
                      onChange={(event) => void loadInventory(event.target.value)}
                    >
                      {inventory.namespaces.map((namespace) => (
                        <option key={namespace.name} value={namespace.name}>{namespace.name}</option>
                      ))}
                    </select>
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="outline" className="gap-1"><Server size={12} />{profile?.displayName || discovery?.serviceId}</Badge>
                    <Badge variant="outline" className="gap-1"><UserRound size={12} />{auth.userName || "Authenticated user"}</Badge>
                    <Badge variant="outline">Kubernetes {inventory.kubernetesVersion}</Badge>
                    {inventory.gatewayVersion ? <Badge variant="outline">Gateway {inventory.gatewayVersion}</Badge> : null}
                    {inventory.session ? <Badge variant="outline">Session {inventory.session.state}</Badge> : null}
                    {inventory.dataPlane ? (
                      <Badge variant="outline">
                        Data Plane {inventory.dataPlane.state === "connected"
                          ? inventory.dataPlane.mode.toUpperCase()
                          : inventory.dataPlane.state}
                      </Badge>
                    ) : null}
                    <Button type="button" variant="outline" size="sm" className="rounded-lg" disabled={Boolean(busy)} onClick={() => void loadInventory()}>
                      {busy === "inventory" ? <Spinner data-icon="inline-start" /> : <RefreshCw size={14} />}
                      Refresh
                    </Button>
                  </div>
                </div>

                {inventory.network?.issues?.some((issue) => issue.severity === "warning") ? (
                  <div className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm">
                    <div className="font-medium text-destructive">Local network conflict detected</div>
                    <div className="mt-1 text-[12px] text-muted-foreground">
                      {inventory.network.issues
                        .filter((issue) => issue.severity === "warning")
                        .map((issue) => issue.message)
                        .join(" · ")}
                    </div>
                  </div>
                ) : null}

                {inventory.dataPlane?.state === "reconnecting" ? (
                  <div className="flex items-start gap-3 rounded-lg border border-amber-500/30 bg-amber-500/5 p-3 text-sm">
                    <RefreshCw className="mt-0.5 animate-spin text-amber-600" size={17} />
                    <div>
                      <div className="font-medium">Data Plane connection interrupted</div>
                      <div className="mt-1 text-[12px] text-muted-foreground">
                        Cluster traffic is paused while KubeLoop obtains a fresh Session generation and RelayTicket.
                      </div>
                    </div>
                  </div>
                ) : inventory.dataPlane?.state === "error" ? (
                  <div role="alert" className="space-y-3 rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm">
                    <div>
                      <div className="font-medium text-destructive">{dataPlaneFailureTitle(dataPlaneReason)}</div>
                      <div className="mt-1 break-words text-[12px] text-muted-foreground">
                        {dataPlaneError || "The connection could not be restored after bounded retries."}
                      </div>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      {dataPlaneRetryable ? (
                        <Button type="button" size="sm" className="rounded-lg" disabled={Boolean(busy)} onClick={() => void loadInventory()}>
                          {busy === "inventory" ? <Spinner data-icon="inline-start" /> : <RefreshCw size={14} />}
                          {dataPlaneRetryLabel(dataPlaneReason)}
                        </Button>
                      ) : null}
                      {dataPlaneReason === "authentication_required" || dataPlaneReason === "access_denied" ? (
                        <Button type="button" variant={dataPlaneRetryable ? "outline" : "default"} size="sm" className="rounded-lg" disabled={Boolean(busy)} onClick={() => void logout()}>
                          {dataPlaneReason === "access_denied" ? "Use another account" : "Sign in again"}
                        </Button>
                      ) : null}
                    </div>
                  </div>
                ) : inventory.dataPlane ? (
                  <div className="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/20 p-3 text-sm">
                    <div>
                      <div className="font-medium">
                        {inventory.dataPlane.mode === "tun" ? "System TUN is connected" : "Remote cluster proxy is ready"}
                      </div>
                      <div className="mt-1 text-[12px] text-muted-foreground">
                        RelayTicket-bound WSS transport · local SOCKS5 endpoint
                      </div>
                    </div>
                    <div className="flex items-center gap-2">
                      <code className="rounded bg-background px-2 py-1 text-[11px]">{inventory.dataPlane.socksAddress}</code>
                      <Button type="button" variant={inventory.dataPlane.state === "connected" ? "outline" : "default"} size="sm" className="rounded-lg" disabled={Boolean(busy)} onClick={() => inventory.dataPlane?.state === "connected" ? void disconnectDataPlane() : void connectDataPlane("tun")}>
                        {busy === "tunnel" ? <Spinner data-icon="inline-start" /> : <Network size={14} />}
                        {inventory.dataPlane.state === "connected" ? "Disconnect" : "Connect"}
                      </Button>
                    </div>
                  </div>
                ) : null}

				{inventory.dataPlane?.state === "connected" && inventory.capabilities.includes("ports.forward") ? (
					  <div className="space-y-3 rounded-xl border border-border/60 bg-muted/20 p-4">
						<div>
						  <div className="font-medium">Port Forward</div>
						  <div className="mt-1 text-[12px] text-muted-foreground">
							The Gateway resolves the Kubernetes resource; this device opens only a loopback listener.
						  </div>
						</div>
						<div className="grid gap-2 md:grid-cols-[120px_1fr_110px_110px_auto]">
						  <select
							className="h-9 rounded-lg border border-input bg-background px-3 text-sm"
							value={forwardKind}
							disabled={Boolean(busy)}
							onChange={(event) => {
							  setForwardKind(event.target.value as "pod" | "service");
							  setForwardName("");
							  setForwardRemotePort("");
							}}
						  >
						<option value="service">Service</option>
						<option value="pod">Pod</option>
					  </select>
					  <select
							className="h-9 min-w-0 rounded-lg border border-input bg-background px-3 text-sm"
							value={forwardName}
							disabled={Boolean(busy)}
						onChange={(event) => {
						  const name = event.target.value;
						  setForwardName(name);
						  const service = inventory.services.find((item) => forwardKind === "service" && item.name === name);
						  setForwardRemotePort(service?.ports[0] ? String(service.ports[0].port) : "");
						}}
					  >
						<option value="">Select target</option>
						{(forwardKind === "service" ? inventory.services : inventory.pods).map((item) => (
						  <option key={item.name} value={item.name}>{item.name}</option>
						))}
					  </select>
					  <Input
						type="number"
						min={1}
						max={65535}
						placeholder="Remote"
						value={forwardRemotePort}
						disabled={Boolean(busy)}
						onChange={(event) => setForwardRemotePort(event.target.value)}
					  />
					  <Input
						type="number"
						min={1}
						max={65535}
						placeholder="Local auto"
						value={forwardLocalPort}
						disabled={Boolean(busy)}
						onChange={(event) => setForwardLocalPort(event.target.value)}
					  />
					  <Button type="button" size="sm" className="rounded-lg" disabled={Boolean(busy) || !forwardName || !forwardRemotePort} onClick={() => void startPortForward()}>
							{busy === "port-forward" ? <Spinner data-icon="inline-start" /> : <Network size={14} />}
							Forward
						  </Button>
					</div>
				  </div>
				) : null}

				{profile && inventory.session ? (
					  <div className="space-y-3 rounded-xl border border-border/60 bg-muted/20 p-4">
						<div>
						  <div className="font-medium">Preview</div>
						  <div className="mt-1 text-[12px] text-muted-foreground">
							The Gateway creates a temporary owner-bound Service and routes it through the authenticated reverse stream to this device.
						  </div>
						</div>
						{inventory.capabilities.includes("services.preview") ? (
						  <>
							<div className="grid gap-2 md:grid-cols-[140px_1fr_100px_110px_150px_110px_auto]">
						  <Input
							aria-label="Namespace"
							value={inventory.namespace}
							readOnly
							disabled
						  />
						  <Input
							placeholder="Service name"
							value={previewName}
							disabled={Boolean(busy)}
							onChange={(event) => setPreviewName(event.target.value)}
						  />
						  <select
								className="h-9 rounded-lg border border-input bg-background px-3 text-sm"
								value={previewProtocol}
								disabled={Boolean(busy)}
								onChange={(event) => setPreviewProtocol(event.target.value === "udp" ? "udp" : "tcp")}
							  >
							<option value="tcp">TCP</option>
							<option value="udp">UDP</option>
						  </select>
						  <Input
							type="number"
							min={1}
							max={65535}
							placeholder="Service port"
							value={previewServicePort}
							disabled={Boolean(busy)}
							onChange={(event) => {
							  const nextServicePort = event.target.value;
							  const previousServicePort = previewServicePort;
							  setPreviewServicePort(nextServicePort);
							  setPreviewLocalPort((current) => current === "" || current === previousServicePort ? nextServicePort : current);
							}}
						  />
						  <Input
							placeholder="Local host"
							value={previewLocalHost}
							disabled={Boolean(busy)}
							onChange={(event) => setPreviewLocalHost(event.target.value)}
						  />
						  <Input
							type="number"
							min={1}
							max={65535}
							placeholder="Local port"
							value={previewLocalPort}
							disabled={Boolean(busy)}
							onChange={(event) => setPreviewLocalPort(event.target.value)}
						  />
						  <Button type="button" size="sm" className="rounded-lg" disabled={Boolean(busy) || !previewName || !previewServicePort || !previewLocalPort} onClick={() => void startPreview()}>
								{busy === "preview" ? <Spinner data-icon="inline-start" /> : <Globe2 size={14} />}
								Preview
							  </Button>
						</div>
					  </>
					) : (
					  <p className="text-sm text-muted-foreground">Preview is not allowed by Gateway Policy or Kubernetes RBAC.</p>
					)}
				  </div>
				) : null}

				{profile && inventory.session ? (
					  <div className="space-y-3 rounded-xl border border-border/60 bg-muted/20 p-4">
						<div>
						  <div className="font-medium">Exchange</div>
						  <div className="mt-1 text-[12px] text-muted-foreground">
							The Gateway redirects the selected Service port through a Session-bound reverse stream to the local target retained on this device.
						  </div>
						</div>
						{inventory.capabilities.includes("services.exchange") ? (
						  <>
							<div className="grid gap-2 md:grid-cols-[1fr_150px_150px_110px_auto]">
							  <select
								className="h-9 min-w-0 rounded-lg border border-input bg-background px-3 text-sm"
								value={exchangeService}
							disabled={Boolean(busy)}
							onChange={(event) => {
							  const name = event.target.value;
							  const service = inventory.services.find((item) => item.name === name);
							  const port = service?.ports[0];
							  setExchangeService(name);
							  setExchangePort(port ? `${port.protocol.toLowerCase()}/${port.port}` : "");
							  setExchangeLocalPort(port ? String(port.port) : "");
							}}
						  >
							<option value="">Select Service</option>
							{inventory.services
							  .filter((service) => Boolean(service.clusterIp) && service.ports.length > 0)
							  .map((service) => <option key={service.name} value={service.name}>{service.name}</option>)}
						  </select>
						  <select
								className="h-9 rounded-lg border border-input bg-background px-3 text-sm"
								value={exchangePort}
								disabled={Boolean(busy) || !selectedExchangeService}
							onChange={(event) => {
							  setExchangePort(event.target.value);
							  setExchangeLocalPort(event.target.value.split("/")[1] ?? "");
							}}
						  >
							<option value="">Select port</option>
							{selectedExchangeService?.ports.map((port) => (
							  <option key={`${port.protocol}/${port.port}`} value={`${port.protocol.toLowerCase()}/${port.port}`}>
								{port.port}/{port.protocol}
							  </option>
							))}
						  </select>
						  <Input
							placeholder="Local host"
							value={exchangeLocalHost}
							disabled={Boolean(busy)}
							onChange={(event) => setExchangeLocalHost(event.target.value)}
						  />
						  <Input
							type="number"
							min={1}
							max={65535}
							placeholder="Local port"
							value={exchangeLocalPort}
							disabled={Boolean(busy)}
							onChange={(event) => setExchangeLocalPort(event.target.value)}
						  />
						  <Button type="button" size="sm" className="rounded-lg" disabled={Boolean(busy) || !selectedExchangePort || !exchangeLocalPort} onClick={() => void startExchange()}>
								{busy === "exchange" ? <Spinner data-icon="inline-start" /> : <ArrowRightLeft size={14} />}
								Exchange
							  </Button>
						</div>
					  </>
					) : (
					  <p className="text-sm text-muted-foreground">Exchange is not allowed by Gateway Policy or Kubernetes RBAC.</p>
					)}
				  </div>
				) : null}

				{profile && inventory.session ? (
					  <div className="space-y-3 rounded-xl border border-border/60 bg-muted/20 p-4">
						<div>
						  <div className="font-medium">Mirror</div>
						  <div className="mt-1 text-[12px] text-muted-foreground">
							The Gateway keeps the original Service response path active and sends best-effort request copies to the local target. Local responses are discarded.
						  </div>
						</div>
						{inventory.capabilities.includes("services.mirror") ? (
						  <>
							<div className="grid gap-2 md:grid-cols-[1fr_150px_150px_110px_auto]">
							  <select
								className="h-9 min-w-0 rounded-lg border border-input bg-background px-3 text-sm"
								value={mirrorService}
							disabled={Boolean(busy)}
							onChange={(event) => {
							  const name = event.target.value;
							  const service = inventory.services.find((item) => item.name === name);
							  const port = service?.ports[0];
							  setMirrorService(name);
							  setMirrorPort(port ? `${port.protocol.toLowerCase()}/${port.port}` : "");
							  setMirrorLocalPort(port ? String(port.port) : "");
							}}
						  >
							<option value="">Select Service</option>
							{inventory.services
							  .filter((service) => Boolean(service.clusterIp) && service.ports.length > 0)
							  .map((service) => <option key={service.name} value={service.name}>{service.name}</option>)}
						  </select>
						  <select
								className="h-9 rounded-lg border border-input bg-background px-3 text-sm"
								value={mirrorPort}
								disabled={Boolean(busy) || !selectedMirrorService}
							onChange={(event) => {
							  setMirrorPort(event.target.value);
							  setMirrorLocalPort(event.target.value.split("/")[1] ?? "");
							}}
						  >
							<option value="">Select port</option>
							{selectedMirrorService?.ports.map((port) => (
							  <option key={`${port.protocol}/${port.port}`} value={`${port.protocol.toLowerCase()}/${port.port}`}>
								{port.port}/{port.protocol}
							  </option>
							))}
						  </select>
						  <Input
							placeholder="Local host"
							value={mirrorLocalHost}
							disabled={Boolean(busy)}
							onChange={(event) => setMirrorLocalHost(event.target.value)}
						  />
						  <Input
							type="number"
							min={1}
							max={65535}
							placeholder="Local port"
							value={mirrorLocalPort}
							disabled={Boolean(busy)}
							onChange={(event) => setMirrorLocalPort(event.target.value)}
						  />
						  <Button type="button" size="sm" className="rounded-lg" disabled={Boolean(busy) || !selectedMirrorPort || !mirrorLocalPort} onClick={() => void startMirror()}>
								{busy === "mirror" ? <Spinner data-icon="inline-start" /> : <Copy size={14} />}
								Mirror
							  </Button>
						</div>
					  </>
					) : (
					  <p className="text-sm text-muted-foreground">Mirror is not allowed by Gateway Policy or Kubernetes RBAC.</p>
					)}
				  </div>
				) : null}

				{profile && inventory.session ? (
					  <div className="space-y-3 rounded-xl border border-border/60 bg-muted/20 p-4">
						<div>
						  <div className="font-medium">Pod SSH</div>
						  <div className="mt-1 text-[12px] text-muted-foreground">
							A public-key-only SSH endpoint listens on this device. Every command is executed through the authenticated Gateway Session.
						  </div>
						</div>
						{inventory.capabilities.includes("pods.exec") ? (
						  <>
							<div className="grid gap-2 md:grid-cols-[1fr_1fr_auto]">
							  <select
								className="h-9 min-w-0 rounded-lg border border-input bg-background px-3 text-sm"
								value={sshPod}
							disabled={Boolean(busy)}
							onChange={(event) => {
							  const podName = event.target.value;
							  const pod = inventory.pods.find((item) => item.name === podName);
							  setSSHPod(podName);
							  setSSHContainer(pod?.containers[0] ?? "");
							}}
						  >
							<option value="">Select a ready Pod</option>
							{inventory.pods
							  .filter((pod) => pod.ready && Boolean(pod.podIp) && pod.containers.length > 0)
							  .map((pod) => <option key={pod.name} value={pod.name}>{pod.name}</option>)}
						  </select>
						  <select
								className="h-9 min-w-0 rounded-lg border border-input bg-background px-3 text-sm"
								value={sshContainer}
								disabled={Boolean(busy) || !selectedSSHPod}
							onChange={(event) => setSSHContainer(event.target.value)}
						  >
							<option value="">Select container</option>
							{selectedSSHPod?.containers.map((container) => (
							  <option key={container} value={container}>{container}</option>
							))}
						  </select>
						  <Button type="button" size="sm" className="rounded-lg" disabled={Boolean(busy) || !selectedSSHPod || !sshContainer} onClick={() => void startPodSSH()}>
								{busy === "pod-ssh" ? <Spinner data-icon="inline-start" /> : <SquareTerminal size={14} />}
								Enable SSH
							  </Button>
						</div>
					  </>
					) : (
					  <p className="text-sm text-muted-foreground">Pod exec is not allowed by Gateway Policy or Kubernetes RBAC.</p>
					)}
				  </div>
				) : null}

                {profile && inventory.session ? (
                  <Suspense fallback={<div className="grid h-32 place-items-center rounded-xl border border-border/60"><Spinner /></div>}>
                    <ServerExecTerminal
                      key={`${profile.id}:${inventory.session.id}`}
                      profileId={profile.id}
                      pods={inventory.pods}
                      allowed={inventory.capabilities.includes("pods.exec")}
                      onError={setError}
                    />
                  </Suspense>
                ) : null}

                {profile && inventory.session ? (
                  <ServerFileTransfer
                    key={`files:${profile.id}:${inventory.session.id}`}
                    profileId={profile.id}
                    pods={inventory.pods}
                    allowed={inventory.capabilities.includes("pods.files")}
                    manageAllowed={inventory.capabilities.includes("pods.files.manage")}
                    onError={setError}
                  />
                ) : null}
				{inventory.namespaces.length === 0 ? (
                  <p className="rounded-xl border border-border/60 bg-muted/20 p-4 text-sm text-muted-foreground">
                    No namespaces are available under the current Gateway Policy.
                  </p>
                ) : (
                  <div className="grid gap-4 lg:grid-cols-2">
                    <InventoryPanel
                      title="Pods"
                      icon={<Boxes size={16} />}
                      allowed={inventory.capabilities.includes("pods.list")}
                      empty="No Pods in this namespace."
                    >
                      {inventory.pods.map((pod) => (
                        <div key={pod.name} className="flex items-center justify-between gap-3 border-b border-border/40 py-2.5 last:border-0">
                          <div className="min-w-0">
                            <div className="truncate text-sm font-medium">{pod.name}</div>
                            <div className="truncate text-[12px] text-muted-foreground">
                              {pod.containers.join(", ") || "No containers"}{pod.nodeName ? ` · ${pod.nodeName}` : ""}
                            </div>
                          </div>
                          <Badge variant={pod.ready ? "default" : "outline"}>{pod.phase || "Unknown"}</Badge>
                        </div>
                      ))}
                    </InventoryPanel>

                    <InventoryPanel
                      title="Services"
                      icon={<Network size={16} />}
                      allowed={inventory.capabilities.includes("services.list")}
                      empty="No Services in this namespace."
                    >
                      {inventory.services.map((service) => (
                        <div key={service.name} className="flex items-center justify-between gap-3 border-b border-border/40 py-2.5 last:border-0">
                          <div className="min-w-0">
                            <div className="truncate text-sm font-medium">{service.name}</div>
                            <div className="truncate font-mono text-[12px] text-muted-foreground">
                              {service.clusterIp || service.externalName || service.type}
                            </div>
                          </div>
                          <div className="text-right text-[12px] text-muted-foreground">
                            {service.ports.map((port) => `${port.port}/${port.protocol}`).join(", ") || "No ports"}
                          </div>
                        </div>
                      ))}
                    </InventoryPanel>
                  </div>
				)}
              </div>
            ) : null}

            {error ? (
              <p role="alert" className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">
                {error}
              </p>
            ) : null}
          </CardContent>

          {profile ? (
            <CardFooter className="justify-between border-t border-border/40 bg-muted/20 px-6 py-4">
              <Button type="button" variant="ghost" size="sm" className="rounded-lg" disabled={Boolean(busy)} onClick={() => void removeProfile()}>
                {busy === "delete" ? <Spinner data-icon="inline-start" /> : <Trash2 size={14} />}
                Remove server
              </Button>
              {authenticated ? (
                <div className="flex items-center gap-2">
                  <Button type="button" variant="ghost" size="sm" className="rounded-lg" disabled={Boolean(busy)} onClick={() => void refreshLogin()}>
                    {busy === "refresh-login" ? <Spinner data-icon="inline-start" /> : <RefreshCw size={14} />}
                    Refresh login
                  </Button>
                  <Button type="button" variant="outline" size="sm" className="rounded-lg" disabled={Boolean(busy)} onClick={() => void logout()}>
                    {busy === "logout" ? <Spinner data-icon="inline-start" /> : <LogOut size={14} />}
                    Sign out / sign in again
                  </Button>
                </div>
              ) : null}
            </CardFooter>
          ) : null}
        </Card>
      </div>
    </main>
  );

}

function normalizeProfileState(state: ServerProfileState): ServerProfileState {
  return { ...state, profiles: state.profiles ?? [] };
}

function supportedAuthMethods(methods: ServerDiscovery["authMethods"]): ServerDiscovery["authMethods"] {
  return methods;
}

function InventoryPanel({
  title,
  icon,
  allowed,
  empty,
  children,
}: {
  title: string;
  icon: ReactNode;
  allowed: boolean;
  empty: string;
  children: ReactNode;
}) {
  const hasChildren = Array.isArray(children) ? children.length > 0 : Boolean(children);
  return (
    <div className="min-w-0 rounded-xl border border-border/60 bg-card p-4 shadow-sm">
      <div className="mb-3 flex items-center gap-2 text-[14px] font-semibold tracking-tight">{icon}{title}</div>
      {!allowed ? (
        <p className="py-5 text-center text-sm text-muted-foreground">Not allowed by Gateway Policy or Kubernetes RBAC.</p>
      ) : hasChildren ? children : (
        <p className="py-5 text-center text-sm text-muted-foreground">{empty}</p>
      )}
    </div>
  );
}

function dataPlaneFailureTitle(reason: DataPlaneStatusEvent["reason"]): string {
  switch (reason) {
    case "authentication_required": return "Gateway sign-in expired";
    case "access_denied": return "Gateway access was denied";
    case "session_expired": return "Remote Session ended";
    case "session_changed": return "Remote Session changed";
    default: return "Data Plane recovery stopped";
  }
}

function dataPlaneRetryLabel(reason: DataPlaneStatusEvent["reason"]): string {
  if (reason === "session_expired") return "Start new session";
  if (reason === "session_changed") return "Reload session";
  return "Retry connection";
}
