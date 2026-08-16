import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import {
  Activity,
  AppWindow,
  ChevronRight,
  CircleUserRound,
  FileClock,
  KeyRound,
  Languages,
  LayoutDashboard,
  LogOut,
  Menu,
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
  request,
  startOIDC,
} from "./api";
import { Button, Loading, Notice } from "./components";
import {
  detectLocale,
  I18nContext,
  localeStorageKey,
  Locale,
  messages,
} from "./i18n";
import type { Bootstrap, Organization, ViewKey } from "./types";
import {
  GroupsPage,
  InvitationsPage,
  OAuthClientsPage,
  UsersPage,
} from "./pages/iam";
import { AuditPage, GrantsPage, OverviewPage, PoliciesPage, RuntimePage, SessionsPage } from "./pages/operations";

type AuthState = {
  status: "loading" | "login" | "ready";
  bootstrap?: Bootstrap;
  error?: string;
};
const labels = {
  "zh-CN": {
    brand: "KubeLoop IAM",
    login: "登录管理后台",
    loginHint:
      "使用 KubeLoop 本地账号登录。浏览器只保存语言和当前会话 CSRF。",
    initialize: "初始化全新 IAM",
    bootstrapToken: "一次性 Bootstrap Token",
    organization: "首个组织",
    slug: "组织 Slug",
    username: "管理员用户名",
    displayName: "显示名称",
    email: "邮箱",
    password: "初始密码",
    complete: "创建平台管理员",
    selectOrg: "当前组织",
    signOut: "退出登录",
    noMethods: "没有可用的登录方式。",
    sections: {
      overview: "总览",
      directory: "目录",
      organization: "组织",
      access: "访问控制",
      applications: "应用",
      security: "安全",
      audit: "审计",
      runtime: "运行资源",
    },
  },
  "en-US": {
    brand: "KubeLoop IAM",
    login: "Sign in to Admin",
    loginHint:
      "Use your KubeLoop local account. The browser stores only language and session CSRF.",
    initialize: "Initialize new IAM",
    bootstrapToken: "One-time bootstrap token",
    organization: "First organization",
    slug: "Organization slug",
    username: "Admin username",
    displayName: "Display name",
    email: "Email",
    password: "Initial password",
    complete: "Create platform admin",
    selectOrg: "Current organization",
    signOut: "Sign out",
    noMethods: "No sign-in method is available.",
    sections: {
      overview: "Overview",
      directory: "Directory",
      organization: "Organizations",
      access: "Access control",
      applications: "Applications",
      security: "Security",
      audit: "Audit",
      runtime: "Runtime resources",
    },
  },
} as const;
const nav: Array<{
  section: keyof (typeof labels)["zh-CN"]["sections"];
  items: Array<[ViewKey, string]>;
}> = [
  { section: "overview", items: [["overview", "总览 / Overview"]] },
  {
    section: "directory",
    items: [
      ["users", "用户 / Users"],
      ["groups", "用户组 / Groups"],
      ["invitations", "邀请 / Invitations"],
    ],
  },
  { section: "applications", items: [["oauthClients", "OAuth Clients"]] },
  {
    section: "security",
    items: [
      ["policies", "策略 / Policies"],
      ["sessions", "会话 / Sessions"],
      ["grants", "OAuth Grants"],
    ],
  },
  { section: "audit", items: [["audit", "审计 / Audit"]] },
  { section: "runtime", items: [["runtime", "运行资源 / Runtime"]] },
];
function currentView(): ViewKey {
  const value = location.hash.replace(/^#\/?/, "").split("/")[0] as ViewKey;
  return nav.some((group) => group.items.some(([key]) => key === value))
    ? value
    : "overview";
}

export default function App() {
  const [locale, setLocaleState] = useState<Locale>(detectLocale),
    [auth, setAuth] = useState<AuthState>({ status: "loading" }),
    [view, setView] = useState<ViewKey>(currentView),
    [menu, setMenu] = useState(false),
    [organizations, setOrganizations] = useState<Organization[]>([]),
    [organizationId, setOrganizationId] = useState("");
  const setLocale = (next: Locale) => {
    localStorage.setItem(localeStorageKey, next);
    document.documentElement.lang = next;
    setLocaleState(next);
  };
  const t = useCallback(
    (key: keyof (typeof messages)["zh-CN"]) => messages[locale][key],
    [locale],
  );
  const bootstrap = useCallback(async () => {
    try {
      await finishOIDCCallback();
      const result = await request<Bootstrap>(`${managementBase}/bootstrap`);
      setAuth({ status: "ready", bootstrap: result });
    } catch (cause) {
      const error = cause as ApiError;
      setAuth({
        status: "login",
        error: error.status === 401 ? "" : error.message,
      });
    }
  }, []);
  useEffect(() => {
    document.documentElement.lang = locale;
    void bootstrap();
  }, [locale, bootstrap]);
  useEffect(() => {
    const listener = () => {
      sessionStorage.removeItem(csrfStorageKey);
      setAuth({ status: "login" });
    };
    addEventListener(authenticationLostEvent, listener);
    return () => removeEventListener(authenticationLostEvent, listener);
  }, []);
  useEffect(() => {
    const listener = () => setView(currentView());
    addEventListener("hashchange", listener);
    return () => removeEventListener("hashchange", listener);
  }, []);
  useEffect(() => {
    if (auth.status !== "ready") return;
    request<{ items: Organization[] }>("/organizations")
      .then((result) => {
        setOrganizations(result.items || []);
        setOrganizationId((current) => current || result.items?.[0]?.id || "");
      })
      .catch(() => setOrganizations([]));
  }, [auth.status]);
  const context = useMemo(() => ({ locale, setLocale, t }), [locale, t]);
  return (
    <I18nContext.Provider value={context}>
      {auth.status === "loading" ? (
        <main className="boot">
          <div className="brand-mark">KL</div>
          <Loading />
        </main>
      ) : auth.status === "login" ? (
        <Login
          locale={locale}
          setLocale={setLocale}
          error={auth.error}
          onReady={bootstrap}
        />
      ) : (
        <Shell
          locale={locale}
          setLocale={setLocale}
          auth={auth}
          view={view}
          menu={menu}
          setMenu={setMenu}
          organizations={organizations}
          organizationId={organizationId}
          onView={(next) => {
            location.hash = `/${next}`;
            setView(next);
            setMenu(false);
          }}
          onLogout={async () => {
            await logout();
            setAuth({ status: "login" });
          }}
        />
      )}
    </I18nContext.Provider>
  );
}

function Login({
  locale,
  setLocale,
  error,
  onReady,
}: {
  locale: Locale;
  setLocale: (value: Locale) => void;
  error?: string;
  onReady: () => Promise<void>;
}) {
  const text = labels[locale],
    [methods, setMethods] = useState<
      Array<{ id: string; displayName: string }>
    >([]),
    [setup, setSetup] = useState(false),
    [busy, setBusy] = useState(false),
    [setupError, setSetupError] = useState("");
  useEffect(() => {
    request<{
      authMethods?: Array<{
        id: string;
        displayName: string;
        interaction: string;
      }>;
    }>("/.well-known/kubeloop")
      .then((value) =>
        setMethods(
          (value.authMethods || []).filter(
            (item) => item.interaction === "browser",
          ),
        ),
      )
      .catch(() => setMethods([]));
  }, []);
  const complete = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setBusy(true);
    setSetupError("");
    const data = new FormData(event.currentTarget);
    try {
      await request(`${managementBase}/bootstrap/complete`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          token: data.get("token"),
          username: data.get("username"),
          password: data.get("password"),
          displayName: data.get("displayName"),
          email: data.get("email"),
        }),
      });
      await startOIDC("local");
      await onReady();
    } catch (cause) {
      setSetupError((cause as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <main className="login-shell">
      <section className="login-card">
        <header className="login-top">
          <div className="brand">
            <span className="brand-mark">KL</span>
            {text.brand}
          </div>
          <button
            className="locale-button"
            onClick={() => setLocale(locale === "zh-CN" ? "en-US" : "zh-CN")}
          >
            <Languages size={15} />
            {locale === "zh-CN" ? "EN" : "中文"}
          </button>
        </header>
        <div className="login-heading">
          <h1>{setup ? text.initialize : text.login}</h1>
          <p>{text.loginHint}</p>
        </div>
        {(error || setupError) && <Notice>{error || setupError}</Notice>}
        {setup ? (
          <form className="form-grid" onSubmit={complete}>
            <label className="full">
              {text.bootstrapToken}
              <input name="token" required autoFocus />
            </label>
            <label>
              {text.username}
              <input name="username" required />
            </label>
            <label>
              {text.displayName}
              <input name="displayName" required />
            </label>
            <label>
              {text.email}
              <input name="email" type="email" />
            </label>
            <label>
              {text.password}
              <input name="password" type="password" minLength={12} required />
            </label>
            <div className="panel-actions full">
              <Button type="button" onClick={() => setSetup(false)}>
                Cancel
              </Button>
              <Button type="submit" kind="primary" busy={busy}>
                {text.complete}
              </Button>
            </div>
          </form>
        ) : (
          <>
            <div className="provider-list">
              {methods.map((method) => (
                <button
                  key={method.id}
                  onClick={() => void startOIDC(method.id)}
                >
                  <span className="provider-icon">
                    <KeyRound size={16} />
                  </span>
                  <span>
                    <strong>{method.displayName}</strong>
                    <small>Authorization Code + PKCE S256</small>
                  </span>
                  <ChevronRight size={16} />
                </button>
              ))}
              {!methods.length && (
                <p className="provider-empty">{text.noMethods}</p>
              )}
            </div>
            <Button onClick={() => setSetup(true)}>{text.initialize}</Button>
          </>
        )}
      </section>
    </main>
  );
}

function Shell({
  locale,
  setLocale,
  auth,
  view,
  menu,
  setMenu,
  organizations,
  organizationId,
  onView,
  onLogout,
}: {
  locale: Locale;
  setLocale: (value: Locale) => void;
  auth: AuthState;
  view: ViewKey;
  menu: boolean;
  setMenu: (value: boolean) => void;
  organizations: Organization[];
  organizationId: string;
  onView: (value: ViewKey) => void;
  onLogout: () => Promise<void>;
}) {
  const text = labels[locale];
  return (
    <div className="app-shell">
      <aside className={`sidebar ${menu ? "open" : ""}`}>
        <div className="sidebar-brand">
          <div className="brand">
            <span className="brand-mark">KL</span>
            <span>
              KubeLoop<small>IAM CONSOLE</small>
            </span>
          </div>
          <button
            className="icon-button mobile-only"
            aria-label={
              locale === "zh-CN" ? "关闭导航菜单" : "Close navigation menu"
            }
            onClick={() => setMenu(false)}
          >
            <X size={18} />
          </button>
        </div>
        <nav>
          {nav.map((group) => (
            <div className="nav-group" key={group.section}>
              <span>{text.sections[group.section]}</span>
              {group.items.map(([key, label]) => (
                <button
                  key={key}
                  className={view === key ? "active" : ""}
                  onClick={() => onView(key)}
                >
                  {iconFor(key)}
                  {label}
                  {view === key && <i />}
                </button>
              ))}
            </div>
          ))}
        </nav>
        <div className="sidebar-footer">
          <button onClick={() => void onLogout()}>
            <LogOut size={16} />
            {text.signOut}
          </button>
        </div>
      </aside>
      {menu && (
        <button
          className="menu-scrim"
          aria-label={
            locale === "zh-CN" ? "收起导航菜单" : "Dismiss navigation menu"
          }
          onClick={() => setMenu(false)}
        />
      )}
      <div className="workspace">
        <header className="topbar">
          <button
            className="icon-button menu-button"
            aria-label={
              locale === "zh-CN" ? "打开导航菜单" : "Open navigation menu"
            }
            onClick={() => setMenu(true)}
          >
            <Menu size={18} />
          </button>
          <div className="breadcrumb">
            <span>KubeLoop</span>
            <ChevronRight size={14} />
            <strong>{view}</strong>
          </div>
          <div className="top-actions">
            {organizations[0] && <span>{organizations[0].name}</span>}
            <span className="auth-badge">
              <span />
              {auth.bootstrap?.identity.displayName ||
                auth.bootstrap?.identity.id.slice(0, 8)}
            </span>
            <button
              className="locale-button"
              onClick={() => setLocale(locale === "zh-CN" ? "en-US" : "zh-CN")}
            >
              <Languages size={14} />
              {locale === "zh-CN" ? "EN" : "中"}
            </button>
          </div>
        </header>
        <main className="page">
          <Page
            view={view}
            organizationId={organizationId}
          />
        </main>
      </div>
    </div>
  );
}
function Page({ view, organizationId }: { view: ViewKey; organizationId: string }) {
  if (view === "overview") return <OverviewPage />;
  if (view === "users") return <UsersPage organizationId={organizationId} />;
  if (view === "groups") return <GroupsPage organizationId={organizationId} />;
  if (view === "invitations")
    return <InvitationsPage organizationId={organizationId} />;
  if (view === "oauthClients")
    return <OAuthClientsPage organizationId={organizationId} />;
  if (view === "policies") return <PoliciesPage />;
  if (view === "sessions") return <SessionsPage />;
  if (view === "grants") return <GrantsPage />;
  if (view === "audit") return <AuditPage />;
  return <RuntimePage />;
}
function iconFor(view: ViewKey) {
  const props = { size: 16 };
  if (view === "overview") return <LayoutDashboard {...props} />;
  if (["users", "groups", "invitations"].includes(view))
    return <UsersRound {...props} />;
  if (view === "oauthClients") return <AppWindow {...props} />;
  if (["policies", "sessions", "grants"].includes(view))
    return <KeyRound {...props} />;
  if (view === "audit") return <FileClock {...props} />;
  if (view === "runtime") return <Activity {...props} />;
  return <CircleUserRound {...props} />;
}
