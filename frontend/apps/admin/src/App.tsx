import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ChevronRight,
  CircleUserRound,
  KeyRound,
  Languages,
  LogOut,
  Menu,
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
import { Loading, Notice } from "./components";
import {
  detectLocale,
  I18nContext,
  localeStorageKey,
  Locale,
  messages,
} from "./i18n";
import type { Bootstrap, ViewKey } from "./types";
import { OAuthClientsPage, UsersPage } from "./pages/iam";

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
    signOut: "退出登录",
    noMethods: "没有可用的登录方式。",
    sections: {
      directory: "目录",
      applications: "应用",
    },
  },
  "en-US": {
    brand: "KubeLoop IAM",
    login: "Sign in to Admin",
    loginHint:
      "Use your KubeLoop local account. The browser stores only language and session CSRF.",
    signOut: "Sign out",
    noMethods: "No sign-in method is available.",
    sections: {
      directory: "Directory",
      applications: "Applications",
    },
  },
} as const;
const nav: Array<{
  section: keyof (typeof labels)["zh-CN"]["sections"];
  items: Array<[ViewKey, string]>;
}> = [
  { section: "directory", items: [["users", "用户 / Users"]] },
  { section: "applications", items: [["oauthClients", "OIDC Clients"]] },
];
function currentView(): ViewKey {
  const value = location.hash.replace(/^#\/?/, "").split("/")[0] as ViewKey;
  return nav.some((group) => group.items.some(([key]) => key === value))
    ? value
    : "users";
}

export default function App() {
  const [locale, setLocaleState] = useState<Locale>(detectLocale),
    [auth, setAuth] = useState<AuthState>({ status: "loading" }),
    [view, setView] = useState<ViewKey>(currentView),
    [menu, setMenu] = useState(false);
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
  const context = useMemo(() => ({ locale, setLocale, t }), [locale, t]);
  return (
    <I18nContext.Provider value={context}>
      {auth.status === "loading" ? (
        <main className="boot">
          <div className="brand-mark">KL</div>
          <Loading />
        </main>
      ) : auth.status === "login" ? (
        <Login locale={locale} setLocale={setLocale} error={auth.error} />
      ) : (
        <Shell
          locale={locale}
          setLocale={setLocale}
          auth={auth}
          view={view}
          menu={menu}
          setMenu={setMenu}
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
}: {
  locale: Locale;
  setLocale: (value: Locale) => void;
  error?: string;
}) {
  const text = labels[locale],
    [methods, setMethods] = useState<
      Array<{ id: string; displayName: string }>
    >([]);
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
          <h1>{text.login}</h1>
          <p>{text.loginHint}</p>
        </div>
        {error && <Notice>{error}</Notice>}
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
  onView,
  onLogout,
}: {
  locale: Locale;
  setLocale: (value: Locale) => void;
  auth: AuthState;
  view: ViewKey;
  menu: boolean;
  setMenu: (value: boolean) => void;
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
          <Page view={view} />
        </main>
      </div>
    </div>
  );
}
function Page({ view }: { view: ViewKey }) {
  return view === "oauthClients" ? <OAuthClientsPage /> : <UsersPage />;
}
function iconFor(view: ViewKey) {
  const props = { size: 16 };
  return view === "oauthClients" ? <KeyRound {...props} /> : <CircleUserRound {...props} />;
}
