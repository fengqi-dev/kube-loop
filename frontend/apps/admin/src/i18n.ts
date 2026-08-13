import { createContext, useContext } from "react";

export type Locale = "zh-CN" | "en-US";
export const localeStorageKey = "kubeloop.admin.locale";

const zh = {
  brand: "KubeLoop Control", signIn: "进入管理后台", signInHint: "使用企业身份提供方安全登录。浏览器不会保存 Access Token 或 Refresh Token。",
  loggingIn: "正在安全登录…", noProviders: "没有可用的浏览器登录方式。", logout: "退出", refresh: "刷新", search: "搜索",
  navOverview: "总览", navIdentity: "身份与访问", navGovernance: "策略治理", navRuntime: "运行资源", navCompliance: "合规审计",
  overview: "运行概览", policy: "角色授权", providers: "身份 Provider", users: "用户管理", security: "账户安全", principals: "Principal", sessions: "会话", tasks: "任务", relays: "Relay", audit: "审计日志",
  overviewDesc: "Control Plane、安全策略与运行资源的集中态势。", policyDesc: "按用户组优先分配角色，并将 Namespace 权限限制在明确范围。",
  providersDesc: "验证、版本化并发布 OIDC 身份提供方。", usersDesc: "创建和管理本地管理用户。", securityDesc: "管理当前本地账户的多因素认证。",
  principalsDesc: "已由身份提供方解析的管理身份。", sessionsDesc: "查看并控制集群会话生命周期。", tasksDesc: "跟踪并控制远程任务。", relaysDesc: "监控 Data Plane 容量、健康与排空状态。", auditDesc: "检索、追踪并导出安全审计事件。",
  authType: "认证方式", capabilities: "全局能力", delegations: "Namespace 委派", policyRevision: "策略 Revision", systemStatus: "系统状态", unavailable: "暂时无法获取数据。",
  loading: "正在加载…", empty: "没有符合条件的记录。", actions: "操作", next: "下一页", previous: "上一页", namespace: "Namespace", all: "全部", records: "条记录",
  createUser: "新建用户", username: "用户名", displayName: "显示名称", email: "邮箱", password: "初始密码", create: "创建", enabled: "启用", disabled: "停用", resetPassword: "重置密码",
  assignments: "角色授权", addAssignment: "添加授权", customRoles: "自定义角色", createRole: "新建角色", roleId: "角色 ID", roleName: "角色名称", roleDescription: "角色说明", permissions: "权限", selectPermissions: "至少选择一个权限。", groupFirst: "建议优先绑定企业用户组；个人 Principal 仅用于临时或例外授权。", group: "用户组", principal: "个人 Principal", role: "角色", subjectType: "授权对象", subjectValue: "对象标识", saveDraft: "创建 Draft", validate: "校验 / Dry-run", publish: "发布", rollback: "回滚", targetRevision: "目标 Revision", reason: "变更原因", remove: "移除", candidate: "候选策略", current: "当前已发布",
  providerId: "Provider ID", providerType: "类型", config: "配置 JSON", secretAliases: "Secret Alias JSON", selectProvider: "选择 Provider", connectivity: "验证连通性", configured: "已配置", revision: "Revision",
  accountSecurity: "账户安全", enableTotp: "启用 TOTP", confirmTotp: "验证并启用", recoveryCodes: "恢复码", disableTotp: "关闭 TOTP", regenerateCodes: "重新生成恢复码", oneTimeCode: "动态验证码", currentPassword: "当前密码",
  confirmAction: "确认操作", operationReason: "请输入操作原因（8–512 个字符）", cancel: "取消", confirm: "确认", stop: "停止", revoke: "撤销设备", drain: "排空", recover: "恢复接流", export: "导出 NDJSON", triggerRecovery: "触发恢复",
  sessionExpired: "管理会话已过期，请重新登录。", forbidden: "当前身份没有管理权限。", requestFailed: "请求失败", unknown: "未知", online: "在线", offline: "离线", active: "正常", status: "状态", mfa: "MFA", noAccess: "无权限", language: "语言", openMenu: "打开导航", close: "关闭", name: "名称", provider: "Provider", groups: "用户组", created: "创建时间", session: "会话", cluster: "集群", state: "状态", heartbeat: "心跳", expires: "过期时间", task: "任务", type: "类型", updated: "更新时间", relay: "Relay", desired: "期望状态", capacity: "容量", time: "时间", action: "操作", outcome: "结果", resource: "资源", resourceId: "资源 ID", requestId: "请求 ID",
} as const;

const en: Record<keyof typeof zh, string> = {
  brand: "KubeLoop Control", signIn: "Sign in to Admin", signInHint: "Sign in securely with your identity provider. Access and refresh tokens are never stored by the browser.",
  loggingIn: "Signing in securely…", noProviders: "No browser sign-in method is available.", logout: "Sign out", refresh: "Refresh", search: "Search",
  navOverview: "Overview", navIdentity: "Identity & Access", navGovernance: "Policy Governance", navRuntime: "Runtime Resources", navCompliance: "Compliance & Audit",
  overview: "Operations Overview", policy: "Role Assignments", providers: "Identity Providers", users: "Users", security: "Account Security", principals: "Principals", sessions: "Sessions", tasks: "Tasks", relays: "Relays", audit: "Audit Log",
  overviewDesc: "A unified view of the Control Plane, security policy, and runtime resources.", policyDesc: "Assign roles group-first and constrain namespace access to explicit scopes.",
  providersDesc: "Validate, version, and publish OIDC identity providers.", usersDesc: "Create and manage local administrators.", securityDesc: "Manage multi-factor authentication for the current local account.",
  principalsDesc: "Management identities resolved by identity providers.", sessionsDesc: "Inspect and control cluster session lifecycles.", tasksDesc: "Track and control remote tasks.", relaysDesc: "Monitor Data Plane capacity, health, and drain state.", auditDesc: "Search, trace, and export security audit events.",
  authType: "Authentication", capabilities: "Global capabilities", delegations: "Namespace delegations", policyRevision: "Policy revision", systemStatus: "System status", unavailable: "Data is temporarily unavailable.",
  loading: "Loading…", empty: "No matching records.", actions: "Actions", next: "Next", previous: "Previous", namespace: "Namespace", all: "All", records: "records",
  createUser: "New user", username: "Username", displayName: "Display name", email: "Email", password: "Initial password", create: "Create", enabled: "Enabled", disabled: "Disabled", resetPassword: "Reset password",
  assignments: "Role assignments", addAssignment: "Add assignment", customRoles: "Custom roles", createRole: "New role", roleId: "Role ID", roleName: "Role name", roleDescription: "Role description", permissions: "Permissions", selectPermissions: "Select at least one permission.", groupFirst: "Prefer enterprise groups. Direct Principal assignments should be temporary exceptions.", group: "Group", principal: "Principal", role: "Role", subjectType: "Subject type", subjectValue: "Subject identifier", saveDraft: "Create draft", validate: "Validate / Dry-run", publish: "Publish", rollback: "Rollback", targetRevision: "Target revision", reason: "Change reason", remove: "Remove", candidate: "Candidate policy", current: "Published",
  providerId: "Provider ID", providerType: "Type", config: "Config JSON", secretAliases: "Secret alias JSON", selectProvider: "Select provider", connectivity: "Validate connectivity", configured: "configured", revision: "Revision",
  accountSecurity: "Account security", enableTotp: "Enable TOTP", confirmTotp: "Verify and enable", recoveryCodes: "Recovery codes", disableTotp: "Disable TOTP", regenerateCodes: "Regenerate recovery codes", oneTimeCode: "One-time code", currentPassword: "Current password",
  confirmAction: "Confirm operation", operationReason: "Enter an operation reason (8–512 characters)", cancel: "Cancel", confirm: "Confirm", stop: "Stop", revoke: "Revoke devices", drain: "Drain", recover: "Recover", export: "Export NDJSON", triggerRecovery: "Run recovery",
  sessionExpired: "Your admin session expired. Sign in again.", forbidden: "This identity has no admin access.", requestFailed: "Request failed", unknown: "Unknown", online: "Online", offline: "Offline", active: "Healthy", status: "Status", mfa: "MFA", noAccess: "No access", language: "Language", openMenu: "Open navigation", close: "Close", name: "Name", provider: "Provider", groups: "Groups", created: "Created", session: "Session", cluster: "Cluster", state: "State", heartbeat: "Heartbeat", expires: "Expires", task: "Task", type: "Type", updated: "Updated", relay: "Relay", desired: "Desired", capacity: "Capacity", time: "Time", action: "Action", outcome: "Outcome", resource: "Resource", resourceId: "Resource ID", requestId: "Request ID",
};

export type MessageKey = keyof typeof zh;
export const messages: Record<Locale, Record<MessageKey, string>> = { "zh-CN": zh, "en-US": en };
export function detectLocale(): Locale {
  const saved = localStorage.getItem(localeStorageKey);
  if (saved === "zh-CN" || saved === "en-US") return saved;
  return navigator.languages.some((language) => language.toLowerCase().startsWith("zh")) ? "zh-CN" : "en-US";
}
export const I18nContext = createContext({ locale: "zh-CN" as Locale, setLocale: (_locale: Locale) => {}, t: (key: MessageKey) => messages["zh-CN"][key] });
export const useI18n = () => useContext(I18nContext);
