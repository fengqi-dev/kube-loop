import React, { useMemo, useState } from "react";
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
  const providers = query.getAll("provider").map((value) => {
    const [id, name] = value.split("\0");
    return { id, name: name || id };
  });
  const [provider, setProvider] = useState("local");
  const text =
    locale === "zh-CN"
      ? {
          title: session ? "确认授权" : "登录并授权",
          hint: `${client} 正在请求访问 KubeLoop`,
          user: "用户名",
          password: "密码",
          mfa: "MFA 或恢复码",
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
          mfa: "MFA or recovery code",
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
  if (!session && provider !== "local")
    return (
      <main>
        <section className="card">
          <header>
            <div className="brand">
              <span>KL</span>KubeLoop
            </div>
          </header>
          <div className="icon">
            <ShieldCheck />
          </div>
          <h1>{text.title}</h1>
          <p>{text.hint}</p>
          <form method="post" action="/oauth2/login/provider">
            <input type="hidden" name="transaction" value={transaction} />
            <input type="hidden" name="csrf" value={csrf} />
            <label>
              Provider
              <select
                name="provider"
                value={provider}
                onChange={(event) => setProvider(event.target.value)}
              >
                <option value="local">Local account</option>
                {providers.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                  </option>
                ))}
              </select>
            </label>
            <div className="actions">
              <button
                type="button"
                className="secondary"
                onClick={() => setProvider("local")}
              >
                {text.cancel}
              </button>
              <button className="primary">{text.allow}</button>
            </div>
          </form>
        </section>
      </main>
    );
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
        {errorMessage && <div className="error" role="alert">{errorMessage}</div>}
        <form method="post" action="/oauth2/login/local">
          <input type="hidden" name="transaction" value={transaction} />
          <input type="hidden" name="csrf" value={csrf} />
          <input type="hidden" name="session" value={String(session)} />
          <input type="hidden" name="return_to" value={location.search} />
          {!session && (
            <>
              <label>
                Provider
                <select
                  value={provider}
                  onChange={(event) => setProvider(event.target.value)}
                >
                  <option value="local">Local account</option>
                  {providers.map((item) => (
                    <option key={item.id} value={item.id}>
                      {item.name}
                    </option>
                  ))}
                </select>
              </label>
              {provider === "local" && (
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
                  <label>
                    {text.mfa}
                    <input name="second_factor" autoComplete="one-time-code" />
                  </label>
                </>
              )}
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
            {provider === "local" ? (
              <button className="primary" name="decision" value="allow">
                {text.allow}
              </button>
            ) : (
              <button
                className="primary"
                type="button"
                onClick={() => document.forms[0]?.requestSubmit()}
              >
                {text.allow}
              </button>
            )}
          </div>
        </form>
      </section>
    </main>
  );
}
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
