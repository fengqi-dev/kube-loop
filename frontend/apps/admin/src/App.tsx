import { ReactNode, useCallback, useEffect, useMemo, useState } from "react";
import {
  Activity,
  BookOpenCheck,
  Boxes,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  CircleUserRound,
  FileClock,
  KeyRound,
  Languages,
  LayoutDashboard,
  LogOut,
  Menu,
  Network,
  RefreshCw,
  ServerCog,
  ShieldCheck,
  UsersRound,
  X,
} from "lucide-react";
import {
  ApiError,
  authenticationLostEvent,
  csrfStorageKey,
  finishOIDCCallback,
  logout,
  managementBase,
  mutation,
  request,
  startOIDC,
  waitForAuditExport,
} from "./api";
import {
  Button,
  ConfirmDialog,
  Empty,
  Loading,
  Metric,
  Notice,
  PageHeader,
  SearchInput,
  SecureActionDialog,
} from "./components";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  detectLocale,
  I18nContext,
  localeStorageKey,
  Locale,
  MessageKey,
  messages,
  useI18n,
} from "./i18n";
import {
  Assignment,
  Bootstrap,
  Capabilities,
  DelegationState,
  ListResponse,
  Overview as OverviewData,
  Policy,
  PrincipalOption,
  RoleDefinition,
  ViewKey,
} from "./types";
import {
  roleSupportsNamespaces,
  validateAssignment,
} from "./policy-validation";
import {
  authorizedView,
  canReadOverview,
  hasCapability,
  validOAuthRedirectURI,
  validOIDCIssuer,
} from "./ui-validation";

type AuthState = {
  status: "loading" | "login" | "success" | "ready";
  capabilities?: Capabilities;
  error?: string;
};
const viewMeta: Record<
  ViewKey,
  { title: MessageKey; description: MessageKey; capability?: string | string[] }
> = {
  overview: { title: "overview", description: "overviewDesc" },
  roles: {
    title: "roles",
    description: "rolesDesc",
    capability: "platform.authorization.read",
  },
  permissions: {
    title: "permissionsPolicy",
    description: "permissionsPolicyDesc",
    capability: "platform.authorization.read",
  },
  assignments: {
    title: "assignments",
    description: "assignmentsDesc",
    capability: "platform.authorization.read",
  },
  delegations: {
    title: "delegations",
    description: "delegationsDesc",
    capability: "namespace.authorization.read",
  },
  providers: {
    title: "providers",
    description: "providersDesc",
    capability: "platform.identity.providers.read",
  },
  oauthClients: {
    title: "oauthClients",
    description: "oauthClientsDesc",
    capability: "platform.oauth-clients.read",
  },
  users: {
    title: "users",
    description: "usersDesc",
    capability: "platform.identity.users.read",
  },
  security: { title: "security", description: "securityDesc" },
  principals: {
    title: "principals",
    description: "principalsDesc",
    capability: "platform.identity.principals.read",
  },
  sessions: {
    title: "sessions",
    description: "sessionsDesc",
    capability: ["platform.sessions.read", "namespace.tasks.read"],
  },
  tasks: {
    title: "tasks",
    description: "tasksDesc",
    capability: ["platform.tasks.read", "namespace.tasks.read"],
  },
  relays: {
    title: "relays",
    description: "relaysDesc",
    capability: "platform.relays.read",
  },
  audit: {
    title: "audit",
    description: "auditDesc",
    capability: "platform.audit.read",
  },
};
function hashView(): ViewKey {
  const value = location.hash.replace(/^#\/?/, "").split(/[/?]/)[0];
  return value in viewMeta ? (value as ViewKey) : "overview";
}
function setHashView(view: ViewKey) {
  history.replaceState(
    {},
    "",
    `${location.pathname}${location.search}#/${view}`,
  );
  window.dispatchEvent(new HashChangeEvent("hashchange"));
}

export default function App() {
  const [locale, setLocaleState] = useState<Locale>(detectLocale);
  const [auth, setAuth] = useState<AuthState>({ status: "loading" });
  const [view, setView] = useState<ViewKey>(hashView);
  const [menuOpen, setMenuOpen] = useState(false);
  const setLocale = (next: Locale) => {
    localStorage.setItem(localeStorageKey, next);
    document.documentElement.lang = next;
    setLocaleState(next);
  };
  const t = useCallback((key: MessageKey) => messages[locale][key], [locale]);
  const bootstrap = useCallback(async () => {
    try {
      const completedLogin = await finishOIDCCallback();
      if (completedLogin) {
        setAuth({ status: "success" });
      }
      const result = await request<Bootstrap>(`${managementBase}/bootstrap`);
      if (completedLogin) {
        await new Promise<void>((resolve) => window.setTimeout(resolve, 1000));
      }
      setAuth({
        status: "ready",
        capabilities: {
          ...result.authorization,
          authenticationType: result.session.authenticationType,
        },
      });
    } catch (error) {
      const api = error as ApiError;
      setAuth({
        status: "login",
        error:
          api.status === 403
            ? t("forbidden")
            : api.status === 401
              ? ""
              : api.message,
      });
    }
  }, [t]);
  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);
  useEffect(() => {
    void bootstrap();
  }, [bootstrap]);
  useEffect(() => {
    const onAuthenticationLost = () => {
      sessionStorage.removeItem(csrfStorageKey);
      setAuth({ status: "login" });
    };
    addEventListener(authenticationLostEvent, onAuthenticationLost);
    return () =>
      removeEventListener(authenticationLostEvent, onAuthenticationLost);
  }, []);
  useEffect(() => {
    const onHash = () => setView(hashView());
    addEventListener("hashchange", onHash);
    return () => removeEventListener("hashchange", onHash);
  }, []);
  useEffect(() => {
    if (auth.status !== "ready" || !auth.capabilities) return;
    const next = authorizedView(
      auth.capabilities,
      view,
      viewMeta[view].capability,
    );
    if (next !== view) {
      setHashView(next);
      setMenuOpen(false);
    }
  }, [auth, view]);
  const context = useMemo(() => ({ locale, setLocale, t }), [locale, t]);
  return (
    <I18nContext.Provider value={context}>
      {auth.status === "loading" ? (
        <BootScreen />
      ) : auth.status === "login" ? (
        <Login error={auth.error} locale={locale} setLocale={setLocale} />
      ) : auth.status === "success" ? (
        <LoginSuccessScreen />
      ) : (
        <Shell
          capabilities={auth.capabilities!}
          view={view}
          onView={(next) => {
            setHashView(next);
            setMenuOpen(false);
          }}
          menuOpen={menuOpen}
          setMenuOpen={setMenuOpen}
          onLogout={async () => {
            await logout();
            setAuth({ status: "login" });
          }}
          locale={locale}
          setLocale={setLocale}
        />
      )}
    </I18nContext.Provider>
  );
}
function BootScreen() {
  return (
    <main className="boot">
      <div className="brand-mark">KL</div>
      <div className="boot-line">
        <span />
      </div>
    </main>
  );
}
function LoginSuccessScreen() {
  const { t } = useI18n();
  return (
    <main className="login-shell" role="status" aria-live="polite">
      <section className="login-card login-success-card">
        <span className="login-success-icon" aria-hidden="true">
          <CheckCircle2 size={28} />
        </span>
        <div className="login-heading">
          <h1>{t("loginSuccess")}</h1>
          <p>{t("loginSuccessHint")}</p>
        </div>
        <div className="login-success-line">
          <span />
        </div>
      </section>
    </main>
  );
}
function LocaleButton({
  locale,
  setLocale,
}: {
  locale: Locale;
  setLocale: (value: Locale) => void;
}) {
  const { t } = useI18n();
  return (
    <button
      className="locale-button"
      onClick={() => setLocale(locale === "zh-CN" ? "en-US" : "zh-CN")}
      aria-label={t("language")}
    >
      <Languages size={16} />
      {locale === "zh-CN" ? "EN" : "中"}
    </button>
  );
}
function Login({
  error,
  locale,
  setLocale,
}: {
  error?: string;
  locale: Locale;
  setLocale: (value: Locale) => void;
}) {
  const { t } = useI18n();
  const [methods, setMethods] = useState<
    { id: string; type: string; interaction: string; displayName?: string }[]
  >([]);
  const [busy, setBusy] = useState(false);
  const [loadError, setLoadError] = useState(error || "");
  useEffect(() => {
    request<{ authMethods?: typeof methods }>("/.well-known/kubeloop")
      .then((body) =>
        setMethods(
          (body.authMethods || []).filter(
            (method) =>
              ["oidc", "local"].includes(method.type) &&
              method.interaction === "browser",
          ),
        ),
      )
      .catch((cause) => setLoadError(cause.message));
  }, []);
  return (
    <main className="login-shell">
      <section className="login-card">
        <header className="login-top">
          <div className="brand">
            <span className="brand-mark">KL</span>
            <span>
              {t("brand")}
              <small>ADMIN CONSOLE</small>
            </span>
          </div>
          <LocaleButton locale={locale} setLocale={setLocale} />
        </header>
        <div className="login-heading">
          <h1>{t("signIn")}</h1>
          <p>{t("signInHint")}</p>
        </div>
        <div className="provider-list">
          {methods.map((method) => (
            <button
              key={method.id}
              disabled={busy}
              onClick={async () => {
                setBusy(true);
                setLoadError("");
                try {
                  await startOIDC(method.id);
                } catch (cause) {
                  sessionStorage.removeItem("kubeloop.admin.oidc");
                  setLoadError((cause as Error).message);
                  setBusy(false);
                }
              }}
            >
              <span className="provider-icon">
                <KeyRound size={18} />
              </span>
              <span>
                <strong>{method.displayName || method.id}</strong>
                <small>OIDC · PKCE</small>
              </span>
              <ChevronRight size={17} />
            </button>
          ))}
          {!methods.length && !loadError && (
            <div className="provider-empty">
              {busy ? t("loggingIn") : t("noProviders")}
            </div>
          )}
        </div>
        {loadError && <Notice>{loadError}</Notice>}
        <p className="login-foot">OIDC + PKCE · HttpOnly Session · CSRF</p>
      </section>
    </main>
  );
}

const icons: Record<ViewKey, typeof Activity> = {
  overview: LayoutDashboard,
  roles: ShieldCheck,
  permissions: BookOpenCheck,
  assignments: UsersRound,
  delegations: UsersRound,
  providers: Network,
  oauthClients: KeyRound,
  users: UsersRound,
  security: KeyRound,
  principals: CircleUserRound,
  sessions: Activity,
  tasks: Boxes,
  relays: ServerCog,
  audit: FileClock,
};
const navGroups: { label: MessageKey; items: ViewKey[] }[] = [
  { label: "navOverview", items: ["overview"] },
  {
    label: "navIdentity",
    items: ["users", "principals", "providers", "oauthClients", "security"],
  },
  {
    label: "navGovernance",
    items: ["roles", "permissions", "assignments", "delegations"],
  },
  { label: "navRuntime", items: ["sessions", "tasks", "relays"] },
  { label: "navCompliance", items: ["audit"] },
];
function Shell({
  capabilities,
  view,
  onView,
  menuOpen,
  setMenuOpen,
  onLogout,
  locale,
  setLocale,
}: {
  capabilities: Capabilities;
  view: ViewKey;
  onView: (view: ViewKey) => void;
  menuOpen: boolean;
  setMenuOpen: (value: boolean) => void;
  onLogout: () => Promise<void>;
  locale: Locale;
  setLocale: (value: Locale) => void;
}) {
  const { t } = useI18n();
  useEffect(() => {
    if (!menuOpen) return;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMenuOpen(false);
    };
    addEventListener("keydown", closeOnEscape);
    return () => {
      document.body.style.overflow = previousOverflow;
      removeEventListener("keydown", closeOnEscape);
    };
  }, [menuOpen, setMenuOpen]);
  const allowed = (capability?: string | string[]) =>
    hasCapability(capabilities, capability);
  return (
    <div className="app-shell">
      <aside className={`sidebar ${menuOpen ? "open" : ""}`}>
        <div className="sidebar-brand">
          <div className="brand">
            <span className="brand-mark">KL</span>
            <span>
              {t("brand")}
              <small>ADMIN CONSOLE</small>
            </span>
          </div>
          <button
            className="icon-button mobile-only"
            onClick={() => setMenuOpen(false)}
            aria-label={t("close")}
          >
            <X size={19} />
          </button>
        </div>
        <nav>
          {navGroups.map((group) => {
            const items = group.items.filter((item) =>
              allowed(viewMeta[item].capability),
            );
            return items.length ? (
              <div className="nav-group" key={group.label}>
                <span>{t(group.label)}</span>
                {items.map((item) => {
                  const Icon = icons[item];
                  return (
                    <button
                      key={item}
                      className={view === item ? "active" : ""}
                      onClick={() => onView(item)}
                    >
                      <Icon size={17} />
                      {t(viewMeta[item].title)}
                      {view === item && <i />}
                    </button>
                  );
                })}
              </div>
            ) : null;
          })}
        </nav>
        <div className="sidebar-footer">
          <button onClick={() => void onLogout()}>
            <LogOut size={16} />
            {t("logout")}
          </button>
        </div>
      </aside>
      {menuOpen && (
        <button
          className="menu-scrim"
          onClick={() => setMenuOpen(false)}
          aria-label={t("close")}
        />
      )}
      <div className="workspace">
        <header className="topbar">
          <button
            className="icon-button menu-button"
            onClick={() => setMenuOpen(true)}
            aria-label={t("openMenu")}
          >
            <Menu size={20} />
          </button>
          <div className="breadcrumb">
            <span>KubeLoop</span>
            <ChevronRight size={14} />
            <strong>{t(viewMeta[view].title)}</strong>
          </div>
          <div className="top-actions">
            <span className="auth-badge">
              <span />
              {capabilities.authenticationType || "authenticated"}
            </span>
            <LocaleButton locale={locale} setLocale={setLocale} />
          </div>
        </header>
        <main className="page">
          <PageRouter
            view={view}
            capabilities={capabilities}
            allowed={allowed}
          />
        </main>
      </div>
    </div>
  );
}

function PageRouter({
  view,
  capabilities,
  allowed,
}: {
  view: ViewKey;
  capabilities: Capabilities;
  allowed: (capability?: string) => boolean;
}) {
  if (view === "overview") return <Overview capabilities={capabilities} />;
  if (view === "roles") return <PolicyPage allowed={allowed} mode="roles" />;
  if (view === "permissions")
    return <PermissionPolicyPage allowed={allowed} />;
  if (view === "assignments")
    return <PolicyPage allowed={allowed} mode="assignments" />;
  if (view === "delegations")
    return <DelegationsPage capabilities={capabilities} />;
  if (view === "providers") return <ProvidersPage allowed={allowed} />;
  if (view === "oauthClients") return <OAuthClientsPage allowed={allowed} />;
  if (view === "users") return <UsersPage allowed={allowed} />;
  if (view === "security") return <SecurityPage />;
  return (
    <ListPage
      key={view}
      view={view}
      capabilities={capabilities}
      allowed={allowed}
    />
  );
}

function useAsync<T>(loader: () => Promise<T>, deps: unknown[]) {
  const [state, setState] = useState<{
    loading: boolean;
    data?: T;
    error?: string;
  }>({ loading: true });
  const load = useCallback(async () => {
    setState((current) => ({ ...current, loading: true, error: undefined }));
    try {
      setState({ loading: false, data: await loader() });
    } catch (error) {
      setState({ loading: false, error: (error as Error).message });
    }
  }, deps); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    void load();
  }, [load]);
  return { ...state, reload: load };
}

async function loadPrincipalOptions(): Promise<PrincipalOption[]> {
  const principals: PrincipalOption[] = [];
  let cursor = "";
  do {
    const query = new URLSearchParams({ limit: "100" });
    if (cursor) query.set("cursor", cursor);
    const page = await request<{
      items?: PrincipalOption[];
      nextCursor?: string;
    }>(`/principals?${query}`);
    principals.push(...(page.items || []));
    cursor = page.nextCursor || "";
  } while (cursor);
  return principals;
}

function Overview({ capabilities }: { capabilities: Capabilities }) {
  const { t } = useI18n();
  const canReadStatus = canReadOverview(capabilities);
  const status = useAsync<OverviewData | undefined>(
    () =>
      canReadStatus
        ? request<OverviewData>("/overview")
        : Promise.resolve(undefined),
    [canReadStatus],
  );
  return (
    <>
      <PageHeader
        title={t("overview")}
        description={t("overviewDesc")}
        actions={canReadStatus ? (
          <Button onClick={() => void status.reload()}>
            <RefreshCw size={15} />
            {t("refresh")}
          </Button>
        ) : undefined}
      />
      <div className="metrics">
        <Metric
          label={t("authType")}
          value={capabilities.authenticationType || "—"}
        />
        <Metric
          label={t("capabilities")}
          value={capabilities.capabilities.length}
        />
        <Metric
          label={t("delegations")}
          value={capabilities.namespaceScopes.length}
        />
      </div>
      <section className="panel">
        <div className="section-title">
          <div>
            <span className="section-icon">
              <Activity size={18} />
            </span>
            <div>
              <h2>{t("systemStatus")}</h2>
              <p>Control Plane · Storage · Runtime</p>
            </div>
          </div>
          {status.data && (
            <span className="healthy">
              <span />
              {t("active")}
            </span>
          )}
        </div>
        {!canReadStatus ? (
          <Notice>{t("overviewRestricted")}</Notice>
        ) : status.loading ? (
          <Loading />
        ) : status.error ? (
          <Notice>{status.error}</Notice>
        ) : (
          <div className="status-grid">
            <StatusRow
              label="Control Plane"
              value={`${status.data?.system.controlPlane.version || "—"} · ${status.data?.system.controlPlane.commit || "unknown"}`}
            />
            <StatusRow
              label="Storage"
              value={`${status.data?.system.storage.backend || "—"} · schema ${status.data?.system.storage.schemaVersion || "—"}`}
            />
            <StatusRow
              label="Sessions / Tasks"
              value={`${status.data?.runtime.activeSessions.count || 0} / ${status.data?.runtime.activeTasks.count || 0}`}
            />
            <StatusRow
              label="Relays"
              value={`${status.data?.runtime.relays.online || 0}/${status.data?.runtime.relays.total || 0} online · ${status.data?.runtime.relays.draining || 0} draining`}
            />
          </div>
        )}
      </section>
      <section className="scope-panel">
        <div>
          <BookOpenCheck size={20} />
          <div>
            <h3>{t("delegations")}</h3>
            <p>
              {capabilities.namespaceScopes.length
                ? capabilities.namespaceScopes
                    .map((scope) => scope.namespace)
                    .join(" · ")
                : t("empty")}
            </p>
          </div>
        </div>
      </section>
    </>
  );
}
function StatusRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="status-row">
      <span>
        <i />
        {label}
      </span>
      <strong>{value}</strong>
    </div>
  );
}

function DelegationsPage({ capabilities }: { capabilities: Capabilities }) {
  const { t } = useI18n();
  const namespaces = capabilities.namespaceScopes
    .filter((scope) =>
      scope.capabilities?.includes("namespace.authorization.read"),
    )
    .map((scope) => scope.namespace);
  const [namespace, setNamespace] = useState(namespaces[0] || "");
  const state = useAsync(
    () =>
      namespace
        ? request<DelegationState>(
            `/authorization/delegations?namespace=${encodeURIComponent(namespace)}`,
          )
        : Promise.resolve(undefined as unknown as DelegationState),
    [namespace],
  );
  const principals = useAsync(
    () =>
      namespace
        ? request<{ items: PrincipalOption[] }>(
            `/authorization/delegations/principals?namespace=${encodeURIComponent(namespace)}`,
          ).then((page) => page.items || [])
        : Promise.resolve([]),
    [namespace],
  );
  const canDelegate = capabilities.namespaceScopes.some(
    (scope) =>
      scope.namespace === namespace &&
      scope.capabilities?.includes("namespace.authorization.delegate"),
  );
  const [open, setOpen] = useState(false);
  const [subjectType, setSubjectType] = useState<"group" | "principal">(
    "group",
  );
  const [principalId, setPrincipalId] = useState("");
  const [providerId, setProviderId] = useState("");
  const [groupName, setGroupName] = useState("");
  const [roleId, setRoleId] = useState("");
  const [reason, setReason] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const save = async () => {
    if (!state.data) return;
    setBusy(true);
    setMessage("");
    try {
      await mutation(
        `/authorization/delegations/${crypto.randomUUID()}`,
        "PUT",
        {
          namespace,
          subject:
            subjectType === "principal"
              ? { type: "principal", principalId }
              : { type: "group", providerId, groupName },
          roleId,
          reason,
        },
        { idempotent: true },
      );
      setOpen(false);
      setReason("");
      await state.reload();
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const remove = async (binding: Assignment) => {
    if (!state.data) return;
    const deletionReason = prompt(t("delegationReason"));
    if (!deletionReason) return;
    setBusy(true);
    setMessage("");
    try {
      await mutation(
        `/authorization/delegations/${encodeURIComponent(binding.id)}`,
        "DELETE",
        { namespace, reason: deletionReason },
        { idempotent: true },
      );
      await state.reload();
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <PageHeader
        title={t("delegations")}
        description={t("delegationsDesc")}
        actions={
          <>
            <label className="delegation-namespace">
              {t("namespace")}
              <select
                value={namespace}
                onChange={(event) => setNamespace(event.target.value)}
              >
                {namespaces.map((value) => (
                  <option key={value}>{value}</option>
                ))}
              </select>
            </label>
            <Button
              disabled={!canDelegate || !state.data}
              onClick={() => {
                setRoleId(state.data?.roles[0]?.id || "");
                setOpen(true);
              }}
            >
              {t("addDelegation")}
            </Button>
          </>
        }
      />
      {message && <Notice>{message}</Notice>}
      {!namespace ? (
        <Empty>{t("empty")}</Empty>
      ) : state.loading ? (
        <Loading />
      ) : state.error ? (
        <Notice>{state.error}</Notice>
      ) : !state.data?.bindings.length ? (
        <section className="panel"><Empty>{t("empty")}</Empty></section>
      ) : (
        <section className="table-panel">
          <table>
            <thead>
              <tr>
                <th>{t("delegationSubject")}</th>
                <th>{t("providerId")}</th>
                <th>{t("delegationRole")}</th>
                <th>{t("namespace")}</th>
                <th>{t("actions")}</th>
              </tr>
            </thead>
            <tbody>
              {state.data.bindings.map((binding) => (
                <tr key={binding.id}>
                  <td className="strong-cell">
                    {binding.subject.type === "principal"
                      ? principals.data?.find(
                          (item) => item.id === binding.subject.principalId,
                        )?.displayName || binding.subject.principalId
                      : binding.subject.groupName}
                  </td>
                  <td>{binding.subject.providerId || "—"}</td>
                  <td>{binding.roleId}</td>
                  <td>{namespace}</td>
                  <td>
                    <Button
                      disabled={!canDelegate || busy}
                      onClick={() => void remove(binding)}
                    >
                      {t("remove")}
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("addDelegation")}</DialogTitle>
            <DialogDescription>{t("delegationsDesc")}</DialogDescription>
          </DialogHeader>
          <div className="form-grid">
            <label>
              {t("subjectType")}
              <select
                value={subjectType}
                onChange={(event) =>
                  setSubjectType(event.target.value as "group" | "principal")
                }
              >
                <option value="group">{t("group")}</option>
                <option value="principal">{t("principal")}</option>
              </select>
            </label>
            {subjectType === "principal" ? (
              <label>
                {t("selectUser")}
                <select
                  value={principalId}
                  onChange={(event) => setPrincipalId(event.target.value)}
                >
                  <option value="">—</option>
                  {(principals.data || []).map((principal) => (
                    <option key={principal.id} value={principal.id}>
                      {principal.displayName || principal.email || principal.id}
                    </option>
                  ))}
                </select>
              </label>
            ) : (
              <>
                <label>
                  {t("providerId")}
                  <input
                    value={providerId}
                    onChange={(event) => setProviderId(event.target.value)}
                  />
                </label>
                <label>
                  {t("group")}
                  <input
                    value={groupName}
                    onChange={(event) => setGroupName(event.target.value)}
                  />
                </label>
              </>
            )}
            <label>
              {t("delegationRole")}
              <select
                value={roleId}
                onChange={(event) => setRoleId(event.target.value)}
              >
                {(state.data?.roles || []).map((role) => (
                  <option key={role.id} value={role.id}>{role.displayName}</option>
                ))}
              </select>
            </label>
            <label className="full">
              {t("delegationReason")}
              <input
                value={reason}
                onChange={(event) => setReason(event.target.value)}
              />
            </label>
          </div>
          {message && <Notice>{message}</Notice>}
          <DialogFooter>
            <Button onClick={() => setOpen(false)}>{t("cancel")}</Button>
            <Button
              disabled={
                busy ||
                reason.trim().length < 8 ||
                !roleId ||
                (subjectType === "principal"
                  ? !principalId
                  : !providerId.trim() || !groupName.trim())
              }
              onClick={() => void save()}
            >
              {t("create")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function PermissionPolicyPage({
  allowed,
}: {
  allowed: (capability?: string) => boolean;
}) {
  const { t } = useI18n();
  const current = useAsync(() => request<Policy>("/authorization"), []);
  const principals = useAsync(
    () =>
      allowed("platform.identity.principals.read")
        ? loadPrincipalOptions()
        : Promise.resolve([] as PrincipalOption[]),
    [],
  );
  return (
    <>
      <PageHeader
        title={t("permissionsPolicy")}
        description={t("permissionsPolicyDesc")}
        actions={
          <Button onClick={() => void current.reload()}>
            <RefreshCw size={15} />
            {t("refresh")}
          </Button>
        }
      />
      {current.loading ? (
        <Loading />
      ) : current.error ? (
        <Notice>{current.error}</Notice>
      ) : (
        <>
          <div className="metrics compact">
            <Metric
              label={t("current")}
              value={current.data?.active ? t("active") : "Bootstrap"}
            />
          </div>
          <PermissionRelations
            policy={current.data}
            principals={principals.data || []}
          />
        </>
      )}
    </>
  );
}

function PolicyPage({
  allowed,
  mode,
}: {
  allowed: (capability?: string) => boolean;
  mode: "roles" | "assignments";
}) {
  const { t } = useI18n();
  const current = useAsync(() => request<Policy>("/authorization"), []);
  const principals = useAsync(
    () =>
      mode === "assignments" && allowed("platform.identity.principals.read")
        ? loadPrincipalOptions()
        : Promise.resolve([] as PrincipalOption[]),
    [],
  );
  const [roles, setRoles] = useState<RoleDefinition[]>([]);
  const [assignments, setAssignments] = useState<Assignment[]>([]);
  const [reason, setReason] = useState("");
  const [message, setMessage] = useState<{
    text: string;
    tone: "error" | "success" | "warning";
  }>();
  const [busy, setBusy] = useState("");
  const [pending, setPending] = useState<{
    changeId: string;
    idempotencyKey: string;
  }>();
  const [roleOpen, setRoleOpen] = useState(false);
  const pageTitle = mode === "roles" ? "roles" : "assignments";
  const pageDescription = mode === "roles" ? "rolesDesc" : "assignmentsDesc";
  useEffect(() => {
    if (current.data) {
      setRoles(current.data.spec?.roles || []);
      setAssignments(current.data.spec?.bindings || []);
    }
  }, [current.data]);
  const spec = { version: current.data?.spec.version || 2, roles, bindings: assignments };
  const policyValidationError = assignments
    .map((assignment) => validateAssignment(assignment, roles))
    .find(Boolean);
  const change = (index: number, patch: Partial<Assignment>) =>
    setAssignments((items) =>
      items.map((item, at) => (at === index ? { ...item, ...patch } : item)),
    );
  const add = () =>
    setAssignments((items) => [
      ...items,
      {
        id: crypto.randomUUID(),
        roleId: "auditor",
        subject: { type: "group", providerId: "", groupName: "" },
        scope: { type: "platform", names: [], labelSelectors: [] },
        managedBy: "platform",
      },
    ]);
  const invoke = async (kind: "validate" | "draft" | "publish") => {
    if (!current.data) return;
    setBusy(kind);
    setMessage(undefined);
    try {
      if (kind === "publish" && pending) {
        await mutation(
          `/authorization/changes/${encodeURIComponent(pending.changeId)}/publish`,
          "POST",
          { reason: reason || "publish validated policy" },
          { idempotencyKey: pending.idempotencyKey },
        );
        setMessage({
          text: "Policy published.",
          tone: "success",
        });
        setPending(undefined);
        await current.reload();
      } else if (kind === "validate") {
        const result = await mutation<{ publishable: boolean }>(
          "/authorization/dry-run",
          "POST",
          { spec, checks: [], reason: reason || "validate candidate policy" },
          {},
        );
        setMessage({
          text: result.publishable
            ? "Policy is valid and publishable."
            : "Policy is valid but requires a platform-admin assignment.",
          tone: result.publishable ? "success" : "warning",
        });
      } else {
        const idempotencyKey = crypto.randomUUID();
        const result = await mutation<{
          changeId: string;
          objectId: string;
        }>(
          "/authorization/drafts",
          "POST",
          { spec, reason },
          { idempotencyKey },
        );
        setPending({ changeId: result.changeId, idempotencyKey });
        setMessage({
          text: "Draft is ready to publish.",
          tone: "success",
        });
      }
    } catch (error) {
      setMessage({ text: (error as Error).message, tone: "error" });
    } finally {
      setBusy("");
    }
  };
  return (
    <>
      <PageHeader
        title={t(pageTitle)}
        description={t(pageDescription)}
        actions={
          <Button onClick={() => void current.reload()}>
            <RefreshCw size={15} />
            {t("refresh")}
          </Button>
        }
      />
      {current.loading ? (
        <Loading />
      ) : current.error ? (
        <Notice>{current.error}</Notice>
      ) : (
        <>
          <div className="metrics compact">
            <Metric
              label={t("current")}
              value={current.data?.active ? t("active") : "Bootstrap"}
            />
          </div>
          <section className="panel assignment-panel">
            <div className="section-title">
              <div>
                <span className="section-icon">
                  {mode === "roles" ? (
                    <ShieldCheck size={18} />
                  ) : (
                    <UsersRound size={18} />
                  )}
                </span>
                <div>
                  <h2>{t(mode === "roles" ? "roles" : "assignmentList")}</h2>
                  <p>{t(mode === "roles" ? "rolesDesc" : "groupFirst")}</p>
                </div>
              </div>
              {allowed("platform.authorization.manage") && (
                <div className="footer-buttons">
                  {mode === "roles" ? (
                    <Button onClick={() => setRoleOpen(true)}>
                      {t("createRole")}
                    </Button>
                  ) : (
                    <Button onClick={add}>
                      <UsersRound size={15} />
                      {t("addAssignment")}
                    </Button>
                  )}
                </div>
              )}
            </div>
            {message && <Notice tone={message.tone}>{message.text}</Notice>}
            {policyValidationError && (
              <Notice tone="error">{policyValidationError}</Notice>
            )}
            {mode === "roles" && (
              <div className="custom-role-list">
                <strong>{t("builtInRoles")}</strong>
                {(current.data?.builtInRoles || []).map((role) => (
                  <div className="custom-role" key={role.id}>
                    <div>
                      <b>{role.displayName}</b>
                      <span>
                        {role.id} · {roleCapabilities(role).length} {t("permissions")}
                      </span>
                    </div>
                  </div>
                ))}
              </div>
            )}
            {mode === "roles" && roles.length > 0 && (
              <div className="custom-role-list">
                <strong>{t("customRoles")}</strong>
                {roles.map((role) => (
                  <div className="custom-role" key={role.id}>
                    <div>
                      <b>{role.displayName}</b>
                      <span>
                        {role.id} · {roleCapabilities(role).length} {t("permissions")}
                      </span>
                    </div>
                    {allowed("platform.authorization.manage") && (
                      <Button
                        kind="ghost"
                        onClick={() => {
                          setRoles((items) =>
                            items.filter((item) => item.id !== role.id),
                          );
                          setAssignments((items) =>
                            items.filter((item) => item.roleId !== role.id),
                          );
                        }}
                      >
                        {t("remove")}
                      </Button>
                    )}
                  </div>
                ))}
              </div>
            )}
            {mode === "assignments" && (
              <div className="assignment-list">
                {assignments.map((assignment, index) => (
                  <AssignmentRow
                    key={assignment.id}
                    assignment={assignment}
                    roles={roles}
                    principals={principals.data || []}
                    principalsLoading={principals.loading}
                    principalsError={principals.error}
                    onChange={(patch) => change(index, patch)}
                    onRemove={() =>
                      setAssignments((items) =>
                        items.filter((_, at) => at !== index),
                      )
                    }
                    readOnly={!allowed("platform.authorization.manage")}
                  />
                ))}
                {!assignments.length && <Empty>{t("empty")}</Empty>}
              </div>
            )}
            {allowed("platform.authorization.manage") && (
              <div className="policy-footer">
                <label className="grow">
                  {t("reason")}
                  <input
                    value={reason}
                    onChange={(event) => setReason(event.target.value)}
                    minLength={8}
                    maxLength={512}
                    placeholder={t("operationReason")}
                  />
                </label>
                <div className="footer-buttons">
                  {allowed("platform.authorization.simulate") && (
                    <Button
                      busy={busy === "validate"}
                      disabled={!!policyValidationError}
                      onClick={() => void invoke("validate")}
                    >
                      {t("validate")}
                    </Button>
                  )}
                  <Button
                    kind="primary"
                    busy={busy === "draft"}
                    disabled={
                      reason.trim().length < 8 || !!policyValidationError
                    }
                    onClick={() => void invoke("draft")}
                  >
                    {t("saveDraft")}
                  </Button>
                  {pending && allowed("platform.authorization.publish") && (
                    <Button
                      kind="primary"
                      busy={busy === "publish"}
                      onClick={() => void invoke("publish")}
                    >
                      {t("publish")}
                      <ChevronRight size={15} />
                    </Button>
                  )}
                </div>
              </div>
            )}
          </section>
        </>
      )}
      {mode === "roles" && (
        <RoleDialog
          open={roleOpen}
          permissions={current.data?.availableCapabilities || []}
          reservedIds={roles.map((role) => role.id)}
          onClose={() => setRoleOpen(false)}
          onConfirm={(role) => {
            setRoles((items) => [...items, role]);
            setRoleOpen(false);
          }}
        />
      )}
    </>
  );
}

function PermissionRelations({
  policy,
  principals,
}: {
  policy?: Policy;
  principals: PrincipalOption[];
}) {
  const { t } = useI18n();
  if (!policy) return null;
  const roles = new Map(
    [...(policy.builtInRoles || []), ...(policy.spec.roles || [])].map(
      (role) => [role.id, role],
    ),
  );
  const principalNames = new Map(
    principals.map((principal) => [
      principal.id,
      principal.displayName || principal.email || principal.provider,
    ]),
  );
  return (
    <section className="panel permission-relations">
      <div className="section-title">
        <div>
          <span className="section-icon">
            <UsersRound size={18} />
          </span>
          <div>
            <h2>{t("currentRelations")}</h2>
            <p>{t("currentRelationsDesc")}</p>
          </div>
        </div>
      </div>
      {!policy.active || !policy.spec.bindings.length ? (
        <Empty>{t("empty")}</Empty>
      ) : (
        <div className="relation-graph" role="list" aria-label={t("currentRelations")}>
          <div className="relation-head" aria-hidden="true">
            <span>{t("subjectType")}</span>
            <span>Binding</span>
            <span>{t("role")}</span>
            <span>Scope</span>
            <span>{t("permissions")}</span>
          </div>
          {policy.spec.bindings.map((assignment) => {
            const role = roles.get(assignment.roleId);
            const subject = assignment.subject.type === "group"
              ? `${t("group")}: ${assignment.subject.providerId}/${assignment.subject.groupName}`
              : `${t("principal")}: ${principalNames.get(assignment.subject.principalId || "") || t("unknownPrincipal")}`;
            const selectors = (assignment.scope.labelSelectors || []).map(formatNamespaceSelector);
            return (
              <article className="relation-path" role="listitem" key={assignment.id}>
                <div className="relation-node relation-subject">
                  <small>{assignment.subject.type}</small>
                  <strong>{subject}</strong>
                </div>
                <span className="relation-edge" aria-hidden="true">→</span>
                <div className="relation-node relation-binding">
                  <small>{assignment.managedBy}</small>
                  <strong>{assignment.id}</strong>
                </div>
                <span className="relation-edge" aria-hidden="true">→</span>
                <div className="relation-node relation-role">
                  <small>{role?.builtIn ? "built-in" : "custom"}</small>
                  <strong>{role?.displayName || assignment.roleId}</strong>
                </div>
                <span className="relation-edge" aria-hidden="true">→</span>
                <div className="relation-node relation-scope">
                  <small>{assignment.scope.type}</small>
                  <strong>{assignment.scope.type === "platform" ? t("clusterWide") : (assignment.scope.names || []).join(", ") || "LabelSelector"}</strong>
                  {selectors.map((selector) => <code key={selector}>{selector}</code>)}
                </div>
                <span className="relation-edge" aria-hidden="true">→</span>
                <details className="relation-node relation-capabilities">
                  <summary>{t("permissionDetails")} ({role ? roleCapabilities(role).length : 0})</summary>
                  <div className="permission-chip-list">
                    {(role?.statements || []).flatMap((statement, statementIndex) => statement.capabilities.map((capability) => (
                      <code className={statement.effect === "deny" ? "deny" : "allow"} key={`${statementIndex}-${capability}`}>
                        {statement.effect.toUpperCase()} · {capability}
                      </code>
                    )))}
                  </div>
                </details>
              </article>
            );
          })}
        </div>
      )}
    </section>
  );
}

function formatNamespaceSelector(selector: NonNullable<Assignment["scope"]["labelSelectors"]>[number]): string {
  const labels = Object.entries(selector.matchLabels || {}).map(([key, value]) => `${key}=${value}`);
  const expressions = (selector.matchExpressions || []).map((expression) =>
    `${expression.key} ${expression.operator}${expression.values?.length ? ` (${expression.values.join(",")})` : ""}`,
  );
  return [...labels, ...expressions].join(" && ");
}

function roleCapabilities(role: RoleDefinition): string[] {
  return Array.from(
    new Set(role.statements.flatMap((statement) => statement.capabilities)),
  );
}

function AssignmentRow({
  assignment,
  roles,
  principals,
  principalsLoading,
  principalsError,
  onChange,
  onRemove,
  readOnly,
}: {
  assignment: Assignment;
  roles: RoleDefinition[];
  principals: PrincipalOption[];
  principalsLoading: boolean;
  principalsError?: string;
  onChange: (patch: Partial<Assignment>) => void;
  onRemove: () => void;
  readOnly: boolean;
}) {
  const { t } = useI18n();
  const [principalSearch, setPrincipalSearch] = useState("");
  const groupMode = assignment.subject.type === "group";
  const value = groupMode
    ? assignment.subject.groupName || ""
    : assignment.subject.principalId || "";
  const supportsNamespaces = roleSupportsNamespaces(assignment.roleId, roles);
  const normalizedSearch = principalSearch.trim().toLowerCase();
  const visiblePrincipals = principals.filter((principal) => {
    if (principal.id === value) return true;
    if (!normalizedSearch) return true;
    return [principal.displayName, principal.email, principal.provider]
      .filter(Boolean)
      .some((item) => item!.toLowerCase().includes(normalizedSearch));
  });
  const selectedPrincipal = principals.find(
    (principal) => principal.id === value,
  );
  return (
    <div className="assignment-row">
      <div className="assignment-number">
        {assignment.roleId === "namespace-admin"
          ? "NS"
          : assignment.roleId.slice(0, 2).toUpperCase()}
      </div>
      <label>
        {t("subjectType")}
        <select
          disabled={readOnly}
          value={groupMode ? "group" : "principal"}
          onChange={(event) =>
            onChange(
              event.target.value === "group"
                ? { subject: { type: "group", providerId: "", groupName: "" } }
                : { subject: { type: "principal", principalId: "" } },
            )
          }
        >
          <option value="group">{t("group")}</option>
          <option value="principal">{t("principal")}</option>
        </select>
      </label>
      <label className="grow">
        {t("subjectValue")}
        {groupMode ? (
          <div className="principal-picker">
            <input
              disabled={readOnly}
              value={assignment.subject.providerId || ""}
              placeholder="auth0"
              aria-label="Provider ID"
              onChange={(event) => onChange({ subject: { ...assignment.subject, type: "group", providerId: event.target.value } })}
            />
            <input
              disabled={readOnly}
              value={value}
              placeholder="platform-operators"
              onChange={(event) => onChange({ subject: { ...assignment.subject, type: "group", groupName: event.target.value } })}
            />
          </div>
        ) : (
          <div className="principal-picker">
            <input
              type="search"
              disabled={readOnly || principalsLoading}
              value={principalSearch}
              placeholder={t("searchUser")}
              onChange={(event) => setPrincipalSearch(event.target.value)}
            />
            <select
              disabled={readOnly || principalsLoading || !!principalsError}
              value={value}
              onChange={(event) => onChange({ subject: { type: "principal", principalId: event.target.value } })}
            >
              <option value="">
                {principalsLoading
                  ? t("loading")
                  : principalsError
                    ? t("unavailable")
                    : t("selectUser")}
              </option>
              {value && !selectedPrincipal && (
                <option value={value}>{t("unknownPrincipal")}</option>
              )}
              {visiblePrincipals.map((principal) => (
                <option key={principal.id} value={principal.id}>
                  {principal.displayName ||
                    principal.email ||
                    principal.provider}
                  {principal.email && principal.displayName
                    ? ` · ${principal.email}`
                    : ""}
                  {` · ${principal.provider}`}
                </option>
              ))}
            </select>
          </div>
        )}
      </label>
      <label>
        {t("role")}
        <select
          disabled={readOnly}
          value={assignment.roleId}
          onChange={(event) => {
            const role = event.target.value;
            onChange({
              roleId: role,
              scope: roleSupportsNamespaces(role, roles)
                ? { ...assignment.scope, type: "namespaces", names: assignment.scope.names || [] }
                : { type: "platform", names: [], labelSelectors: [] },
            });
          }}
        >
          <option value="platform-admin">Platform Admin</option>
          <option value="security-admin">Security Admin</option>
          <option value="operator">Operator</option>
          <option value="auditor">Auditor</option>
          <option value="namespace-admin">Namespace Admin</option>
          {roles.map((role) => (
            <option key={role.id} value={role.id}>
              {role.displayName}
            </option>
          ))}
        </select>
      </label>
      {supportsNamespaces && (
        <>
          <label className="grow">
            {t("namespace")}
            <input
              disabled={readOnly}
              value={(assignment.scope.names || []).join(", ")}
              placeholder="team-a, team-b"
              onChange={(event) =>
                onChange({
                  scope: { ...assignment.scope, type: "namespaces", names: event.target.value
                    .split(",")
                    .map((item) => item.trim())
                    .filter(Boolean) },
                })
              }
            />
          </label>
          <label className="grow">
            Namespace labels
            <input
              disabled={readOnly}
              value={Object.entries(assignment.scope.labelSelectors?.[0]?.matchLabels || {})
                .map(([key, label]) => `${key}=${label}`)
                .join(", ")}
              placeholder="team=payments, environment=production"
              onChange={(event) => {
                const matchLabels = Object.fromEntries(
                  event.target.value.split(",").map((item) => item.trim()).filter(Boolean).map((item) => {
                    const separator = item.indexOf("=");
                    return separator > 0
                      ? [item.slice(0, separator).trim(), item.slice(separator + 1).trim()]
                      : [item, ""];
                  }).filter(([key, label]) => key && label),
                );
                onChange({
                  scope: {
                    ...assignment.scope,
                    type: "namespaces",
                    labelSelectors: Object.keys(matchLabels).length ? [{ matchLabels }] : [],
                  },
                });
              }}
            />
          </label>
        </>
      )}
      {!readOnly && (
        <Button kind="ghost" onClick={onRemove}>
          {t("remove")}
        </Button>
      )}
    </div>
  );
}

function RoleDialog({
  open,
  permissions,
  reservedIds,
  onClose,
  onConfirm,
}: {
  open: boolean;
  permissions: string[];
  reservedIds: string[];
  onClose: () => void;
  onConfirm: (role: RoleDefinition) => void;
}) {
  const { t } = useI18n();
  const [form, setForm] = useState({
    id: "",
    displayName: "",
    description: "",
    permissions: [] as string[],
    deniedPermissions: [] as string[],
  });
  const [error, setError] = useState("");
  useEffect(() => {
    if (open) {
      setForm({ id: "", displayName: "", description: "", permissions: [], deniedPermissions: [] });
      setError("");
    }
  }, [open]);
  const valid =
    /^[a-z][a-z0-9-]{2,63}$/.test(form.id) &&
    form.displayName.trim().length > 0 &&
    form.permissions.length + form.deniedPermissions.length > 0;
  const submit = () => {
    if (
      reservedIds.includes(form.id) ||
      [
        "platform-admin",
        "security-admin",
        "operator",
        "auditor",
        "namespace-admin",
      ].includes(form.id)
    ) {
      setError(`${t("roleId")}: ${form.id}`);
      return;
    }
    onConfirm({
      id: form.id,
      displayName: form.displayName.trim(),
      description: form.description.trim(),
      statements: [
        ...(form.permissions.length ? [{ effect: "allow" as const, capabilities: form.permissions }] : []),
        ...(form.deniedPermissions.length ? [{ effect: "deny" as const, capabilities: form.deniedPermissions }] : []),
      ],
    });
  };
  const grouped = Object.entries(
    permissions.reduce<Record<string, string[]>>((result, permission) => {
      const resource = permission.split(".").slice(0, 2).join(".");
      (result[resource] ||= []).push(permission);
      return result;
    }, {}),
  );
  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="role-dialog" closeLabel={t("close")}>
        <DialogHeader>
          <DialogTitle>{t("createRole")}</DialogTitle>
          <DialogDescription>{t("selectPermissions")}</DialogDescription>
        </DialogHeader>
        {error && <Notice>{error}</Notice>}
        <div className="form-grid">
          <label>
            {t("roleId")}
            <input
              autoFocus
              value={form.id}
              onChange={(event) =>
                setForm({
                  ...form,
                  id: event.target.value.trim().toLowerCase(),
                })
              }
              placeholder="support-reader"
              maxLength={64}
            />
          </label>
          <label>
            {t("roleName")}
            <input
              value={form.displayName}
              onChange={(event) =>
                setForm({ ...form, displayName: event.target.value })
              }
              maxLength={128}
            />
          </label>
          <label className="full">
            {t("roleDescription")}
            <textarea
              value={form.description}
              onChange={(event) =>
                setForm({ ...form, description: event.target.value })
              }
              maxLength={512}
            />
          </label>
        </div>
        <div className="permission-groups">
          {grouped.map(([resource, items]) => (
            <fieldset key={resource}>
              <legend>{resource.replace("admin.", "")}</legend>
              {(items || []).map((permission) => (
                <label className="permission-option" key={permission}>
                  <span>{permission}</span>
                  <select
                    value={form.deniedPermissions.includes(permission) ? "deny" : form.permissions.includes(permission) ? "allow" : ""}
                    onChange={(event) => setForm((current) => ({
                      ...current,
                      permissions: event.target.value === "allow"
                        ? [...current.permissions.filter((item) => item !== permission), permission]
                        : current.permissions.filter((item) => item !== permission),
                      deniedPermissions: event.target.value === "deny"
                        ? [...current.deniedPermissions.filter((item) => item !== permission), permission]
                        : current.deniedPermissions.filter((item) => item !== permission),
                    }))}
                  >
                    <option value="">{t("unset")}</option>
                    <option value="allow">{t("allow")}</option>
                    <option value="deny">{t("deny")}</option>
                  </select>
                </label>
              ))}
            </fieldset>
          ))}
        </div>
        <DialogFooter>
          <Button onClick={onClose}>{t("cancel")}</Button>
          <Button kind="primary" disabled={!valid} onClick={submit}>
            {t("create")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ProvidersPage({
  allowed,
}: {
  allowed: (capability?: string) => boolean;
}) {
  const { t } = useI18n();
  const listed = useAsync(
    () => request<{ items?: Record<string, any>[] }>("/providers"),
    [],
  );
  const items = listed.data?.items || [];
  const [selected, setSelected] = useState("");
  const [form, setForm] = useState({
    type: "oidc",
    displayName: "",
    issuer: "",
    clientId: "",
    clientSecret: "",
    caPem: "",
    scopes: "openid\nprofile\nemail",
    allowedSigningAlgs: "RS256",
    requiredClaims: "sub",
    subjectClaim: "sub",
    groupsClaim: "groups",
    displayNameClaim: "name",
    emailClaim: "email",
    httpTimeout: "10s",
    enabled: true,
    reason: "",
  });
  const [message, setMessage] = useState<{
    text: string;
    tone: "error" | "success";
  }>();
  const [busy, setBusy] = useState("");
  const [pending, setPending] = useState<{
    providerId: string;
    changeId: string;
    idempotencyKey: string;
  }>();
  const selectedItem = items.find((item) => item.providerId === selected);
  const issuerValid = validOIDCIssuer(form.issuer.trim());
  useEffect(() => {
    if (selectedItem) {
      const config = selectedItem.config || {};
      setForm({
        type: selectedItem.type || "oidc",
        displayName: config.displayName || "",
        issuer: config.issuer || "",
        clientId: config.clientId || "",
        clientSecret: "",
        caPem: config.caPem || "",
        scopes: (config.scopes || []).join("\n"),
        allowedSigningAlgs: (config.allowedSigningAlgs || ["RS256"]).join("\n"),
        requiredClaims: (config.requiredClaims || ["sub"]).join("\n"),
        subjectClaim: config.claims?.subject || "sub",
        groupsClaim: config.claims?.groups || "groups",
        displayNameClaim: config.claims?.displayName || "name",
        emailClaim: config.claims?.email || "email",
        httpTimeout: config.httpTimeout || "10s",
        enabled: config.enabled !== false,
        reason: "",
      });
    }
  }, [selected, selectedItem]);
  const providerID = () =>
    selected ||
    (document.getElementById("provider-id") as HTMLInputElement)?.value.trim();
  const submit = async (mode: "validate" | "draft") => {
    setBusy(mode);
    setMessage(undefined);
    try {
      const id = providerID();
      const lines = (value: string) =>
        value
          .split(/[,\n]/)
          .map((item) => item.trim())
          .filter(Boolean);
      const body = {
        type: form.type,
        config: {
          displayName: form.displayName,
          issuer: form.issuer,
          clientId: form.clientId,
          clientSecret: form.clientSecret,
          caPem: form.caPem,
          scopes: lines(form.scopes),
          allowedSigningAlgs: lines(form.allowedSigningAlgs),
          requiredClaims: lines(form.requiredClaims),
          claims: {
            subject: form.subjectClaim,
            groups: form.groupsClaim,
            displayName: form.displayNameClaim,
            email: form.emailClaim,
          },
          httpTimeout: form.httpTimeout,
          enabled: form.enabled,
        },
        reason: form.reason,
      };
      const path =
        mode === "validate"
          ? `/providers/${encodeURIComponent(id)}/validate`
          : `/providers/${encodeURIComponent(id)}/drafts`;
      const idempotencyKey = crypto.randomUUID();
      const result = await mutation<{
        providerId?: string;
        changeId?: string;
        objectId?: string;
      }>(path, "POST", body, {
        idempotencyKey,
      });
      if (mode === "draft" && result.changeId)
        setPending({
          providerId: result.providerId || id,
          changeId: result.changeId,
          idempotencyKey,
        });
      setMessage({
        text:
          mode === "validate"
            ? t("connectivity")
            : t("saveDraft"),
        tone: "success",
      });
      await listed.reload();
    } catch (error) {
      setMessage({ text: (error as Error).message, tone: "error" });
    } finally {
      setBusy("");
    }
  };
  const publish = async () => {
    if (!pending) return;
    setBusy("publish");
    try {
      await mutation(
        `/providers/${encodeURIComponent(pending.providerId)}/changes/${encodeURIComponent(pending.changeId)}/publish`,
        "POST",
        { reason: form.reason },
        { idempotencyKey: pending.idempotencyKey },
      );
      setMessage({
        text: t("publish"),
        tone: "success",
      });
      setPending(undefined);
      await listed.reload();
    } catch (error) {
      setMessage({ text: (error as Error).message, tone: "error" });
    } finally {
      setBusy("");
    }
  };
  return (
    <>
      <PageHeader
        title={t("providers")}
        description={t("providersDesc")}
        actions={
          <Button onClick={() => void listed.reload()}>
            <RefreshCw size={15} />
            {t("refresh")}
          </Button>
        }
      />
      {listed.loading ? (
        <Loading />
      ) : listed.error ? (
        <Notice>{listed.error}</Notice>
      ) : (
        <div className="two-column">
          <section className="panel provider-list-panel">
            <div className="section-title">
              <div>
                <h2>{t("providers")}</h2>
                <p>
                  {items.length} {t("configured")}
                </p>
              </div>
            </div>
            {items.map((item) => (
              <button
                className={`provider-row ${selected === item.providerId ? "active" : ""}`}
                key={item.providerId}
                onClick={() => {
                  setSelected(item.providerId);
                  setPending(undefined);
                }}
              >
                <span className="provider-icon">
                  <Network size={17} />
                </span>
                <span>
                  <strong>{item.providerId}</strong>
                  <small>{item.type}</small>
                </span>
                <span className={`state-dot ${item.active ? "active" : ""}`} />
              </button>
            ))}
            <button
              className={`provider-row ${selected === "" ? "active" : ""}`}
              onClick={() => {
                setSelected("");
                setPending(undefined);
                setForm({
                  type: "oidc",
                  displayName: "",
                  issuer: "",
                  clientId: "",
                  clientSecret: "",
                  caPem: "",
                  scopes: "openid\nprofile\nemail",
                  allowedSigningAlgs: "RS256",
                  requiredClaims: "sub",
                  subjectClaim: "sub",
                  groupsClaim: "groups",
                  displayNameClaim: "name",
                  emailClaim: "email",
                  httpTimeout: "10s",
                  enabled: true,
                  reason: "",
                });
              }}
            >
              <span className="provider-icon">+</span>
              <span>
                <strong>{t("create")}</strong>
                <small>OIDC Provider</small>
              </span>
            </button>
          </section>
          <section className="panel form-panel">
            <h2>{selected || t("selectProvider")}</h2>
            {message && <Notice tone={message.tone}>{message.text}</Notice>}
            {!issuerValid && form.issuer.trim() && (
              <Notice>Issuer URL must be an absolute HTTPS URL without credentials, query parameters, or a fragment.</Notice>
            )}
            <div className="form-grid">
              <label>
                {t("providerId")}
                <input
                  id="provider-id"
                  disabled={!!selected}
                  defaultValue={selected}
                  placeholder="corporate"
                />
              </label>
              <label>
                {t("providerType")}
                <select
                  value={form.type}
                  onChange={(event) =>
                    setForm({ ...form, type: event.target.value })
                  }
                >
                  <option value="oidc">OIDC</option>
                </select>
              </label>
              <label>
                Display name
                <input
                  value={form.displayName}
                  onChange={(event) =>
                    setForm({ ...form, displayName: event.target.value })
                  }
                />
              </label>
              <label>
                Enabled
                <select
                  value={String(form.enabled)}
                  onChange={(event) =>
                    setForm({ ...form, enabled: event.target.value === "true" })
                  }
                >
                  <option value="true">Yes</option>
                  <option value="false">No</option>
                </select>
              </label>
              <label className="full">
                Issuer URL
                <input
                  type="url"
                  value={form.issuer}
                  onChange={(event) =>
                    setForm({ ...form, issuer: event.target.value })
                  }
                  placeholder="https://tenant.auth0.com/"
                />
              </label>
              <label>
                Client ID
                <input
                  value={form.clientId}
                  onChange={(event) =>
                    setForm({ ...form, clientId: event.target.value })
                  }
                />
              </label>
              <label>
                Client Secret
                <input
                  type="password"
                  autoComplete="new-password"
                  value={form.clientSecret}
                  onChange={(event) =>
                    setForm({ ...form, clientSecret: event.target.value })
                  }
                  placeholder={
                    selectedItem?.clientSecretConfigured
                      ? "•••••••• (leave blank to keep)"
                      : "Required"
                  }
                />
              </label>
              <label className="full">
                Private CA PEM (optional)
                <textarea
                  value={form.caPem}
                  onChange={(event) =>
                    setForm({ ...form, caPem: event.target.value })
                  }
                  placeholder="-----BEGIN CERTIFICATE-----"
                />
              </label>
              <label className="full">
                Scopes
                <textarea
                  value={form.scopes}
                  onChange={(event) =>
                    setForm({ ...form, scopes: event.target.value })
                  }
                />
              </label>
              <label>
                Allowed signing algorithms
                <textarea
                  value={form.allowedSigningAlgs}
                  onChange={(event) =>
                    setForm({ ...form, allowedSigningAlgs: event.target.value })
                  }
                />
              </label>
              <label>
                Required claims
                <textarea
                  value={form.requiredClaims}
                  onChange={(event) =>
                    setForm({ ...form, requiredClaims: event.target.value })
                  }
                />
              </label>
              <label>
                Subject claim
                <input
                  value={form.subjectClaim}
                  onChange={(event) =>
                    setForm({ ...form, subjectClaim: event.target.value })
                  }
                />
              </label>
              <label>
                Groups claim
                <input
                  value={form.groupsClaim}
                  onChange={(event) =>
                    setForm({ ...form, groupsClaim: event.target.value })
                  }
                />
              </label>
              <label>
                Display name claim
                <input
                  value={form.displayNameClaim}
                  onChange={(event) =>
                    setForm({ ...form, displayNameClaim: event.target.value })
                  }
                />
              </label>
              <label>
                Email claim
                <input
                  value={form.emailClaim}
                  onChange={(event) =>
                    setForm({ ...form, emailClaim: event.target.value })
                  }
                />
              </label>
              <label>
                HTTP timeout
                <input
                  value={form.httpTimeout}
                  onChange={(event) =>
                    setForm({ ...form, httpTimeout: event.target.value })
                  }
                />
              </label>
              <label className="full">
                {t("reason")}
                <input
                  value={form.reason}
                  onChange={(event) =>
                    setForm({ ...form, reason: event.target.value })
                  }
                  placeholder={t("operationReason")}
                />
              </label>
            </div>
            <div className="panel-actions">
              {allowed("platform.identity.providers.manage") && (
                <Button
                  busy={busy === "validate"}
                  disabled={!issuerValid}
                  onClick={() => void submit("validate")}
                >
                  {t("connectivity")}
                </Button>
              )}
              {allowed("platform.identity.providers.manage") && (
                <Button
                  kind="primary"
                  busy={busy === "draft"}
                  disabled={form.reason.length < 8 || !issuerValid}
                  onClick={() => void submit("draft")}
                >
                  {t("saveDraft")}
                </Button>
              )}
              {pending && allowed("platform.identity.providers.manage") && (
                <Button
                  kind="primary"
                  busy={busy === "publish"}
                  onClick={() => void publish()}
                >
                  {t("publish")}
                </Button>
              )}
            </div>
          </section>
        </div>
      )}
    </>
  );
}

type OAuthClientView = {
  id: string;
  name: string;
  public: boolean;
  redirectUris: string[];
  grantTypes: string[];
  responseTypes: string[];
  scopes: string[];
  trusted: boolean;
  enabled: boolean;
  builtin: boolean;
  machinePrincipalId?: string;
};

const oauthGrantOptions = [
  "authorization_code",
  "refresh_token",
  "implicit",
  "password",
  "client_credentials",
];
const oauthResponseOptions = [
  "code",
  "token",
  "id_token",
  "id_token token",
  "code token",
  "code id_token",
  "code id_token token",
];
const oauthScopeOptions = [
  "openid",
  "profile",
  "email",
  "offline_access",
  "kubeloop.api",
];
const emptyOAuthClient = (): OAuthClientView => ({
  id: "",
  name: "",
  public: true,
  redirectUris: [""],
  grantTypes: ["authorization_code", "refresh_token"],
  responseTypes: ["code"],
  scopes: [...oauthScopeOptions],
  trusted: false,
  enabled: true,
  builtin: false,
});

function OAuthClientsPage({
  allowed,
}: {
  allowed: (capability?: string) => boolean;
}) {
  const { locale, t } = useI18n();
  const clients = useAsync(
    () => request<{ items: OAuthClientView[] }>("/oauth-clients"),
    [],
  );
  const [editing, setEditing] = useState<OAuthClientView>();
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const save = async () => {
    if (!editing) return;
    setBusy(true);
    setMessage("");
    try {
      const path =
        editing.builtin ||
        clients.data?.items.some((item) => item.id === editing.id)
          ? `/oauth-clients/${encodeURIComponent(editing.id)}`
          : "/oauth-clients";
      const result = await mutation<
        OAuthClientView & { clientSecret?: string }
      >(path, path === "/oauth-clients" ? "POST" : "PUT", editing);
      if (result.clientSecret) setSecret(result.clientSecret);
      setEditing(undefined);
      await clients.reload();
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const rotate = async (client: OAuthClientView) => {
    setBusy(true);
    try {
      const result = await mutation<{ clientSecret: string }>(
        `/oauth-clients/${encodeURIComponent(client.id)}/secret`,
        "POST",
        {},
      );
      setSecret(result.clientSecret);
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const toggle = async (client: OAuthClientView) => {
    setBusy(true);
    try {
      await mutation(
        `/oauth-clients/${encodeURIComponent(client.id)}/enabled`,
        "POST",
        { enabled: !client.enabled },
      );
      await clients.reload();
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const remove = async (client: OAuthClientView) => {
    if (
      !confirm(
        locale === "zh-CN" ? `删除 ${client.name}？` : `Delete ${client.name}?`,
      )
    )
      return;
    setBusy(true);
    try {
      await mutation(
        `/oauth-clients/${encodeURIComponent(client.id)}`,
        "DELETE",
      );
      await clients.reload();
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <PageHeader
        title={t("oauthClients")}
        description={t("oauthClientsDesc")}
        actions={
          <>
            <Button onClick={() => void clients.reload()}>
              <RefreshCw size={15} />
              {t("refresh")}
            </Button>
            {allowed("platform.oauth-clients.manage") && (
              <Button
                kind="primary"
                onClick={() => setEditing(emptyOAuthClient())}
              >
                {locale === "zh-CN" ? "新建 Client" : "New client"}
              </Button>
            )}
          </>
        }
      />
      {message && <Notice>{message}</Notice>}
      {clients.loading ? (
        <Loading />
      ) : clients.error ? (
        <Notice>{clients.error}</Notice>
      ) : (
        <section className="table-panel">
          <table>
            <thead>
              <tr>
                <th>Client</th>
                <th>Type</th>
                <th>Grants</th>
                <th>Scopes</th>
                <th>{t("status")}</th>
                <th>{t("actions")}</th>
              </tr>
            </thead>
            <tbody>
              {(clients.data?.items || []).map((client) => (
                <tr key={client.id}>
                  <td>
                    <strong>{client.name}</strong>
                    <br />
                    <small>
                      {client.id}
                      {client.builtin ? " · built-in" : ""}
                    </small>
                  </td>
                  <td>
                    {client.public ? "Public" : "Confidential"}
                    {client.machinePrincipalId ? " · Machine" : ""}
                  </td>
                  <td>{client.grantTypes.join(", ")}</td>
                  <td>{client.scopes.join(", ")}</td>
                  <td>
                    <Pill value={client.enabled ? "Enabled" : "Disabled"} />
                  </td>
                  <td>
                    <div className="footer-buttons">
                      {allowed("platform.oauth-clients.manage") && (
                        <Button
                          kind="ghost"
                          onClick={() => setEditing(structuredClone(client))}
                        >
                          {locale === "zh-CN" ? "编辑" : "Edit"}
                        </Button>
                      )}
                      {!client.public &&
                        allowed("platform.oauth-clients.manage") && (
                          <Button
                            kind="ghost"
                            busy={busy}
                            onClick={() => void rotate(client)}
                          >
                            {locale === "zh-CN"
                              ? "轮换 Secret"
                              : "Rotate secret"}
                          </Button>
                        )}
                      {!client.builtin &&
                        allowed("platform.oauth-clients.manage") && (
                          <Button
                            kind="ghost"
                            onClick={() => void toggle(client)}
                          >
                            {client.enabled
                              ? locale === "zh-CN"
                                ? "停用"
                                : "Disable"
                              : locale === "zh-CN"
                                ? "启用"
                                : "Enable"}
                          </Button>
                        )}
                      {!client.builtin &&
                        allowed("platform.oauth-clients.manage") && (
                          <Button
                            kind="danger"
                            onClick={() => void remove(client)}
                          >
                            {t("remove")}
                          </Button>
                        )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {!clients.data?.items.length && <Empty>{t("empty")}</Empty>}
        </section>
      )}
      <OAuthClientDialog
        value={editing}
        busy={busy}
        locale={locale}
        onClose={() => setEditing(undefined)}
        onChange={setEditing}
        onSave={() => void save()}
      />
      <Dialog
        open={!!secret}
        onOpenChange={(open) => {
          if (!open) setSecret("");
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {locale === "zh-CN"
                ? "Client Secret（仅显示一次）"
                : "Client secret (shown once)"}
            </DialogTitle>
            <DialogDescription>
              {locale === "zh-CN"
                ? "立即复制并安全保存，关闭后无法再次查看。"
                : "Copy and store it securely now. It cannot be viewed again."}
            </DialogDescription>
          </DialogHeader>
          <pre className="recovery-box">{secret}</pre>
          <DialogFooter>
            <Button onClick={() => void navigator.clipboard.writeText(secret)}>
              {locale === "zh-CN" ? "复制" : "Copy"}
            </Button>
            <Button kind="primary" onClick={() => setSecret("")}>
              {t("close")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function OAuthClientDialog({
  value,
  busy,
  locale,
  onClose,
  onChange,
  onSave,
}: {
  value?: OAuthClientView;
  busy: boolean;
  locale: Locale;
  onClose: () => void;
  onChange: (value: OAuthClientView) => void;
  onSave: () => void;
}) {
  if (!value) return null;
  const toggle = (
    field: "grantTypes" | "responseTypes" | "scopes",
    item: string,
  ) =>
    onChange({
      ...value,
      [field]: value[field].includes(item)
        ? value[field].filter((entry) => entry !== item)
        : [...value[field], item],
    });
  const risky =
    value.grantTypes.some((grant) =>
      ["implicit", "password"].includes(grant),
    ) || value.responseTypes.some((response) => response.includes("token"));
  const redirectUris = value.redirectUris
    .map((item) => item.trim())
    .filter(Boolean);
  const redirectUrisValid =
    redirectUris.length > 0 && redirectUris.every(validOAuthRedirectURI);
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {locale === "zh-CN" ? "OAuth Client" : "OAuth client"}
          </DialogTitle>
          <DialogDescription>
            {locale === "zh-CN"
              ? "所有能力必须显式选择；新 Client 默认不启用高风险流程。"
              : "Every capability is explicit; risky grants are disabled by default."}
          </DialogDescription>
        </DialogHeader>
        <div className="form-grid">
          <label>
            Client ID
            <input
              disabled={value.builtin}
              value={value.id}
              onChange={(event) =>
                onChange({ ...value, id: event.target.value })
              }
            />
          </label>
          <label>
            {locale === "zh-CN" ? "名称" : "Name"}
            <input
              value={value.name}
              onChange={(event) =>
                onChange({ ...value, name: event.target.value })
              }
            />
          </label>
          <label>
            <input
              type="checkbox"
              checked={value.public}
              disabled={value.builtin}
              onChange={(event) =>
                onChange({ ...value, public: event.target.checked })
              }
            />{" "}
            Public client
          </label>
          <label>
            <input
              type="checkbox"
              checked={value.trusted}
              disabled={value.builtin}
              onChange={(event) =>
                onChange({ ...value, trusted: event.target.checked })
              }
            />{" "}
            {locale === "zh-CN"
              ? "可信（跳过 Consent）"
              : "Trusted (skip consent)"}
          </label>
          <label className="full">
            Redirect URIs
            <textarea
              value={value.redirectUris.join("\n")}
              onChange={(event) =>
                onChange({
                  ...value,
                  redirectUris: event.target.value.split("\n"),
                })
              }
            />
          </label>
          <OptionChecks
            label="Grant types"
            values={oauthGrantOptions}
            selected={value.grantTypes}
            disabled={value.builtin}
            onToggle={(item) => toggle("grantTypes", item)}
          />
          <OptionChecks
            label="Response types"
            values={oauthResponseOptions}
            selected={value.responseTypes}
            disabled={value.builtin}
            onToggle={(item) => toggle("responseTypes", item)}
          />
          <OptionChecks
            label="Scopes"
            values={oauthScopeOptions}
            selected={value.scopes}
            onToggle={(item) => toggle("scopes", item)}
          />
        </div>
        {risky && (
          <Notice tone="warning">
            {locale === "zh-CN"
              ? "Implicit、Hybrid 与 Password 会扩大凭据暴露面，只应为受控兼容场景启用。"
              : "Implicit, hybrid, and password flows increase credential exposure. Enable only for controlled compatibility cases."}
          </Notice>
        )}
        {!redirectUrisValid && value.redirectUris.some((item) => item.trim()) && (
          <Notice>
            {locale === "zh-CN"
              ? "Redirect URI 必须使用 HTTPS；仅 localhost、127.0.0.1 或 ::1 可使用 HTTP，且不能包含 fragment。"
              : "Redirect URIs must use HTTPS. Only localhost, 127.0.0.1, or ::1 may use HTTP, and fragments are not allowed."}
          </Notice>
        )}
        <DialogFooter>
          <Button onClick={onClose}>
            {locale === "zh-CN" ? "取消" : "Cancel"}
          </Button>
          <Button
            kind="primary"
            busy={busy}
            disabled={
              !value.id ||
              !value.name ||
              !redirectUrisValid ||
              !value.grantTypes.length ||
              !value.responseTypes.length ||
              !value.scopes.length
            }
            onClick={onSave}
          >
            {locale === "zh-CN" ? "保存" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
function OptionChecks({
  label,
  values,
  selected,
  disabled,
  onToggle,
}: {
  label: string;
  values: string[];
  selected: string[];
  disabled?: boolean;
  onToggle: (item: string) => void;
}) {
  return (
    <fieldset className="full">
      <legend>{label}</legend>
      <div className="check-grid">
        {values.map((item) => (
          <label key={item}>
            <input
              type="checkbox"
              checked={selected.includes(item)}
              disabled={disabled}
              onChange={() => onToggle(item)}
            />
            <code>{item}</code>
          </label>
        ))}
      </div>
    </fieldset>
  );
}

function UsersPage({ allowed }: { allowed: (capability?: string) => boolean }) {
  const { t } = useI18n();
  const users = useAsync(
    () => request<{ items?: Record<string, any>[] }>("/users"),
    [],
  );
  const [message, setMessage] = useState<string>();
  const [resetUser, setResetUser] = useState<Record<string, any>>();
  const [createOpen, setCreateOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const createUser = async (values: Record<string, string>) => {
    setBusy(true);
    try {
      await mutation("/users", "POST", values);
      setCreateOpen(false);
      await users.reload();
      setMessage(undefined);
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const resetPassword = async (values: Record<string, string>) => {
    if (!resetUser) return;
    setBusy(true);
    try {
      await mutation(
        `/users/${encodeURIComponent(resetUser.principalId)}/password`,
        "PUT",
        { password: values.password },
      );
      setResetUser(undefined);
      setMessage(undefined);
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <PageHeader
        title={t("users")}
        description={t("usersDesc")}
        actions={
          <>
            {allowed("platform.identity.users.manage") && (
              <Button kind="primary" onClick={() => setCreateOpen(true)}>
                {t("createUser")}
              </Button>
            )}
            <Button onClick={() => void users.reload()}>
              <RefreshCw size={15} />
              {t("refresh")}
            </Button>
          </>
        }
      />
      {message && <Notice>{message}</Notice>}
      {users.loading ? (
        <Loading />
      ) : users.error ? (
        <Notice>{users.error}</Notice>
      ) : (
        <section className="table-panel">
          <table>
            <thead>
              <tr>
                <th>{t("username")}</th>
                <th>{t("displayName")}</th>
                <th>{t("email")}</th>
                <th>{t("status")}</th>
                <th>{t("mfa")}</th>
                <th>{t("actions")}</th>
              </tr>
            </thead>
            <tbody>
              {(users.data?.items || []).map((user) => (
                <tr key={user.principalId}>
                  <td className="strong-cell">{user.username}</td>
                  <td>{user.displayName || "—"}</td>
                  <td>{user.email || "—"}</td>
                  <td>
                    <Pill
                      value={user.enabled ? t("enabled") : t("disabled")}
                      tone={user.enabled ? "good" : "bad"}
                    />
                  </td>
                  <td>{user.mfaEnabled ? "TOTP" : "—"}</td>
                  <td>
                    <div className="footer-buttons">
                      {allowed("platform.identity.users.manage") && (
                        <>
                          <Button
                            kind="ghost"
                            onClick={async () => {
                              try {
                                await mutation(
                                  `/users/${encodeURIComponent(user.principalId)}/status`,
                                  "PATCH",
                                  { enabled: !user.enabled },
                                );
                                await users.reload();
                              } catch (error) {
                                setMessage((error as Error).message);
                              }
                            }}
                          >
                            {user.enabled ? t("disabled") : t("enabled")}
                          </Button>
                          <Button
                            kind="ghost"
                            onClick={() => setResetUser(user)}
                          >
                            {t("resetPassword")}
                          </Button>
                        </>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {!users.data?.items?.length && <Empty>{t("empty")}</Empty>}
        </section>
      )}
      <SecureActionDialog
        open={createOpen}
        title={t("createUser")}
        fields={[
          {
            name: "username",
            label: t("username"),
            autoComplete: "off",
            minLength: 3,
          },
          { name: "displayName", label: t("displayName"), optional: true },
          {
            name: "email",
            label: t("email"),
            type: "email",
            autoComplete: "off",
            optional: true,
          },
          {
            name: "password",
            label: t("password"),
            type: "password",
            autoComplete: "new-password",
            minLength: 12,
          },
        ]}
        busy={busy}
        onClose={() => setCreateOpen(false)}
        onConfirm={(values) => void createUser(values)}
      />
      <SecureActionDialog
        open={!!resetUser}
        title={`${t("resetPassword")} · ${resetUser?.username || ""}`}
        fields={[
          {
            name: "password",
            label: t("password"),
            type: "password",
            autoComplete: "new-password",
            minLength: 12,
          },
        ]}
        busy={busy}
        onClose={() => setResetUser(undefined)}
        onConfirm={(values) => void resetPassword(values)}
      />
    </>
  );
}

function SecurityPage() {
  const { locale, t } = useI18n();
  const user = useAsync(() => request<Record<string, any>>("/users/me"), []);
  const [message, setMessage] = useState<string>();
  const [enrollment, setEnrollment] = useState<Record<string, any>>();
  const [codes, setCodes] = useState<string[]>();
  const [action, setAction] = useState<"recovery" | "disable">();
  const [busy, setBusy] = useState(false);
  const complete = async (values: Record<string, string>) => {
    if (!action) return;
    setBusy(true);
    setMessage(undefined);
    try {
      if (action === "recovery") {
        const result = await mutation<{ recoveryCodes: string[] }>(
          "/users/me/mfa/recovery-codes",
          "POST",
          { code: values.code },
        );
        setCodes(result.recoveryCodes || []);
      } else {
        await mutation("/users/me/mfa/totp", "DELETE", {
          password: values.password,
          code: values.code,
        });
        setCodes(undefined);
        await user.reload();
      }
      setAction(undefined);
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const fields =
    action === "disable"
      ? [
          {
            name: "password",
            label: t("currentPassword"),
            type: "password",
            autoComplete: "current-password",
          },
          {
            name: "code",
            label: t("oneTimeCode"),
            autoComplete: "one-time-code",
            minLength: 6,
          },
        ]
      : [
          {
            name: "code",
            label: t("oneTimeCode"),
            autoComplete: "one-time-code",
            minLength: 6,
          },
        ];
  return (
    <>
      <PageHeader title={t("security")} description={t("securityDesc")} />
      {user.loading ? (
        <Loading />
      ) : user.error ? (
        <Empty>{user.error}</Empty>
      ) : (
        <section className="panel security-card">
          <div className="security-avatar">
            <ShieldCheck size={25} />
          </div>
          <div className="security-copy">
            <span>{t("accountSecurity")}</span>
            <h2>{user.data?.displayName || user.data?.username}</h2>
            <p>
              {user.data?.mfaEnabled
                ? locale === "zh-CN"
                  ? "此账户已启用 TOTP。"
                  : "TOTP is enabled for this account."
                : locale === "zh-CN"
                  ? "添加第二重验证以保护高权限操作。"
                  : "Add a second factor to protect privileged operations."}
            </p>
          </div>
          {message && <Notice>{message}</Notice>}
          {codes ? (
            <div className="recovery-box">
              <h3>{t("recoveryCodes")}</h3>
              <p>
                {locale === "zh-CN"
                  ? "请立即保存这些一次性恢复码；之后不会再次显示。"
                  : "Save these one-time codes now. They will not be displayed again."}
              </p>
              <pre>{codes.join("\n")}</pre>
              <Button onClick={() => setCodes(undefined)}>{t("close")}</Button>
            </div>
          ) : enrollment ? (
            <TotpConfirm
              enrollment={enrollment}
              onComplete={(next) => {
                setCodes(next);
                setEnrollment(undefined);
                void user.reload();
              }}
              onError={setMessage}
            />
          ) : user.data?.mfaEnabled ? (
            <div className="panel-actions">
              <Button kind="primary" onClick={() => setAction("recovery")}>
                {t("regenerateCodes")}
              </Button>
              <Button kind="danger" onClick={() => setAction("disable")}>
                {t("disableTotp")}
              </Button>
            </div>
          ) : (
            <Button
              kind="primary"
              onClick={async () => {
                try {
                  setEnrollment(
                    await mutation("/users/me/mfa/totp/start", "POST", {}),
                  );
                } catch (error) {
                  setMessage((error as Error).message);
                }
              }}
            >
              {t("enableTotp")}
            </Button>
          )}
        </section>
      )}
      <SecureActionDialog
        open={!!action}
        title={action === "disable" ? t("disableTotp") : t("regenerateCodes")}
        fields={fields}
        busy={busy}
        danger={action === "disable"}
        onClose={() => setAction(undefined)}
        onConfirm={(values) => void complete(values)}
      />
    </>
  );
}
function TotpConfirm({
  enrollment,
  onComplete,
  onError,
}: {
  enrollment: Record<string, any>;
  onComplete: (codes: string[]) => void;
  onError: (message: string) => void;
}) {
  const { t } = useI18n();
  const [code, setCode] = useState("");
  return (
    <div className="totp-setup">
      {enrollment.qrCodeDataUrl && (
        <img src={enrollment.qrCodeDataUrl} alt="TOTP enrollment QR code" />
      )}
      <div>
        <code>{enrollment.secret}</code>
        <label>
          {t("oneTimeCode")}
          <input
            value={code}
            onChange={(event) => setCode(event.target.value)}
            autoComplete="one-time-code"
            inputMode="numeric"
          />
        </label>
        <Button
          kind="primary"
          onClick={async () => {
            try {
              const result = await mutation<{ recoveryCodes: string[] }>(
                "/users/me/mfa/totp/confirm",
                "POST",
                { enrollmentToken: enrollment.enrollmentToken, code },
              );
              onComplete(result.recoveryCodes || []);
            } catch (error) {
              onError((error as Error).message);
            }
          }}
        >
          {t("confirmTotp")}
        </Button>
      </div>
    </div>
  );
}

const listConfig: Record<
  Exclude<
    ViewKey,
    | "overview"
    | "roles"
    | "permissions"
    | "assignments"
    | "delegations"
    | "providers"
    | "oauthClients"
    | "users"
    | "security"
  >,
  { path: string; scoped?: boolean; columns: [string, MessageKey][] }
> = {
  principals: {
    path: "/principals",
    columns: [
      ["displayName", "name"],
      ["email", "email"],
      ["provider", "provider"],
      ["groups", "groups"],
      ["createdAt", "created"],
    ],
  },
  sessions: {
    path: "/sessions",
    scoped: true,
    columns: [
      ["id", "session"],
      ["namespace", "namespace"],
      ["clusterId", "cluster"],
      ["state", "state"],
      ["lastHeartbeatAt", "heartbeat"],
      ["expiresAt", "expires"],
    ],
  },
  tasks: {
    path: "/tasks",
    scoped: true,
    columns: [
      ["id", "task"],
      ["sessionId", "session"],
      ["type", "type"],
      ["state", "state"],
      ["createdAt", "created"],
      ["updatedAt", "updated"],
    ],
  },
  relays: {
    path: "/relays",
    columns: [
      ["relayId", "relay"],
      ["state", "state"],
      ["desiredState", "desired"],
      ["online", "online"],
      ["capacity", "capacity"],
      ["lastHeartbeatAt", "heartbeat"],
    ],
  },
  audit: {
    path: "/audit",
    columns: [
      ["createdAt", "time"],
      ["action", "action"],
      ["outcome", "outcome"],
      ["principalId", "principal"],
      ["resourceType", "resource"],
      ["resourceId", "resourceId"],
      ["requestId", "requestId"],
    ],
  },
};
function ListPage({
  view,
  capabilities,
  allowed,
}: {
  view: Exclude<
    ViewKey,
    | "overview"
    | "roles"
    | "permissions"
    | "assignments"
    | "delegations"
    | "providers"
    | "oauthClients"
    | "users"
    | "security"
  >;
  capabilities: Capabilities;
  allowed: (capability?: string) => boolean;
}) {
  const { locale, t } = useI18n();
  const config = listConfig[view];
  const namespaces = capabilities.namespaceScopes
    .map((scope) => scope.namespace)
    .filter(Boolean);
  const [namespace, setNamespace] = useState(namespaces[0] || "");
  const [cursor, setCursor] = useState("");
  const [history, setHistory] = useState<string[]>([]);
  const [search, setSearch] = useState("");
  const query = new URLSearchParams({ limit: "50" });
  if (cursor) query.set("cursor", cursor);
  if (config.scoped && namespace) query.set("namespace", namespace);
  const result = useAsync(
    () => request<ListResponse>(`${config.path}?${query}`),
    [view, cursor, namespace],
  );
  const filtered = (result.data?.items || []).filter(
    (item) =>
      !search ||
      JSON.stringify(item).toLowerCase().includes(search.toLowerCase()),
  );
  const [confirm, setConfirm] = useState<{
    label: string;
    path: string;
    etag?: number;
  }>();
  const [busy, setBusy] = useState(false);
  const operationFor = (item: Record<string, unknown>) => {
    if (view === "principals" && allowed("platform.sessions.revoke"))
      return {
        label: t("revoke"),
        path: `/principals/${encodeURIComponent(String(item.id))}/revoke`,
      };
    if (
      view === "sessions" &&
      item.state !== "stopped" &&
      allowed("platform.sessions.revoke")
    )
      return {
        label: t("stop"),
        path: `/sessions/${encodeURIComponent(String(item.id))}/stop`,
        etag: Number(item.generation),
      };
    if (
      view === "tasks" &&
      !["stopped", "failed"].includes(String(item.state)) &&
      allowed("platform.tasks.stop")
    )
      return {
        label: t("stop"),
        path: `/tasks/${encodeURIComponent(String(item.id))}/stop`,
        etag: Number(item.version),
      };
    if (
      view === "relays" &&
      item.desiredState === "draining" &&
      allowed("platform.relays.manage")
    )
      return {
        label: t("recover"),
        path: `/relays/${encodeURIComponent(String(item.relayId))}/recover`,
        etag: Number(item.controlVersion || 0),
      };
    if (view === "relays" && allowed("platform.relays.manage"))
      return {
        label: t("drain"),
        path: `/relays/${encodeURIComponent(String(item.relayId))}/drain`,
        etag: Number(item.controlVersion || 0),
      };
    return undefined;
  };
  const special = async (path: string) =>
    setConfirm({
      label: view === "audit" ? t("export") : t("triggerRecovery"),
      path,
    });
  return (
    <>
      <PageHeader
        title={t(viewMeta[view].title)}
        description={t(viewMeta[view].description)}
        actions={
          <>
            <SearchInput value={search} onChange={setSearch} />
            {config.scoped && namespaces.length > 0 && (
              <label className="select-filter">
                <span>{t("namespace")}</span>
                <select
                  value={namespace}
                  onChange={(event) => {
                    setNamespace(event.target.value);
                    setCursor("");
                    setHistory([]);
                  }}
                >
                  {namespaces.map((item) => (
                    <option key={item}>{item}</option>
                  ))}
                </select>
              </label>
            )}
            {view === "tasks" && allowed("platform.tasks.stop") && (
              <Button onClick={() => void special("/tasks/recovery")}>
                {t("triggerRecovery")}
              </Button>
            )}
            {view === "audit" && allowed("platform.audit.export") && (
              <Button onClick={() => void special("/audit/exports")}>
                {t("export")}
              </Button>
            )}
            <Button onClick={() => void result.reload()}>
              <RefreshCw size={15} />
              {t("refresh")}
            </Button>
          </>
        }
      />
      {result.loading ? (
        <Loading />
      ) : result.error ? (
        <Notice>{result.error}</Notice>
      ) : (
        <section className="table-panel">
          <table>
            <thead>
              <tr>
                {config.columns.map(([, label]) => (
                  <th key={label}>{t(label)}</th>
                ))}
                <th>{t("actions")}</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((item, index) => {
                const operation = operationFor(item);
                return (
                  <tr
                    key={String(
                      item.id || item.relayId || item.requestId || index,
                    )}
                  >
                    {config.columns.map(([key]) => (
                      <td key={key}>
                        {["state", "desiredState", "outcome"].includes(key) ? (
                          <Pill value={formatValue(key, item[key], locale)} />
                        ) : (
                          formatValue(key, item[key], locale)
                        )}
                      </td>
                    ))}
                    <td>
                      {operation && (
                        <Button
                          kind="ghost"
                          onClick={() => setConfirm(operation)}
                        >
                          {operation.label}
                        </Button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          {!filtered.length && <Empty>{t("empty")}</Empty>}
          <div className="pager">
            <Button
              disabled={!history.length}
              onClick={() => {
                const values = [...history];
                setCursor(values.pop() || "");
                setHistory(values);
              }}
            >
              <ChevronLeft size={15} />
              {t("previous")}
            </Button>
            <span>
              {filtered.length} {t("records")}
            </span>
            <Button
              disabled={!result.data?.nextCursor}
              onClick={() => {
                setHistory((values) => [...values, cursor]);
                setCursor(result.data?.nextCursor || "");
              }}
            >
              {t("next")}
              <ChevronRight size={15} />
            </Button>
          </div>
        </section>
      )}
      <ConfirmDialog
        open={!!confirm}
        title={confirm?.label || ""}
        detail={confirm?.path}
        busy={busy}
        onClose={() => setConfirm(undefined)}
        onConfirm={async (reason) => {
          if (!confirm) return;
          setBusy(true);
          try {
            const created = await mutation<{ jobId?: string }>(
              confirm.path,
              "POST",
              { reason },
              { etag: confirm.etag, idempotent: true },
            );
            if (view === "audit" && created.jobId) {
              const blob = await waitForAuditExport(created.jobId);
              const url = URL.createObjectURL(blob);
              const anchor = document.createElement("a");
              anchor.href = url;
              anchor.download = `kubeloop-audit-${created.jobId}.ndjson`;
              document.body.append(anchor);
              anchor.click();
              anchor.remove();
              URL.revokeObjectURL(url);
            }
            setConfirm(undefined);
            await result.reload();
          } finally {
            setBusy(false);
          }
        }}
      />
    </>
  );
}
function Pill({ value, tone }: { value: string; tone?: "good" | "bad" }) {
  const normalized = value.toLowerCase();
  const className =
    tone ||
    ([
      "active",
      "online",
      "success",
      "running",
      "ready",
      "enabled",
      "正常",
      "启用",
      "在线",
    ].some((item) => normalized.includes(item))
      ? "good"
      : ["failed", "offline", "disabled", "stopped", "停用", "离线"].some(
            (item) => normalized.includes(item),
          )
        ? "bad"
        : "neutral");
  return (
    <span className={`pill ${className}`}>
      <i />
      {value}
    </span>
  );
}
function formatValue(key: string, value: unknown, locale: Locale) {
  if (value == null || value === "") return "—";
  if (Array.isArray(value)) return value.join(", ") || "—";
  if (typeof value === "boolean") return value ? "Yes" : "No";
  if (typeof value === "object") {
    const record = value as Record<string, number>;
    if (key === "capacity")
      return `${record.activeLogicalStreams || 0}/${record.maximumLogicalStreams || 0} streams`;
    return JSON.stringify(value);
  }
  if (key.endsWith("At")) {
    const date = new Date(String(value));
    if (!Number.isNaN(date.getTime()))
      return new Intl.DateTimeFormat(locale, {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(date);
  }
  return String(value);
}
