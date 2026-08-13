"use strict";

const managementBase = document.querySelector('meta[name="kubeloop-management-path"]').content;
const authBase = "/oauth2";
const csrfStorageKey = "kubeloop.admin.csrf";
const oidcStorageKey = "kubeloop.admin.oidc";
const deviceStorageKey = "kubeloop.admin.device";
const policyDraftStorageKey = "kubeloop.admin.policy-draft";
const providerDraftStorageKey = "kubeloop.admin.provider-draft";

const app = document.getElementById("app");
const state = {
  capabilities: [],
  namespaceScopes: [],
  authenticationType: "",
  activeView: "overview",
  cursor: "",
  selectedNamespace: "",
  loading: false,
  policyEtag: 0,
};

const views = {
  overview: { title: "运行概览", description: "Control Plane、存储与管理策略状态。" },
  policy: { title: "管理策略", description: "以 revision、dry-run 和乐观并发安全管理后台角色。", capability: "admin.policy/read" },
  providers: { title: "身份 Provider", description: "验证并版本化发布 OIDC；Secret 只使用部署 allowlist 中的 alias。", capability: "admin.provider/read" },
  users: { title: "用户管理", description: "创建、停用本地管理用户并重置密码。", capability: "admin.user/list" },
  security: { title: "账户安全", description: "为当前本地账户启用 TOTP、保存恢复码或更新 MFA。" },
  principals: { title: "身份", description: "已通过 OIDC 解析的 Principal。", capability: "admin.principal/list", path: "/principals" },
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
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${tokens.access_token}` },
      body: "{}",
    });
    sessionStorage.setItem(csrfStorageKey, issued.csrfToken);
  } finally {
    tokens.access_token = "";
    tokens.refresh_token = "";
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
  const form = new URLSearchParams({
    grant_type: "authorization_code",
    code,
    code_verifier: stored.verifier,
    client_id: "kubeloop-management",
    redirect_uri: `${location.origin}${managementBase}/ui/callback`,
    device_id: stored.deviceId,
  });
  const tokens = await requestJSON(`${authBase}/token`, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: form.toString(),
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
    const authorize = new URL(`${authBase}/authorize`, location.origin);
    authorize.search = new URLSearchParams({
      response_type: "code",
      client_id: "kubeloop-management",
      redirect_uri: `${location.origin}${managementBase}/ui/callback`,
      scope: "openid profile email offline_access kubeloop.api",
      state: transaction.state,
      nonce: transaction.nonce,
      code_challenge: await pkceChallenge(verifier),
      code_challenge_method: "S256",
      provider: providerID,
    }).toString();
    location.assign(authorize.toString());
  } catch (error) {
    sessionStorage.removeItem(oidcStorageKey);
    showLoginMessage(error.message);
    setLoginBusy(false);
  }
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
        <div class="hero-copy"><p class="eyebrow">Gateway management plane</p><h1 id="login-title">集群连接，<br>集中治理。</h1><p>身份、会话、任务、Relay 与审计统一由 Control Plane 管理。浏览器不会保存 Access Token 或 Refresh Token。</p></div>
        <p class="trust-note">同源 · HttpOnly Session · 严格 CSP · Namespace 委派</p>
      </section>
      <section class="login-pane">
        <div class="login-card">
          <p class="eyebrow">安全登录</p><h2>进入管理后台</h2>
          <p class="subtle">使用本地管理员或企业 OIDC 安全登录。</p>
          <div id="auth-options" class="auth-options"></div>
          <p id="login-message" class="message hidden" role="alert"></p>
        </div>
      </section>
    </main>`;
  if (errorMessage) showLoginMessage(errorMessage);

  let discovery;
  try { discovery = await requestJSON("/.well-known/kubeloop"); } catch { discovery = { authMethods: [] }; }
  const methods = Array.isArray(discovery.authMethods) ? discovery.authMethods : [];
  const options = document.getElementById("auth-options");
  const browserMethods = methods.filter((method) =>
    (method.type === "oidc" || method.type === "local") && method.interaction === "browser");
  browserMethods.forEach((method) => {
    const button = text("button", `使用 ${method.displayName || method.id} 登录`, "auth-button");
    button.type = "button";
    button.addEventListener("click", () => startOIDC(method.id));
    options.append(button);
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
  if (key === "overview") loadOverview();
  else if (key === "policy") loadPolicy();
  else if (key === "providers") loadProviders();
  else if (key === "users") loadUsers();
  else if (key === "security") loadSecurity();
  else loadList();
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
  if (state.activeView === "tasks" && capabilityAllowed("admin.task/recover")) {
    const recover = text("button", "触发恢复", "secondary"); recover.type = "button";
    recover.addEventListener("click", () => runRecovery(recover)); actions.append(recover);
  }
  if (state.activeView === "audit" && capabilityAllowed("admin.audit/export")) {
    const exportButton = text("button", "导出 NDJSON", "secondary"); exportButton.type = "button";
    exportButton.addEventListener("click", () => startAuditExport(exportButton)); actions.append(exportButton);
  }
  actions.append(refresh); wrapper.append(copy, actions); return wrapper;
}

function operationReason(message) {
  const reason = window.prompt(message, "planned administrative operation")?.trim() || "";
  if (reason.length < 8 || reason.length > 512 || /[\r\n\0]/u.test(reason)) throw new Error("原因必须为 8–512 个字符且不能换行。");
  return reason;
}

async function operationMutation(path, reason, etag = null) {
  const headers = {
    "Content-Type": "application/json",
    "X-KubeLoop-CSRF": sessionStorage.getItem(csrfStorageKey) || "",
    "Idempotency-Key": randomValue(),
  };
  if (etag !== null) headers["If-Match"] = `"${etag}"`;
  return requestJSON(`${managementBase}${path}`, { method: "POST", headers, body: JSON.stringify({ reason }) });
}

function rowOperation(item) {
  const view = state.activeView;
  let label = ""; let path = ""; let etag = null;
  if (view === "principals" && capabilityAllowed("admin.session/revoke")) {
    label = "撤销设备"; path = `/principals/${encodeURIComponent(item.id)}/revoke`;
  } else if (view === "sessions" && capabilityAllowed("admin.session/stop") && item.state !== "stopped") {
    label = "强制停止"; path = `/sessions/${encodeURIComponent(item.id)}/stop`; etag = item.generation;
  } else if (view === "tasks" && capabilityAllowed("admin.task/stop") && !["stopped", "failed"].includes(item.state)) {
    label = "停止"; path = `/tasks/${encodeURIComponent(item.id)}/stop`; etag = item.version;
  } else if (view === "relays" && item.desiredState === "draining" && capabilityAllowed("admin.relay/recover")) {
    label = "恢复接流"; path = `/relays/${encodeURIComponent(item.relayId)}/recover`; etag = item.controlVersion || 0;
  } else if (view === "relays" && capabilityAllowed("admin.relay/drain")) {
    label = "排空"; path = `/relays/${encodeURIComponent(item.relayId)}/drain`; etag = item.controlVersion || 0;
  }
  if (!label) return null;
  const button = text("button", label, "secondary"); button.type = "button";
  button.addEventListener("click", async () => {
    try {
      button.disabled = true;
      await operationMutation(path, operationReason(`请输入“${label}”原因`), etag);
      state.cursor = ""; await loadList();
    } catch (error) { renderError(error.message); } finally { button.disabled = false; }
  });
  return button;
}

async function runRecovery(button) {
  try {
    button.disabled = true;
    await operationMutation("/tasks/recovery", operationReason("请输入恢复原因"));
    state.cursor = ""; await loadList();
  } catch (error) { renderError(error.message); } finally { button.disabled = false; }
}

async function startAuditExport(button) {
  try {
    button.disabled = true;
    const created = await operationMutation("/audit/exports", operationReason("请输入审计导出原因"));
    await downloadAuditExport(created.jobId);
  } catch (error) { renderError(error.message); } finally { button.disabled = false; }
}

async function downloadAuditExport(jobID) {
  for (let attempt = 0; attempt < 60; attempt += 1) {
    const response = await fetch(`${managementBase}/audit/exports/${encodeURIComponent(jobID)}`, {
      credentials: "same-origin", cache: "no-store", headers: { Accept: "application/x-ndjson, application/json" },
    });
    if (response.status === 202) { await new Promise((resolve) => setTimeout(resolve, 1000)); continue; }
    if (!response.ok) throw new Error(`审计导出失败 (${response.status})`);
    if ((response.headers.get("Content-Type") || "").startsWith("application/x-ndjson")) {
      await response.body?.cancel();
      const link = document.createElement("a"); link.href = `${managementBase}/audit/exports/${encodeURIComponent(jobID)}`;
      link.download = `kubeloop-audit-${jobID}.ndjson`; link.click(); return;
    }
    const status = await response.json(); throw new Error(status.errorCode || "审计导出失败");
  }
  throw new Error("审计导出仍在处理中，请稍后重试。");
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
    [["Control Plane", `${status.controlPlane.version} (${status.controlPlane.commit})`], ["Protocol", `${status.controlPlane.protocolMin} – ${status.controlPlane.protocolMax}`], ["Storage", `${status.storage.backend} · schema ${status.storage.schemaVersion}`], ["Policy", `revision ${status.managementPolicy.revision} · ETag ${status.managementPolicy.etag}`]].forEach(([label, value]) => {
      const row = document.createElement("div"); row.className = "status-row";
      const left = document.createElement("span"); left.append(text("span", "", "dot"), document.createTextNode(` ${label}`));
      row.append(left, text("strong", value)); card.append(row);
    });
    grid.append(card);
  } catch (error) { renderError(error.message); }
}

function policyField(labelText, input) {
  const label = text("label", labelText);
  label.append(input);
  return label;
}

function policyMessage(container, message, kind = "success") {
  const existing = container.querySelector(".policy-message");
  if (existing) existing.remove();
  const notice = text("p", message, `policy-message ${kind}`);
  notice.setAttribute("role", kind === "error" ? "alert" : "status");
  container.prepend(notice);
}

function pendingPolicyDraft() {
  try {
    const value = JSON.parse(sessionStorage.getItem(policyDraftStorageKey) || "null");
    if (!value || typeof value.changeId !== "string" || typeof value.key !== "string" || !Number.isSafeInteger(value.baseEtag)) return null;
    return value;
  } catch { return null; }
}

async function policyMutation(path, etag, key, body) {
  const csrf = sessionStorage.getItem(csrfStorageKey) || "";
  return requestJSON(`${managementBase}${path}`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-KubeLoop-CSRF": csrf,
      "If-Match": `"${etag}"`,
      "Idempotency-Key": key,
    },
    body: JSON.stringify(body),
  });
}

async function loadPolicy() {
  const content = document.getElementById("content");
  content.replaceChildren(text("div", "正在加载管理策略…", "empty"));
  try {
    const current = await requestJSON(`${managementBase}/policy`);
    state.policyEtag = Number(current.etag || 0);
    state.policyRevision = Number(current.revision || 0);
    const revisionLabel = document.getElementById("policy-revision");
    if (revisionLabel) revisionLabel.textContent = String(state.policyRevision);

    const toolbar = document.createElement("div"); toolbar.className = "toolbar";
    const copy = document.createElement("div"); copy.className = "toolbar-copy";
    copy.append(text("p", views.policy.description));
    const refresh = text("button", "刷新", "secondary"); refresh.type = "button"; refresh.addEventListener("click", loadPolicy);
    toolbar.append(copy, refresh);

    const summary = document.createElement("div"); summary.className = "grid policy-summary";
    [["状态", current.active ? "已发布" : "Bootstrap"], ["Revision", current.revision || 0], ["ETag", current.etag || 0]].forEach(([label, value]) => {
      const card = document.createElement("article"); card.className = "card";
      card.append(text("div", label, "metric-label"), text("div", value, "metric")); summary.append(card);
    });

    const panel = document.createElement("section"); panel.className = "policy-panel";
    const heading = document.createElement("div"); heading.className = "policy-heading";
    heading.append(text("div", "候选策略", "metric-label"), text("h2", "角色与 Namespace 委派"));
    panel.append(heading);
    const editor = document.createElement("textarea"); editor.id = "policy-editor"; editor.spellcheck = false;
    editor.value = JSON.stringify(current.spec || { version: 1, assignments: [] }, null, 2);
    const canCreate = capabilityAllowed("admin.policy/create");
    editor.readOnly = !canCreate;
    panel.append(policyField("Policy JSON", editor));
    panel.append(text("p", "固定角色：platform-admin、security-admin、operator、auditor、namespace-admin。只有 namespace-admin 可以声明 namespaces。", "subtle policy-help"));

    const reason = document.createElement("input"); reason.id = "policy-reason"; reason.maxLength = 512;
    reason.placeholder = "说明本次变更目的（至少 8 个字符）";
    panel.append(policyField("变更原因", reason));

    const dryRunGrid = document.createElement("div"); dryRunGrid.className = "policy-check-grid";
    const subject = document.createElement("input"); subject.placeholder = "Principal UUID（留空则只校验策略）";
    const groups = document.createElement("input"); groups.placeholder = "组，使用逗号分隔";
    const resource = document.createElement("select");
    ["policy", "provider", "session", "task", "relay", "audit", "namespace-policy"].forEach((value) => { const option = text("option", value); option.value = value; resource.append(option); });
    const operation = document.createElement("select");
    ["read", "list", "create", "update", "validate", "dry-run", "publish", "rollback", "revoke", "stop"].forEach((value) => { const option = text("option", value); option.value = value; operation.append(option); });
    const namespace = document.createElement("input"); namespace.placeholder = "Namespace（可选）";
    dryRunGrid.append(policyField("Dry-run Principal", subject), policyField("Groups", groups), policyField("Resource", resource), policyField("Operation", operation), policyField("Namespace", namespace));
    if (capabilityAllowed("admin.policy/dry-run")) panel.append(dryRunGrid);

    const actions = document.createElement("div"); actions.className = "policy-actions";
    if (capabilityAllowed("admin.policy/dry-run")) {
      const dryRun = text("button", "校验并 Dry-run", "secondary"); dryRun.type = "button";
      dryRun.addEventListener("click", async () => {
        try {
          const spec = JSON.parse(editor.value);
          const checks = subject.value.trim() ? [{
            subject: { id: subject.value.trim(), groups: groups.value.split(",").map((value) => value.trim()).filter(Boolean) },
            request: { resource: resource.value, operation: operation.value, namespace: namespace.value.trim() },
          }] : [];
          const result = await policyMutation("/policy/dry-run", state.policyEtag, crypto.randomUUID(), {
            spec, checks, reason: reason.value.trim() || "validate candidate policy",
          });
          const decision = result.decisions?.[0];
          const detail = decision ? `${decision.allowed ? "ALLOW" : "DENY"} · ${decision.reason}${decision.role ? ` · ${decision.role}` : ""}` : "策略结构有效";
          policyMessage(panel, `${detail}；${result.publishable ? "包含正式 platform-admin" : "不可发布：缺少正式 platform-admin"}`,
            result.publishable ? "success" : "warning");
        } catch (error) { policyMessage(panel, error.message || "Policy JSON 无法解析。", "error"); }
      });
      actions.append(dryRun);
    }
    if (canCreate) {
      const draft = text("button", "创建 Draft", "primary"); draft.type = "button";
      draft.addEventListener("click", async () => {
        try {
          const spec = JSON.parse(editor.value);
          const key = crypto.randomUUID();
          const result = await policyMutation("/policy/drafts", state.policyEtag, key, { spec, reason: reason.value.trim() });
          const pending = { changeId: result.changeId, revision: result.revision, baseEtag: result.baseEtag, key };
          sessionStorage.setItem(policyDraftStorageKey, JSON.stringify(pending));
          policyMessage(panel, `Draft revision ${result.revision} 已校验；Change ${result.changeId} 等待发布。`);
          renderPendingPolicy(panel, actions, pending, reason);
        } catch (error) { policyMessage(panel, error.message || "创建 Draft 失败。", "error"); }
      });
      actions.append(draft);
    }
    panel.append(actions);
    const pending = pendingPolicyDraft();
    if (pending && pending.baseEtag === state.policyEtag) renderPendingPolicy(panel, actions, pending, reason);
    else if (pending) sessionStorage.removeItem(policyDraftStorageKey);

    if (capabilityAllowed("admin.policy/rollback") && current.active) {
      const rollback = document.createElement("section"); rollback.className = "policy-panel compact";
      rollback.append(text("h2", "回滚到已验证 Revision"));
      const target = document.createElement("input"); target.type = "number"; target.min = "1"; target.placeholder = "目标 revision";
      const rollbackReason = document.createElement("input"); rollbackReason.maxLength = 512; rollbackReason.placeholder = "回滚原因（至少 8 个字符）";
      const rollbackButton = text("button", "执行回滚", "secondary danger-action"); rollbackButton.type = "button";
      rollbackButton.addEventListener("click", async () => {
        try {
          const result = await policyMutation("/policy/rollback", state.policyEtag, crypto.randomUUID(), {
            targetRevision: Number(target.value), reason: rollbackReason.value.trim(),
          });
          sessionStorage.removeItem(policyDraftStorageKey);
          await startApplication();
          policyMessage(document.getElementById("content"), `已回滚到 revision ${result.revision}，新 ETag ${result.etag}。`);
        } catch (error) { policyMessage(rollback, error.message, "error"); }
      });
      const rollbackFields = document.createElement("div"); rollbackFields.className = "policy-rollback";
      rollbackFields.append(policyField("目标 Revision", target), policyField("原因", rollbackReason), rollbackButton); rollback.append(rollbackFields);
      content.replaceChildren(toolbar, summary, panel, rollback); return;
    }
    content.replaceChildren(toolbar, summary, panel);
  } catch (error) {
    if (error.status === 401) { await renderLogin("管理会话已过期，请重新登录。"); return; }
    renderError(error.message);
  }
}

function renderPendingPolicy(panel, actions, pending, reasonInput) {
  if (!capabilityAllowed("admin.policy/publish") || actions.querySelector("[data-publish]")) return;
  const publish = text("button", `发布 Revision ${pending.revision}`, "primary"); publish.type = "button"; publish.dataset.publish = "true";
  publish.addEventListener("click", async () => {
    try {
      const result = await policyMutation(`/policy/changes/${encodeURIComponent(pending.changeId)}/publish`, pending.baseEtag, pending.key, {
        reason: reasonInput.value.trim() || "publish validated policy",
      });
      sessionStorage.removeItem(policyDraftStorageKey);
      await startApplication();
      if (capabilityAllowed("admin.policy/read")) {
        openView("policy");
        const currentContent = document.getElementById("content");
        policyMessage(currentContent, `Revision ${result.revision} 已发布，ETag ${result.etag}。`);
      }
    } catch (error) { policyMessage(panel, error.message, "error"); }
  });
  actions.append(publish);
}

function pendingProviderDraft() {
  try {
    const value = JSON.parse(sessionStorage.getItem(providerDraftStorageKey) || "null");
    if (!value || typeof value.providerId !== "string" || typeof value.changeId !== "string" ||
      typeof value.key !== "string" || !Number.isSafeInteger(value.baseEtag)) return null;
    return value;
  } catch { return null; }
}

function providerDefaults() {
  return {
    issuer: "", clientId: "kubeloop", scopes: ["openid", "profile", "email"],
    allowedSigningAlgs: ["RS256"], requiredClaims: ["sub"], claims: { displayName: "name", email: "email", groups: "groups" },
  };
}

async function loadProviders() {
  const content = document.getElementById("content");
  content.replaceChildren(text("div", "正在加载身份 Provider…", "empty"));
  try {
    const listed = await requestJSON(`${managementBase}/providers`);
    const items = Array.isArray(listed.items) ? listed.items : [];
    const toolbar = document.createElement("div"); toolbar.className = "toolbar";
    const copy = document.createElement("div"); copy.className = "toolbar-copy"; copy.append(text("p", views.providers.description));
    const refresh = text("button", "刷新", "secondary"); refresh.type = "button"; refresh.addEventListener("click", loadProviders);
    toolbar.append(copy, refresh);

    const summary = document.createElement("div"); summary.className = "grid policy-summary";
    [["已发布", items.length], ["OIDC", items.filter((item) => item.type === "oidc").length]].forEach(([label, value]) => {
      const card = document.createElement("article"); card.className = "card";
      card.append(text("div", label, "metric-label"), text("div", value, "metric")); summary.append(card);
    });

    const panel = document.createElement("section"); panel.className = "policy-panel";
    const heading = document.createElement("div"); heading.className = "policy-heading";
    heading.append(text("div", "版本化配置", "metric-label"), text("h2", "OIDC")); panel.append(heading);
    const selector = document.createElement("select");
    const createOption = text("option", "新建 Provider"); createOption.value = ""; selector.append(createOption);
    items.forEach((item) => { const option = text("option", `${item.providerId} · ${item.type} · revision ${item.revision}`); option.value = item.providerId; selector.append(option); });
    const providerID = document.createElement("input"); providerID.placeholder = "稳定 Provider ID"; providerID.maxLength = 128;
    const type = document.createElement("select");
    ["oidc"].forEach((value) => { const option = text("option", value.toUpperCase()); option.value = value; type.append(option); });
    const config = document.createElement("textarea"); config.spellcheck = false; config.value = JSON.stringify(providerDefaults(), null, 2);
    const aliases = document.createElement("textarea"); aliases.spellcheck = false; aliases.value = JSON.stringify({ "client-secret": "" }, null, 2);
    const reason = document.createElement("input"); reason.maxLength = 512; reason.placeholder = "说明本次变更目的（至少 8 个字符）";
    let etag = 0;
    const selectProvider = (id) => {
      const item = items.find((candidate) => candidate.providerId === id);
      providerID.value = item?.providerId || ""; providerID.readOnly = Boolean(item);
      type.value = item?.type || "oidc"; type.disabled = Boolean(item);
      config.value = JSON.stringify(item?.config || providerDefaults(), null, 2);
      aliases.value = JSON.stringify({ "client-secret": "" }, null, 2);
      etag = Number(item?.etag || 0);
      policyMessage(panel, item ? `当前 revision ${item.revision}，ETag ${etag}。请重新选择 Secret alias；服务不会回显 alias 值。` : "填写非敏感配置，并选择 Helm allowlist 中的 Secret alias。", "warning");
    };
    selector.addEventListener("change", () => selectProvider(selector.value));
    type.addEventListener("change", () => {
      if (!selector.value) {
        config.value = JSON.stringify(providerDefaults(), null, 2);
        aliases.value = JSON.stringify({ "client-secret": "" }, null, 2);
      }
    });
    panel.append(policyField("已有 Provider", selector), policyField("Provider ID", providerID), policyField("类型", type),
      policyField("非敏感配置 JSON", config), policyField("Secret alias JSON", aliases),
      text("p", "仅填写 alias，例如 {\"client-secret\":\"corporate\",\"ca\":\"corporate-ca\"}。不要填写 Secret 明文。", "subtle policy-help"),
      policyField("变更原因", reason));
    selectProvider("");

    const actions = document.createElement("div"); actions.className = "policy-actions";
    const candidate = () => ({ type: type.value, config: JSON.parse(config.value), secretAliases: JSON.parse(aliases.value), reason: reason.value.trim() });
    if (capabilityAllowed("admin.provider/validate")) {
      const validate = text("button", "验证连通性", "secondary"); validate.type = "button";
      validate.addEventListener("click", async () => {
        try {
          const id = providerID.value.trim();
          const result = await policyMutation(`/providers/${encodeURIComponent(id)}/validate`, etag, crypto.randomUUID(), candidate());
          policyMessage(panel, `Provider 配置有效；${result.validation?.connectivity || "结构校验通过"}。`);
        } catch (error) { policyMessage(panel, error.message || "Provider 验证失败。", "error"); }
      }); actions.append(validate);
    }
    if (capabilityAllowed("admin.provider/create")) {
      const draft = text("button", "创建 Draft", "primary"); draft.type = "button";
      draft.addEventListener("click", async () => {
        try {
          const id = providerID.value.trim(); const key = crypto.randomUUID();
          const result = await policyMutation(`/providers/${encodeURIComponent(id)}/drafts`, etag, key, candidate());
          const pending = { providerId: id, changeId: result.changeId, revision: result.revision, baseEtag: result.baseEtag, key };
          sessionStorage.setItem(providerDraftStorageKey, JSON.stringify(pending));
          policyMessage(panel, `Draft revision ${result.revision} 已验证，等待发布。`);
          renderPendingProvider(panel, actions, pending, reason);
        } catch (error) { policyMessage(panel, error.message || "创建 Provider Draft 失败。", "error"); }
      }); actions.append(draft);
    }
    panel.append(actions);
    const pending = pendingProviderDraft();
    const pendingCurrent = pending ? items.find((item) => item.providerId === pending.providerId) : null;
    if (pending && ((pendingCurrent && Number(pendingCurrent.etag) === pending.baseEtag) || (!pendingCurrent && pending.baseEtag === 0))) {
      renderPendingProvider(panel, actions, pending, reason);
    } else if (pending) sessionStorage.removeItem(providerDraftStorageKey);

    if (capabilityAllowed("admin.provider/rollback")) {
      const rollback = document.createElement("section"); rollback.className = "policy-panel compact";
      rollback.append(text("h2", "回滚 Provider Revision"));
      const rollbackID = document.createElement("input"); rollbackID.placeholder = "Provider ID";
      const target = document.createElement("input"); target.type = "number"; target.min = "1"; target.placeholder = "目标 revision";
      const rollbackReason = document.createElement("input"); rollbackReason.maxLength = 512; rollbackReason.placeholder = "回滚原因（至少 8 个字符）";
      const rollbackButton = text("button", "执行回滚", "secondary danger-action"); rollbackButton.type = "button";
      selector.addEventListener("change", () => { rollbackID.value = selector.value; });
      rollbackButton.addEventListener("click", async () => {
        try {
          const item = items.find((candidateItem) => candidateItem.providerId === rollbackID.value.trim());
          const result = await policyMutation(`/providers/${encodeURIComponent(rollbackID.value.trim())}/rollback`, Number(item?.etag || 0), crypto.randomUUID(), {
            targetRevision: Number(target.value), reason: rollbackReason.value.trim(),
          });
          sessionStorage.removeItem(providerDraftStorageKey); await loadProviders();
          policyMessage(document.getElementById("content"), `已回滚到 revision ${result.revision}，新 ETag ${result.etag}。`);
        } catch (error) { policyMessage(rollback, error.message, "error"); }
      });
      const fields = document.createElement("div"); fields.className = "policy-rollback";
      fields.append(policyField("Provider ID", rollbackID), policyField("目标 Revision", target), policyField("原因", rollbackReason), rollbackButton); rollback.append(fields);
      content.replaceChildren(toolbar, summary, panel, rollback); return;
    }
    content.replaceChildren(toolbar, summary, panel);
  } catch (error) {
    if (error.status === 401) { await renderLogin("管理会话已过期，请重新登录。"); return; }
    renderError(error.message);
  }
}

function renderPendingProvider(panel, actions, pending, reasonInput) {
  if (!capabilityAllowed("admin.provider/publish") || actions.querySelector("[data-provider-publish]")) return;
  const publish = text("button", `发布 Revision ${pending.revision}`, "primary"); publish.type = "button"; publish.dataset.providerPublish = "true";
  publish.addEventListener("click", async () => {
    try {
      const result = await policyMutation(`/providers/${encodeURIComponent(pending.providerId)}/changes/${encodeURIComponent(pending.changeId)}/publish`,
        pending.baseEtag, pending.key, { reason: reasonInput.value.trim() || "publish validated Provider" });
      sessionStorage.removeItem(providerDraftStorageKey); await loadProviders();
      policyMessage(document.getElementById("content"), `Provider revision ${result.revision} 已发布，ETag ${result.etag}。`);
    } catch (error) { policyMessage(panel, error.message || "发布 Provider 失败。", "error"); }
  }); actions.append(publish);
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

function csrfJSON(method, body) {
  return {
    method,
    headers: { "Content-Type": "application/json", "X-KubeLoop-CSRF": sessionStorage.getItem(csrfStorageKey) || "" },
    body: JSON.stringify(body),
  };
}

async function loadUsers() {
  const content = document.getElementById("content");
  content.replaceChildren(text("div", "正在加载用户…", "empty"));
  try {
    const result = await requestJSON(`${managementBase}/users`);
    const roles = new Map();
    if (capabilityAllowed("admin.policy/read")) {
      try {
        const policy = await requestJSON(`${managementBase}/policy`);
        (policy.spec?.assignments || []).forEach((assignment) => {
          (assignment.subjects || []).forEach((principalID) => {
            const values = roles.get(principalID) || [];
            values.push(assignment.role); roles.set(principalID, values);
          });
        });
      } catch { /* User management remains available if policy detail is unavailable. */ }
    }
    const panel = document.createElement("section"); panel.className = "policy-panel compact";
    panel.append(text("h2", "创建本地用户"));
    panel.append(text("p", "新用户默认没有管理权限。创建后请在“管理策略”中按 Principal ID 分配角色。", "subtle"));
    const form = document.createElement("form"); form.className = "user-form";
    form.innerHTML = `<label>用户名<input name="username" required autocomplete="off"></label>
      <label>显示名称<input name="displayName" autocomplete="off"></label>
      <label>邮箱<input name="email" type="email" autocomplete="off"></label>
      <label>初始密码<input name="password" type="password" minlength="12" autocomplete="new-password" required></label>
      <button class="primary" type="submit">创建用户</button>`;
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      const data = new FormData(form);
      try {
        await requestJSON(`${managementBase}/users`, csrfJSON("POST", Object.fromEntries(data.entries())));
        form.reset(); await loadUsers();
      } catch (error) { renderError(error.message); }
    });
    panel.append(form);

    const wrap = document.createElement("div"); wrap.className = "table-wrap";
    const users = Array.isArray(result.items) ? result.items : [];
    if (!users.length) wrap.append(text("div", "尚无本地用户。", "empty"));
    else {
      const table = document.createElement("table");
      table.innerHTML = "<thead><tr><th>用户名</th><th>名称</th><th>邮箱</th><th>角色</th><th>状态</th><th>MFA</th><th>操作</th></tr></thead>";
      const body = document.createElement("tbody");
      users.forEach((user) => {
        const row = document.createElement("tr");
        [user.username, user.displayName, user.email || "—"].forEach((value) => row.append(text("td", value)));
        row.append(text("td", (roles.get(user.principalId) || []).join(", ") || "无权限"));
        const status = document.createElement("td"); status.append(text("span", user.enabled ? "启用" : "停用", `pill ${user.enabled ? "active" : "failed"}`)); row.append(status);
        row.append(text("td", user.mfaEnabled ? "TOTP + 恢复码" : "未启用"));
        const actions = document.createElement("td"); actions.className = "inline-actions";
        const toggle = text("button", user.enabled ? "停用" : "启用", "secondary"); toggle.type = "button";
        toggle.addEventListener("click", async () => {
          try { await requestJSON(`${managementBase}/users/${encodeURIComponent(user.principalId)}/status`, csrfJSON("PATCH", { enabled: !user.enabled })); await loadUsers(); }
          catch (error) { renderError(error.message); }
        });
        const reset = text("button", "重置密码", "secondary"); reset.type = "button";
        reset.addEventListener("click", async () => {
          const password = window.prompt("输入不少于 12 个字符的新密码") || "";
          if (!password) return;
          try { await requestJSON(`${managementBase}/users/${encodeURIComponent(user.principalId)}/password`, csrfJSON("PUT", { password })); }
          catch (error) { renderError(error.message); }
        });
        actions.append(toggle, reset); row.append(actions); body.append(row);
      });
      table.append(body); wrap.append(table);
    }
    content.replaceChildren(panel, wrap);
  } catch (error) {
    if (error.status === 401) { await renderLogin("管理会话已过期，请重新登录。"); return; }
    renderError(error.message);
  }
}

async function loadSecurity() {
  const content = document.getElementById("content");
  content.replaceChildren(text("div", "正在加载账户安全设置…", "empty"));
  try {
    const user = await requestJSON(`${managementBase}/users/me`);
    const panel = document.createElement("section"); panel.className = "policy-panel";
    panel.append(text("h2", `${user.displayName || user.username} 的 MFA`));
    panel.append(text("p", user.mfaEnabled ? "TOTP 已启用。登录时可使用动态验证码或任一未使用的恢复码。" : "TOTP 尚未启用。启用后会生成 10 个一次性恢复码。", "subtle"));
    if (!user.mfaEnabled) {
      const start = text("button", "启用 TOTP", "primary"); start.type = "button";
      start.addEventListener("click", async () => {
        try {
          const enrollment = await requestJSON(`${managementBase}/users/me/mfa/totp/start`, csrfJSON("POST", {}));
          const setup = document.createElement("div"); setup.className = "mfa-setup";
          setup.append(text("p", "在认证器中扫描/导入下面的 URI，或手工输入密钥。", "subtle"));
          const qr = document.createElement("img"); qr.className = "totp-qr"; qr.src = enrollment.qrCodeDataUrl; qr.alt = "TOTP 注册二维码";
          const secret = text("code", enrollment.secret, "secret-value");
          const uri = text("code", enrollment.provisioningUri, "secret-value");
          const code = document.createElement("input"); code.placeholder = "6 位动态验证码"; code.autocomplete = "one-time-code";
          const confirm = text("button", "验证并启用", "primary"); confirm.type = "button";
          confirm.addEventListener("click", async () => {
            try {
              const completed = await requestJSON(`${managementBase}/users/me/mfa/totp/confirm`, csrfJSON("POST", { enrollmentToken: enrollment.enrollmentToken, code: code.value }));
              showRecoveryCodes(panel, completed.recoveryCodes || []);
            } catch (error) { policyMessage(panel, error.message, "error"); }
          });
          setup.append(qr, policyField("手工密钥", secret), policyField("Provisioning URI", uri), policyField("动态验证码", code), confirm);
          start.replaceWith(setup);
        } catch (error) { policyMessage(panel, error.message, "error"); }
      });
      panel.append(start);
    } else {
      const regenerate = text("button", "重新生成恢复码", "secondary"); regenerate.type = "button";
      regenerate.addEventListener("click", async () => {
        const code = window.prompt("输入当前 TOTP 动态验证码") || "";
        if (!code) return;
        try {
          const result = await requestJSON(`${managementBase}/users/me/mfa/recovery-codes`, csrfJSON("POST", { code }));
          showRecoveryCodes(panel, result.recoveryCodes || []);
        } catch (error) { policyMessage(panel, error.message, "error"); }
      });
      const disable = text("button", "关闭 TOTP", "secondary danger-action"); disable.type = "button";
      disable.addEventListener("click", async () => {
        const password = window.prompt("输入当前密码") || "";
        if (!password) return;
        const code = window.prompt("输入当前 TOTP 动态验证码") || "";
        if (!code) return;
        try {
          await requestJSON(`${managementBase}/users/me/mfa/totp`, csrfJSON("DELETE", { password, code }));
          await loadSecurity();
        } catch (error) { policyMessage(panel, error.message, "error"); }
      });
      const actions = document.createElement("div"); actions.className = "policy-actions"; actions.append(regenerate, disable);
      panel.append(actions);
    }
    content.replaceChildren(panel);
  } catch (error) {
    if (error.status === 404) {
      content.replaceChildren(text("div", "当前会话来自企业 OIDC；TOTP 由上游身份提供方管理。", "empty")); return;
    }
    if (error.status === 401) { await renderLogin("管理会话已过期，请重新登录。"); return; }
    renderError(error.message);
  }
}

function showRecoveryCodes(panel, codes) {
  const box = document.createElement("section"); box.className = "recovery-codes";
  box.append(text("h2", "立即保存恢复码"), text("p", "每个恢复码只能使用一次，离开此页面后不会再次显示。", "subtle"));
  const list = document.createElement("pre"); list.textContent = codes.join("\n"); box.append(list);
  panel.replaceChildren(box);
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
      columns[state.activeView].forEach(([, label]) => headRow.append(text("th", label)));
      const showActions = items.some((item) => rowOperation(item) !== null);
      if (showActions) headRow.append(text("th", "操作")); head.append(headRow); table.append(head);
      const body = document.createElement("tbody");
      items.forEach((item) => {
        const row = document.createElement("tr");
        columns[state.activeView].forEach(([key]) => {
          const cell = document.createElement("td");
          const value = displayValue(key, item[key]);
          if (key === "state" || key === "desiredState" || key === "outcome") cell.append(text("span", value, `pill ${String(item[key] || "").toLowerCase()}`));
          else cell.textContent = value;
          row.append(cell);
        });
        if (showActions) { const cell = document.createElement("td"); const action = rowOperation(item); if (action) cell.append(action); row.append(cell); }
        body.append(row);
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
