"use strict";

const managementBase = "/api/v2/admin";
const authBase = "/auth";
const csrfStorageKey = "kubeloop.admin.csrf";
const oidcStorageKey = "kubeloop.admin.oidc";
const deviceStorageKey = "kubeloop.admin.device";

const app = document.getElementById("app");
const state = {
  capabilities: [],
  namespaceScopes: [],
  authenticationType: "",
  activeView: "overview",
  cursor: "",
  selectedNamespace: "",
  loading: false,
};

const views = {
  overview: { title: "运行概览", description: "Controller、存储与管理策略状态。" },
  principals: { title: "身份", description: "已通过 OIDC 或 AD 解析的 Principal。", capability: "admin.principal/list", path: "/principals" },
  sessions: { title: "会话", description: "Cluster Session 生命周期与网络摘要。", capability: "admin.session/list", path: "/sessions", scoped: true },
  tasks: { title: "任务", description: "Preview、Exchange、Mirror、Port Forward 等远程任务。", capability: "admin.task/list", path: "/tasks", scoped: true },
  relays: { title: "Relay", description: "Data Plane 健康、容量和租约状态。", capability: "admin.relay/list", path: "/relays" },
  audit: { title: "审计", description: "不含请求 Secret 和业务 Payload 的管理事件。", capability: "admin.audit/list", path: "/audit" },
};

const columns = {
  principals: [["displayName", "名称"], ["email", "邮箱"], ["provider", "Provider"], ["groups", "Groups"], ["createdAt", "创建时间"]],
  sessions: [["id", "Session"], ["namespace", "Namespace"], ["clusterId", "Cluster"], ["state", "状态"], ["generation", "Generation"], ["networkSpecSha256", "NetworkSpec SHA-256"], ["lastHeartbeatAt", "最后心跳"], ["expiresAt", "过期时间"]],
  tasks: [["id", "Task"], ["sessionId", "Session"], ["type", "类型"], ["state", "状态"], ["createdAt", "创建时间"], ["updatedAt", "更新时间"]],
  relays: [["relayId", "Relay"], ["state", "状态"], ["desiredState", "期望状态"], ["online", "在线"], ["capacity", "容量"], ["reservations", "预留"], ["lastHeartbeatAt", "最后心跳"], ["leaseExpiresAt", "租约过期"]],
  audit: [["createdAt", "时间"], ["action", "动作"], ["outcome", "结果"], ["principalId", "Principal"], ["resourceType", "资源"], ["resourceId", "资源 ID"], ["requestId", "Request ID"]],
};

function text(tag, value, className) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  node.textContent = value == null ? "" : String(value);
  return node;
}

function randomValue(bytes = 32) {
  const buffer = new Uint8Array(bytes);
  crypto.getRandomValues(buffer);
  return base64url(buffer);
}

function base64url(bytes) {
  let binary = "";
  bytes.forEach((value) => { binary += String.fromCharCode(value); });
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
}

async function pkceChallenge(verifier) {
  return base64url(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier))));
}

function deviceID() {
  let value = sessionStorage.getItem(deviceStorageKey);
  if (!value) {
    value = crypto.randomUUID();
    sessionStorage.setItem(deviceStorageKey, value);
  }
  return value;
}

async function requestJSON(path, options = {}) {
  const response = await fetch(path, {
    credentials: "same-origin",
    cache: "no-store",
    ...options,
    headers: { Accept: "application/json", ...(options.headers || {}) },
  });
  let body = null;
  if (response.status !== 204) {
    try { body = await response.json(); } catch { body = null; }
  }
  if (!response.ok) {
    const error = new Error(body?.error?.message || body?.message || `请求失败 (${response.status})`);
    error.status = response.status;
    error.code = body?.error?.code || body?.code || "REQUEST_FAILED";
    throw error;
  }
  if (response.status !== 204 && body === null) {
    throw new Error("服务返回了无法解析的响应。");
  }
  return body;
}

async function exchangeManagement(tokens) {
  try {
    const issued = await requestJSON(`${managementBase}/sessions/token`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${tokens.accessToken}` },
      body: "{}",
    });
    sessionStorage.setItem(csrfStorageKey, issued.csrfToken);
  } finally {
    tokens.accessToken = "";
    tokens.refreshToken = "";
  }
}

async function finishOIDCCallback() {
  const query = new URLSearchParams(location.search);
  const code = query.get("code");
  const returnedState = query.get("state");
  if (!code && !returnedState) return false;
  const storedRaw = sessionStorage.getItem(oidcStorageKey);
  sessionStorage.removeItem(oidcStorageKey);
  history.replaceState({}, "", `${managementBase}/ui`);
  if (!storedRaw) throw new Error("OIDC 登录状态已丢失，请重新登录。");
  const stored = JSON.parse(storedRaw);
  if (!returnedState || returnedState !== stored.state || !stored.verifier || !stored.deviceId) {
    throw new Error("OIDC 登录状态校验失败。");
  }
  const tokens = await requestJSON(`${authBase}/token/exchange`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code, pkceVerifier: stored.verifier, deviceId: stored.deviceId }),
  });
  await exchangeManagement(tokens);
  return true;
}

async function startOIDC(providerID) {
  setLoginBusy(true);
  try {
    const verifier = randomValue();
    const transaction = {
      verifier,
      state: randomValue(),
      nonce: randomValue(),
      deviceId: deviceID(),
    };
    sessionStorage.setItem(oidcStorageKey, JSON.stringify(transaction));
    const result = await requestJSON(`${authBase}/oidc/${encodeURIComponent(providerID)}/start`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        clientCallback: `${location.origin}${managementBase}/ui/callback`,
        state: transaction.state,
        nonce: transaction.nonce,
        pkceChallenge: await pkceChallenge(verifier),
      }),
    });
    location.assign(result.authorizationUrl);
  } catch (error) {
    sessionStorage.removeItem(oidcStorageKey);
    showLoginMessage(error.message);
    setLoginBusy(false);
  }
}

async function loginAD(providerID, username, password) {
  const tokens = await requestJSON(`${authBase}/ad/${encodeURIComponent(providerID)}/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password, deviceId: deviceID() }),
  });
  await exchangeManagement(tokens);
}

async function loginBreakGlass(credential) {
  const issued = await requestJSON(`${managementBase}/sessions/break-glass`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ credential }),
  });
  sessionStorage.setItem(csrfStorageKey, issued.csrfToken);
}

function setLoginBusy(busy) {
  document.querySelectorAll(".login-card button, .login-card input, .login-card select").forEach((node) => { node.disabled = busy; });
}

function showLoginMessage(message) {
  const target = document.getElementById("login-message");
  if (!target) return;
  target.textContent = message;
  target.classList.remove("hidden");
}

async function renderLogin(errorMessage = "") {
  app.className = "";
  app.innerHTML = `
    <main class="login-shell">
      <section class="login-hero" aria-labelledby="login-title">
        <div class="brand"><span class="brand-mark" aria-hidden="true">KL</span><span>KubeLoop Control</span></div>
        <div class="hero-copy"><p class="eyebrow">Gateway management plane</p><h1 id="login-title">集群连接，<br>集中治理。</h1><p>身份、会话、任务、Relay 与审计统一由 Controller 管理。浏览器不会保存 Access Token 或 Refresh Token。</p></div>
        <p class="trust-note">同源 · HttpOnly Session · 严格 CSP · Namespace 委派</p>
      </section>
      <section class="login-pane">
        <div class="login-card">
          <p class="eyebrow">安全登录</p><h2>进入管理后台</h2>
          <p class="subtle">使用 Gateway 已配置的 OIDC / AD，或在紧急情况下使用 break-glass。</p>
          <div id="auth-options" class="auth-options"></div>
          <form id="ad-form" class="auth-form hidden">
            <label>目录 <select id="ad-provider"></select></label>
            <label>用户名 <input id="ad-username" autocomplete="username" required></label>
            <label>密码 <input id="ad-password" type="password" autocomplete="current-password" required></label>
            <button class="primary" type="submit">使用 AD 登录</button>
          </form>
          <form id="breakglass-form" class="auth-form">
            <label>Break-glass 凭据 <input id="breakglass-credential" type="password" autocomplete="off" required></label>
            <button class="secondary" type="submit">紧急访问</button>
          </form>
          <p id="login-message" class="message hidden" role="alert"></p>
        </div>
      </section>
    </main>`;
  if (errorMessage) showLoginMessage(errorMessage);

  let discovery;
  try { discovery = await requestJSON("/.well-known/kubeloop"); } catch { discovery = { authMethods: [] }; }
  const methods = Array.isArray(discovery.authMethods) ? discovery.authMethods : [];
  const options = document.getElementById("auth-options");
  const oidc = methods.filter((method) => method.type === "oidc" && method.interaction === "browser");
  const ad = methods.filter((method) => method.type === "ad" && method.interaction === "password");
  oidc.forEach((method) => {
    const button = text("button", `使用 ${method.displayName || method.id} 登录`, "auth-button");
    button.type = "button";
    button.addEventListener("click", () => startOIDC(method.id));
    options.append(button);
  });
  if (ad.length) {
    const form = document.getElementById("ad-form");
    const provider = document.getElementById("ad-provider");
    ad.forEach((method) => {
      const option = text("option", method.displayName || method.id);
      option.value = method.id;
      provider.append(option);
    });
    form.classList.remove("hidden");
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      const passwordInput = document.getElementById("ad-password");
      const password = passwordInput.value;
      passwordInput.value = "";
      setLoginBusy(true);
      try {
        await loginAD(provider.value, document.getElementById("ad-username").value, password);
        await startApplication();
      } catch (error) { showLoginMessage(error.message); setLoginBusy(false); }
    });
  }
  document.getElementById("breakglass-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const input = document.getElementById("breakglass-credential");
    const credential = input.value;
    input.value = "";
    setLoginBusy(true);
    try { await loginBreakGlass(credential); await startApplication(); }
    catch (error) { showLoginMessage(error.message); setLoginBusy(false); }
  });
}

function capabilityAllowed(capability) {
  return state.capabilities.includes(capability) || state.namespaceScopes.some((scope) => scope.capabilities?.includes(capability));
}

function allowedViews() {
  return Object.entries(views).filter(([key, view]) => key === "overview" || !view.capability || capabilityAllowed(view.capability));
}

function renderShell() {
  app.className = "";
  app.innerHTML = `
    <div class="shell">
      <aside class="sidebar">
        <div class="brand"><span class="brand-mark" aria-hidden="true">KL</span><span>KubeLoop Control</span></div>
        <nav id="nav" class="nav" aria-label="管理导航"></nav>
        <div class="sidebar-foot"><span>Policy revision <strong id="policy-revision"></strong></span><button id="logout" class="ghost" type="button">退出管理会话</button></div>
      </aside>
      <main class="workspace">
        <header class="topbar"><h1 id="page-title"></h1><span id="auth-badge" class="session-badge"></span></header>
        <section id="content" class="content" aria-live="polite"></section>
      </main>
    </div>`;
  document.getElementById("policy-revision").textContent = String(state.policyRevision || 0);
  document.getElementById("auth-badge").textContent = state.authenticationType || "authenticated";
  const nav = document.getElementById("nav");
  allowedViews().forEach(([key, view]) => {
    const button = text("button", view.title);
    button.type = "button";
    button.dataset.view = key;
    button.addEventListener("click", () => openView(key));
    nav.append(button);
  });
  document.getElementById("logout").addEventListener("click", logout);
}

async function logout() {
  const csrf = sessionStorage.getItem(csrfStorageKey) || "";
  try {
    await requestJSON(`${managementBase}/sessions/current`, { method: "DELETE", headers: { "X-KubeLoop-CSRF": csrf } });
  } catch (error) {
    if (error.status !== 401) {
      renderError(error.message);
      return;
    }
  }
  sessionStorage.removeItem(csrfStorageKey);
  await renderLogin();
}

function namespaceOptions() {
  return state.namespaceScopes.map((scope) => scope.namespace).filter(Boolean).sort();
}

function openView(key) {
  if (!views[key]) return;
  state.activeView = key;
  state.cursor = "";
  document.querySelectorAll("#nav button").forEach((button) => button.classList.toggle("active", button.dataset.view === key));
  document.getElementById("page-title").textContent = views[key].title;
  if (key === "overview") loadOverview(); else loadList();
}

function renderToolbar(view) {
  const wrapper = document.createElement("div");
  wrapper.className = "toolbar";
  const copy = document.createElement("div"); copy.className = "toolbar-copy";
  copy.append(text("p", view.description));
  const actions = document.createElement("div"); actions.className = "toolbar-actions";
  if (view.scoped && namespaceOptions().length) {
    const label = text("label", "Namespace");
    const select = document.createElement("select"); select.id = "namespace-filter";
    namespaceOptions().forEach((namespace) => {
      const option = text("option", namespace); option.value = namespace; select.append(option);
    });
    if (!state.selectedNamespace || !namespaceOptions().includes(state.selectedNamespace)) state.selectedNamespace = namespaceOptions()[0];
    select.value = state.selectedNamespace;
    select.addEventListener("change", () => { state.selectedNamespace = select.value; state.cursor = ""; loadList(); });
    label.append(select); actions.append(label);
  }
  const refresh = text("button", "刷新", "secondary"); refresh.type = "button"; refresh.addEventListener("click", () => { state.cursor = ""; loadList(); });
  actions.append(refresh); wrapper.append(copy, actions); return wrapper;
}

async function loadOverview() {
  const content = document.getElementById("content");
  content.replaceChildren();
  const toolbar = document.createElement("div"); toolbar.className = "toolbar";
  const copy = document.createElement("div"); copy.className = "toolbar-copy";
  copy.append(text("p", views.overview.description)); toolbar.append(copy); content.append(toolbar);
  const grid = document.createElement("div"); grid.className = "grid"; content.append(grid);
  const cards = [
    ["认证方式", state.authenticationType || "—"],
    ["集群能力", state.capabilities.length],
    ["Namespace 委派", state.namespaceScopes.length],
  ];
  cards.forEach(([label, value]) => {
    const card = document.createElement("article"); card.className = "card";
    card.append(text("div", label, "metric-label"), text("div", value, "metric")); grid.append(card);
  });
  if (!state.capabilities.includes("admin.status/read")) {
    const card = document.createElement("article"); card.className = "card full";
    card.append(text("p", "当前角色仅具有 namespace 范围权限；系统状态不会向该会话公开。", "subtle")); grid.append(card); return;
  }
  try {
    const status = await requestJSON(`${managementBase}/status`);
    const card = document.createElement("article"); card.className = "card full";
    [["Controller", `${status.controller.version} (${status.controller.commit})`], ["Protocol", `${status.controller.protocolMin} – ${status.controller.protocolMax}`], ["Storage", `${status.storage.backend} · schema ${status.storage.schemaVersion}`], ["Policy", `revision ${status.managementPolicy.revision} · ETag ${status.managementPolicy.etag}`]].forEach(([label, value]) => {
      const row = document.createElement("div"); row.className = "status-row";
      const left = document.createElement("span"); left.append(text("span", "", "dot"), document.createTextNode(` ${label}`));
      row.append(left, text("strong", value)); card.append(row);
    });
    grid.append(card);
  } catch (error) { renderError(error.message); }
}

function displayValue(key, value) {
  if (value == null || value === "") return "—";
  if (Array.isArray(value)) return value.join(", ") || "—";
  if (typeof value === "object") {
    if (key === "capacity") return `${value.activeLogicalStreams || 0}/${value.maximumLogicalStreams || 0} streams · ${value.activePhysicalConnections || 0}/${value.maximumPhysicalConnections || 0} connections`;
    return JSON.stringify(value);
  }
  if (key.endsWith("At")) {
    const parsed = new Date(value);
    if (!Number.isNaN(parsed.getTime())) return parsed.toLocaleString();
  }
  if (typeof value === "boolean") return value ? "是" : "否";
  return String(value);
}

async function loadList() {
  const view = views[state.activeView];
  const content = document.getElementById("content"); content.replaceChildren(renderToolbar(view));
  const loading = document.createElement("div"); loading.className = "empty"; loading.textContent = "正在加载…"; content.append(loading);
  const query = new URLSearchParams({ limit: "50" });
  if (state.cursor) query.set("cursor", state.cursor);
  if (view.scoped && state.selectedNamespace) query.set("namespace", state.selectedNamespace);
  try {
    const result = await requestJSON(`${managementBase}${view.path}?${query}`);
    const items = Array.isArray(result.items) ? result.items : [];
    const tableWrap = document.createElement("div"); tableWrap.className = "table-wrap";
    if (!items.length) tableWrap.append(text("div", "没有符合条件的记录。", "empty"));
    else {
      const table = document.createElement("table");
      const head = document.createElement("thead"); const headRow = document.createElement("tr");
      columns[state.activeView].forEach(([, label]) => headRow.append(text("th", label))); head.append(headRow); table.append(head);
      const body = document.createElement("tbody");
      items.forEach((item) => {
        const row = document.createElement("tr");
        columns[state.activeView].forEach(([key]) => {
          const cell = document.createElement("td");
          const value = displayValue(key, item[key]);
          if (key === "state" || key === "desiredState" || key === "outcome") cell.append(text("span", value, `pill ${String(item[key] || "").toLowerCase()}`));
          else cell.textContent = value;
          row.append(cell);
        }); body.append(row);
      });
      table.append(body); tableWrap.append(table);
    }
    content.replaceChildren(renderToolbar(view), tableWrap);
    const pager = document.createElement("div"); pager.className = "pager";
    if (result.nextCursor) {
      const next = text("button", "下一页", "secondary"); next.type = "button";
      next.addEventListener("click", () => { state.cursor = result.nextCursor; loadList(); }); pager.append(next);
    }
    content.append(pager);
  } catch (error) {
    if (error.status === 401) { await renderLogin("管理会话已过期，请重新登录。"); return; }
    renderError(error.message);
  }
}

function renderError(message) {
  const content = document.getElementById("content");
  const panel = text("div", message || "管理服务暂时不可用。", "error-panel"); panel.setAttribute("role", "alert");
  content.replaceChildren(panel);
}

async function startApplication() {
  const capabilities = await requestJSON(`${managementBase}/capabilities`);
  state.capabilities = Array.isArray(capabilities.capabilities) ? capabilities.capabilities : [];
  state.namespaceScopes = Array.isArray(capabilities.namespaceScopes) ? capabilities.namespaceScopes : [];
  state.authenticationType = capabilities.authenticationType || "";
  state.policyRevision = capabilities.policyRevision || 0;
  state.selectedNamespace = namespaceOptions()[0] || "";
  renderShell();
  openView("overview");
}

(async function bootstrap() {
  try {
    await finishOIDCCallback();
    await startApplication();
  } catch (error) {
    if (error.status === 401 || error.status === 403) await renderLogin(error.status === 403 ? "当前身份没有管理权限。" : "");
    else await renderLogin(error.message);
  }
})();
