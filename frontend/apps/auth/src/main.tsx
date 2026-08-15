import React, { FormEvent, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import { KeyRound, ShieldCheck } from "lucide-react";
import { authenticationError } from "./auth-error";
import "./styles.css";

type Locale = "zh-CN" | "en-US";

function App() {
  const query = useMemo(() => new URLSearchParams(location.search), []);
  const initial = (localStorage.getItem("kubeloop.locale") || "") as Locale;
  const [locale, setLocale] = useState<Locale>(
    initial === "en-US" || initial === "zh-CN"
      ? initial
      : navigator.language.startsWith("zh")
        ? "zh-CN"
        : "en-US",
  );
  const session = query.get("session") === "true",
    consent = query.get("consent") === "true",
    scopes = (query.get("scope") || "").split(/\s+/).filter(Boolean),
    client = query.get("client") || "OAuth client";
  if (query.has("invitation"))
    return (
      <InvitationActivation
        locale={locale}
        token={query.get("invitation") || ""}
        onLocale={setLocale}
      />
    );
  const text =
    locale === "zh-CN"
      ? {
          title: session ? "确认授权" : "登录并授权",
          hint: `${client} 正在请求访问 KubeLoop`,
          user: "用户名",
          password: "密码",
          allow: consent ? "允许" : "继续",
          cancel: "取消",
          scope: "请求的权限",
          risk: "仅在你信任此应用时继续。",
        }
      : {
          title: session ? "Confirm authorization" : "Sign in and authorize",
          hint: `${client} is requesting access to KubeLoop`,
          user: "Username",
          password: "Password",
          allow: consent ? "Allow" : "Continue",
          cancel: "Cancel",
          scope: "Requested permissions",
          risk: "Continue only if you trust this application.",
        };
  const choose = (next: Locale) => {
    localStorage.setItem("kubeloop.locale", next);
    setLocale(next);
  };
  const transaction = query.get("transaction") || "",
    csrf = query.get("csrf") || "";
  const errorMessage = authenticationError(locale, query.get("error"));
  return (
    <main>
      <section className="card">
        <header>
          <div className="brand">
            <span>KL</span>KubeLoop
          </div>
          <button
            className="locale"
            onClick={() => choose(locale === "zh-CN" ? "en-US" : "zh-CN")}
          >
            {locale === "zh-CN" ? "EN" : "中"}
          </button>
        </header>
        <div className="icon">
          <ShieldCheck />
        </div>
        <h1>{text.title}</h1>
        <p>{text.hint}</p>
        {errorMessage && (
          <div className="error" role="alert">
            {errorMessage}
          </div>
        )}
        <form method="post" action="/oauth2/login/local">
          <input type="hidden" name="transaction" value={transaction} />
          <input type="hidden" name="csrf" value={csrf} />
          <input type="hidden" name="session" value={String(session)} />
          <input type="hidden" name="return_to" value={location.search} />
          {!session && (
            <>
              <>
                <label>
                  {text.user}
                  <input
                    name="username"
                    autoComplete="username"
                    required
                    autoFocus
                  />
                </label>
                <label>
                  {text.password}
                  <input
                    type="password"
                    name="password"
                    autoComplete="current-password"
                    required
                  />
                </label>
              </>
            </>
          )}
          {consent && (
            <section className="consent">
              <strong>{text.scope}</strong>
              {scopes.map((scope) => (
                <div key={scope}>
                  <KeyRound />
                  <code>{scope}</code>
                </div>
              ))}
              <small>{text.risk}</small>
            </section>
          )}
          <div className="actions">
            <button
              className="secondary"
              name="decision"
              value="cancel"
              formNoValidate
            >
              {text.cancel}
            </button>
            <button className="primary" name="decision" value="allow">
              {text.allow}
            </button>
          </div>
        </form>
      </section>
    </main>
  );
}

function InvitationActivation({
  locale,
  token,
  onLocale,
}: {
  locale: Locale;
  token: string;
  onLocale: (value: Locale) => void;
}) {
  const zh = locale === "zh-CN",
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [complete, setComplete] = useState(false);
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    const data = new FormData(event.currentTarget);
    try {
      const response = await fetch("/api/admin/invitations/accept", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          token,
          username: data.get("username"),
          displayName: data.get("displayName"),
          password: data.get("password"),
        }),
      });
      if (!response.ok) {
        const body = (await response.json().catch(() => null)) as {
          error?: { message?: string };
        } | null;
        throw new Error(
          body?.error?.message ||
            `${zh ? "邀请激活失败" : "Invitation activation failed"} (${response.status})`,
        );
      }
      setComplete(true);
    } catch (cause) {
      setError((cause as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <main>
      <section className="card">
        <header>
          <div className="brand">
            <span>KL</span>KubeLoop
          </div>
          <button
            className="locale"
            onClick={() => {
              const next = locale === "zh-CN" ? "en-US" : "zh-CN";
              localStorage.setItem("kubeloop.locale", next);
              onLocale(next);
            }}
          >
            {locale === "zh-CN" ? "EN" : "中"}
          </button>
        </header>
        <div className="icon">
          <KeyRound />
        </div>
        <h1>{zh ? "激活 KubeLoop 账号" : "Activate your KubeLoop account"}</h1>
        {complete ? (
          <>
            <p>
              {zh
                ? "账号已创建，现在可以返回应用登录。"
                : "Your account is ready. Return to the application to sign in."}
            </p>
            <a className="primary" href="/oauth2/ui/">
              {zh ? "前往登录" : "Continue to sign in"}
            </a>
          </>
        ) : (
          <form onSubmit={submit}>
            {error && (
              <div className="error" role="alert">
                {error}
              </div>
            )}
            <label>
              {zh ? "用户名" : "Username"}
              <input
                name="username"
                autoComplete="username"
                required
                autoFocus
              />
            </label>
            <label>
              {zh ? "显示名称" : "Display name"}
              <input name="displayName" autoComplete="name" required />
            </label>
            <label>
              {zh ? "初始密码" : "Initial password"}
              <input
                name="password"
                type="password"
                autoComplete="new-password"
                minLength={12}
                required
              />
            </label>
            <div className="actions">
              <button className="primary" disabled={busy}>
                {busy
                  ? zh
                    ? "正在激活…"
                    : "Activating…"
                  : zh
                    ? "激活账号"
                    : "Activate account"}
              </button>
            </div>
          </form>
        )}
      </section>
    </main>
  );
}
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
