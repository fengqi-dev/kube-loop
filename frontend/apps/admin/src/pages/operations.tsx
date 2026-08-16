import { useCallback, useEffect, useMemo, useState } from "react";
import { Download, RefreshCw, RotateCcw, ShieldCheck, Square } from "lucide-react";
import { mutation, request, waitForAuditExport } from "../api";
import { Button, ConfirmDialog, Empty, Loading, Metric, Notice, PageHeader } from "../components";
import { useI18n } from "../i18n";
import type {
  AuditEvent,
  EffectiveAuthorization,
  ListResponse,
  OAuthGrant,
  Overview,
  RelayStatus,
  RuntimeSession,
  RuntimeTask,
} from "../types";

function useResource<T>(path: string) {
  const [value, setValue] = useState<T>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const reload = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setValue(await request<T>(path));
    } catch (cause) {
      setError((cause as Error).message);
    } finally {
      setLoading(false);
    }
  }, [path]);
  useEffect(() => void reload(), [reload]);
  return { value, loading, error, reload };
}

function ResourceState({ loading, error, empty, children }: { loading: boolean; error: string; empty: boolean; children: React.ReactNode }) {
  const { locale } = useI18n();
  if (loading) return <Loading />;
  if (error) return <Notice>{error}</Notice>;
  if (empty) return <Empty>{locale === "zh-CN" ? "暂无可见记录。" : "No visible records."}</Empty>;
  return <>{children}</>;
}

function RefreshButton({ onClick }: { onClick: () => void }) {
  const { locale } = useI18n();
  return <Button onClick={onClick}><RefreshCw size={14} />{locale === "zh-CN" ? "刷新" : "Refresh"}</Button>;
}

export function OverviewPage() {
  const { locale } = useI18n(), state = useResource<Overview>("/overview"), value = state.value;
  return <>
    <PageHeader title={locale === "zh-CN" ? "总览" : "Overview"} description={locale === "zh-CN" ? "Control Plane、安全与运行资源的实时摘要。" : "Live Control Plane, security, and runtime summary."} actions={<RefreshButton onClick={() => void state.reload()} />} />
    <ResourceState loading={state.loading} error={state.error} empty={!value}>
      {value && <>
        <div className="metrics">
          <Metric label={locale === "zh-CN" ? "活动会话" : "Active sessions"} value={value.runtime.activeSessions.count} />
          <Metric label={locale === "zh-CN" ? "活动任务" : "Active tasks"} value={value.runtime.activeTasks.count} />
          <Metric label={locale === "zh-CN" ? "在线 Relay" : "Online relays"} value={`${value.runtime.relays.online}/${value.runtime.relays.total}`} />
          <Metric label={locale === "zh-CN" ? "存储" : "Storage"} value={value.system.storage.status} />
        </div>
        <section className="table-panel compact-panel"><h2>{locale === "zh-CN" ? "系统" : "System"}</h2><dl className="detail-grid"><div><dt>Version</dt><dd>{value.system.controlPlane.version}</dd></div><div><dt>Commit</dt><dd>{value.system.controlPlane.commit}</dd></div><div><dt>Protocol</dt><dd>{value.system.controlPlane.protocolMin}–{value.system.controlPlane.protocolMax}</dd></div><div><dt>Storage</dt><dd>{value.system.storage.backend} / schema {value.system.storage.schemaVersion}</dd></div></dl></section>
        <AuditTable items={value.recentAudit} />
      </>}
    </ResourceState>
  </>;
}

export function PoliciesPage() {
  const { locale } = useI18n(), state = useResource<EffectiveAuthorization>("/authorization/effective"), value = state.value;
  const allowed = useMemo(() => value?.capabilities.filter((item) => item.allowed) ?? [], [value]);
  return <>
    <PageHeader title={locale === "zh-CN" ? "有效策略" : "Effective policies"} description={locale === "zh-CN" ? "只读展示当前身份从用户组继承的实际权限；授权变更请前往用户组。" : "Read-only effective access inherited from groups; edit assignments on the Groups page."} actions={<RefreshButton onClick={() => void state.reload()} />} />
    <ResourceState loading={state.loading} error={state.error} empty={!value}>
      {value && <>
        <div className="metrics compact"><Metric label={locale === "zh-CN" ? "管理员" : "Administrator"} value={value.administrator ? "Yes" : "No"} /><Metric label="Groups" value={value.groups.length} /><Metric label="Namespaces" value={value.administrator ? "*" : value.namespaces.length} /><Metric label="Capabilities" value={allowed.length} /></div>
        <div className="table-panel"><table><thead><tr><th>Capability</th><th>Scope</th><th>Allowed</th><th>Namespaces</th><th>Source groups</th></tr></thead><tbody>{value.capabilities.map((item) => <tr key={item.id}><td><code>{item.id}</code></td><td>{item.scope}</td><td>{item.allowed ? "✓" : "—"}</td><td>{item.namespaces?.join(", ") || "—"}</td><td>{item.matchingGroupIds?.join(", ") || "—"}</td></tr>)}</tbody></table></div>
      </>}
    </ResourceState>
  </>;
}

export function SessionsPage() {
  const { locale } = useI18n(), state = useResource<ListResponse<RuntimeSession>>("/sessions?limit=100"), [selected, setSelected] = useState<RuntimeSession>(), [busy, setBusy] = useState(false), [error, setError] = useState("");
  const items = state.value?.items ?? [];
  const stop = async (reason: string) => { if (!selected) return; setBusy(true); setError(""); try { await mutation(`/sessions/${selected.id}/stop`, "POST", { reason }, { etag: selected.generation, idempotent: true }); setSelected(undefined); await state.reload(); } catch (cause) { setError((cause as Error).message); } finally { setBusy(false); } };
  return <>
    <PageHeader title={locale === "zh-CN" ? "会话" : "Sessions"} description={locale === "zh-CN" ? "查看并停止真实 Gateway 数据平面会话。" : "Inspect and stop live Gateway data-plane sessions."} actions={<RefreshButton onClick={() => void state.reload()} />} />
    {error && <Notice>{error}</Notice>}
    <ResourceState loading={state.loading} error={state.error} empty={!items.length}><div className="table-panel"><table><thead><tr><th>Namespace</th><th>Identity</th><th>Cluster</th><th>State</th><th>Heartbeat</th><th /></tr></thead><tbody>{items.map((item) => <tr key={item.id}><td>{item.namespace}</td><td><code>{item.identityId}</code></td><td>{item.clusterId}</td><td>{item.state}</td><td>{new Date(item.lastHeartbeatAt).toLocaleString()}</td><td><Button kind="danger" onClick={() => setSelected(item)} disabled={item.state !== "active"}><Square size={13} />Stop</Button></td></tr>)}</tbody></table></div></ResourceState>
    <ConfirmDialog open={Boolean(selected)} title={locale === "zh-CN" ? "停止会话" : "Stop session"} detail={selected?.id} busy={busy} onClose={() => setSelected(undefined)} onConfirm={(reason) => void stop(reason)} />
  </>;
}

export function GrantsPage() {
  const { locale } = useI18n(), state = useResource<ListResponse<OAuthGrant>>("/oauth-grants?limit=100"), [selected, setSelected] = useState<OAuthGrant>(), [busy, setBusy] = useState(false), [error, setError] = useState("");
  const items = state.value?.items ?? [];
  const revoke = async (reason: string) => { if (!selected) return; setBusy(true); setError(""); try { await mutation(`/identities/${selected.identityId}/oauth-grants/${selected.authorizationId}/revoke`, "POST", { reason }, { idempotent: true }); setSelected(undefined); await state.reload(); } catch (cause) { setError((cause as Error).message); } finally { setBusy(false); } };
  return <>
    <PageHeader title="OAuth Grants" description={locale === "zh-CN" ? "查看设备授权并撤销 OAuth Grant。" : "Inspect device authorizations and revoke OAuth grants."} actions={<RefreshButton onClick={() => void state.reload()} />} />
    {error && <Notice>{error}</Notice>}
    <ResourceState loading={state.loading} error={state.error} empty={!items.length}><div className="table-panel"><table><thead><tr><th>Client</th><th>Identity</th><th>Device</th><th>Scopes</th><th>Status</th><th /></tr></thead><tbody>{items.map((item) => <tr key={item.authorizationId}><td>{item.clientId}</td><td><code>{item.identityId}</code></td><td>{item.deviceId}</td><td>{item.scopes.join(", ")}</td><td>{item.status}</td><td><Button kind="danger" disabled={item.status !== "active"} onClick={() => setSelected(item)}>Revoke</Button></td></tr>)}</tbody></table></div></ResourceState>
    <ConfirmDialog open={Boolean(selected)} title={locale === "zh-CN" ? "撤销 OAuth Grant" : "Revoke OAuth grant"} detail={selected?.authorizationId} busy={busy} onClose={() => setSelected(undefined)} onConfirm={(reason) => void revoke(reason)} />
  </>;
}

function AuditTable({ items }: { items: AuditEvent[] }) {
  const { locale } = useI18n();
  if (!items.length) return <Empty>{locale === "zh-CN" ? "暂无审计记录。" : "No audit events."}</Empty>;
  return <div className="table-panel"><table><thead><tr><th>Time</th><th>Action</th><th>Outcome</th><th>Identity</th><th>Request</th></tr></thead><tbody>{items.map((item) => <tr key={item.id}><td>{new Date(item.createdAt).toLocaleString()}</td><td>{item.action}</td><td>{item.outcome}</td><td><code>{item.identityId || "system"}</code></td><td><code>{item.requestId}</code></td></tr>)}</tbody></table></div>;
}

export function AuditPage() {
  const { locale } = useI18n(), state = useResource<ListResponse<AuditEvent>>("/audit?limit=100"), [exporting, setExporting] = useState(false), [error, setError] = useState("");
  const exportAudit = async () => { setExporting(true); setError(""); try { const result = await mutation<{ jobId: string }>("/audit/exports", "POST", { limit: 10000, reason: "Export audit records from the management console" }, { idempotent: true }); const blob = await waitForAuditExport(result.jobId); const link = document.createElement("a"); link.href = URL.createObjectURL(blob); link.download = `kubeloop-audit-${result.jobId}.ndjson`; link.click(); URL.revokeObjectURL(link.href); } catch (cause) { setError((cause as Error).message); } finally { setExporting(false); } };
  const items = state.value?.items ?? [];
  return <><PageHeader title={locale === "zh-CN" ? "审计" : "Audit"} description={locale === "zh-CN" ? "查询不可变审计事件并导出 NDJSON。" : "Query immutable audit events and export NDJSON."} actions={<><RefreshButton onClick={() => void state.reload()} /><Button kind="primary" busy={exporting} onClick={() => void exportAudit()}><Download size={14} />Export</Button></>} />{error && <Notice>{error}</Notice>}<ResourceState loading={state.loading} error={state.error} empty={!items.length}><AuditTable items={items} /></ResourceState></>;
}

export function RuntimePage() {
  const { locale } = useI18n(), tasks = useResource<ListResponse<RuntimeTask>>("/tasks?limit=100"), relays = useResource<ListResponse<RelayStatus>>("/relays?limit=100"), [action, setAction] = useState<{ kind: "task" | "relay" | "recovery"; id?: string; version?: number; desired?: "drain" | "recover" }>(), [busy, setBusy] = useState(false), [error, setError] = useState("");
  const run = async (reason: string) => { if (!action) return; setBusy(true); setError(""); try { if (action.kind === "task") await mutation(`/tasks/${action.id}/stop`, "POST", { reason }, { etag: action.version, idempotent: true }); else if (action.kind === "relay") await mutation(`/relays/${action.id}/${action.desired}`, "POST", { reason }, { etag: action.version, idempotent: true }); else await mutation("/tasks/recovery", "POST", { reason }, { idempotent: true }); setAction(undefined); await Promise.all([tasks.reload(), relays.reload()]); } catch (cause) { setError((cause as Error).message); } finally { setBusy(false); } };
  const taskItems = tasks.value?.items ?? [], relayItems = relays.value?.items ?? [];
  return <>
    <PageHeader title={locale === "zh-CN" ? "运行资源" : "Runtime resources"} description={locale === "zh-CN" ? "管理远程任务、Relay 状态与恢复扫描。" : "Manage remote tasks, Relay state, and recovery scans."} actions={<><RefreshButton onClick={() => void Promise.all([tasks.reload(), relays.reload()])} /><Button onClick={() => setAction({ kind: "recovery" })}><RotateCcw size={14} />Recovery</Button></>} />
    {error && <Notice>{error}</Notice>}
    <section className="table-panel compact-panel"><h2>Tasks</h2><ResourceState loading={tasks.loading} error={tasks.error} empty={!taskItems.length}><table><thead><tr><th>Type</th><th>State</th><th>Identity</th><th>Session</th><th /></tr></thead><tbody>{taskItems.map((item) => <tr key={item.id}><td>{item.type}</td><td>{item.state}</td><td><code>{item.identityId}</code></td><td><code>{item.sessionId}</code></td><td><Button kind="danger" onClick={() => setAction({ kind: "task", id: item.id, version: item.version })}>Stop</Button></td></tr>)}</tbody></table></ResourceState></section>
    <section className="table-panel compact-panel"><h2>Relays</h2><ResourceState loading={relays.loading} error={relays.error} empty={!relayItems.length}><table><thead><tr><th>Relay</th><th>State</th><th>Online</th><th>Reservations</th><th /></tr></thead><tbody>{relayItems.map((item) => <tr key={item.relayId}><td>{item.relayId}</td><td>{item.state} → {item.desiredState}</td><td>{item.online ? "✓" : "—"}</td><td>{item.reservations}</td><td><Button onClick={() => setAction({ kind: "relay", id: item.relayId, version: item.controlVersion, desired: item.desiredState === "draining" ? "recover" : "drain" })}><ShieldCheck size={13} />{item.desiredState === "draining" ? "Recover" : "Drain"}</Button></td></tr>)}</tbody></table></ResourceState></section>
    <ConfirmDialog open={Boolean(action)} title={action?.kind === "recovery" ? "Run recovery" : action?.kind === "relay" ? `${action.desired} Relay` : "Stop task"} detail={action?.id} busy={busy} onClose={() => setAction(undefined)} onConfirm={(reason) => void run(reason)} />
  </>;
}
