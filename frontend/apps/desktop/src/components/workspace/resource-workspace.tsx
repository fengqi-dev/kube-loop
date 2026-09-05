import { createPortal } from "react-dom";
import { createContext, useContext, useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { ArrowLeft, PanelLeft, X, ChevronRight } from "lucide-react";
import { useI18n } from "@/i18n";
import { resolveSplit, resourceAvailability, type ResourceSelection } from "./workspace-model";

export const ResourceExplorer = createContext<{ host: HTMLElement | null; onSelect(): void } | null>(null);

export type WorkspaceResource = ResourceSelection & { fields: Array<[string, ReactNode]>; actions?: ReactNode };
export function useResourceWorkspace() {
  const [selection, setSelection] = useState<ResourceSelection | null>(null);
  const [showDetail, setShowDetail] = useState(false);
  return {
    state: { active: selection?.key ?? null }, selection, showDetail, setShowDetail,
    open: (resource: ResourceSelection) => { setSelection(resource); setShowDetail(true); },
    close: () => { setSelection(null); setShowDetail(false); },
  };
}

export type WorkspaceController = ReturnType<typeof useResourceWorkspace>;
export function ResourceWorkspace({ workspace, resources, settled, namespace, children }: {
  workspace: WorkspaceController; resources: WorkspaceResource[]; settled: boolean; namespace?: string; children: ReactNode;
}) {
  const { t } = useI18n();
  const explorer = useContext(ResourceExplorer);
  const [query, setQuery] = useState("");
  const root = useRef<HTMLDivElement>(null);
  const [split, setSplit] = useState(() => {
    try { return resolveSplit(localStorage.getItem("kubeloop.workspace.split")); } catch { return 55; }
  });
  useEffect(() => { try { localStorage.setItem("kubeloop.workspace.split", String(split)); } catch { /* optional preference */ } }, [split]);
  const selected = resources.find(item => item.key === workspace.state.active);
  const availability = resourceAvailability(workspace.state.active ?? "", resources.map(item => item.key), settled);
  const activeSelection = workspace.selection;
  const outsideScope = namespace !== undefined && activeSelection?.namespace !== namespace;
  const detail = workspace.showDetail && workspace.state.active !== null;
  return (
    <div ref={root} className="resource-workspace" data-detail={detail} data-selected={Boolean(workspace.state.active)} style={{ "--list-width": `${split}%` } as CSSProperties}>
      {explorer?.host && createPortal(<details open className="explorer-group">
        <summary>{t("shell.resources")} ({resources.length})<ChevronRight size={13} /></summary>
        <label className="explorer-search"><input aria-label={t("shell.searchExplorer")} placeholder={t("shell.searchExplorer")} value={query} onChange={event => setQuery(event.target.value)} /></label>
        {resources.filter(item => `${item.namespace}/${item.label}`.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())).map(item =>
          <button key={item.key} className="explorer-row" data-selected={workspace.state.active === item.key} disabled={!settled} title={`${item.namespace}/${item.label}`} onClick={() => { workspace.open(item); explorer.onSelect(); }}><span>{item.label}</span></button>)}
      </details>, explorer.host)}
      <section className="workspace-list" aria-label={t("workspace.list")}>
        {workspace.selection !== null && <button className="workspace-mobile-switch" onClick={() => workspace.setShowDetail(true)}>{t("workspace.details")} · {workspace.selection?.label}</button>}
        <fieldset disabled={!settled} className="min-w-0 border-0 p-0">{children}</fieldset>
      </section>
      <div className="workspace-divider" role="separator" tabIndex={0} aria-label={t("workspace.resize")} aria-orientation="vertical" aria-valuemin={25} aria-valuemax={75} aria-valuenow={Math.round(split)}
        onDoubleClick={() => setSplit(55)}
        onKeyDown={event => {
          if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) {
            event.preventDefault();
            setSplit(value => resolveSplit(event.key === "Home" ? 25 : event.key === "End" ? 75 : value + (event.key === "ArrowLeft" ? -2 : 2)));
          }
        }}
        onPointerDown={event => { event.currentTarget.setPointerCapture(event.pointerId); event.preventDefault(); }}
        onPointerMove={event => {
          if (!event.currentTarget.hasPointerCapture(event.pointerId)) return;
          const bounds = root.current?.getBoundingClientRect();
          if (bounds?.width) setSplit(resolveSplit((event.clientX - bounds.left) / bounds.width * 100));
        }}
        onPointerUp={event => { if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId); }}
      />
      <section className="workspace-detail" aria-label={t("workspace.details")}>
        <div className="workspace-detail-toolbar">
          <button className="workspace-mobile-back" aria-label={t("workspace.list")} onClick={() => workspace.setShowDetail(false)}><ArrowLeft size={14} /></button>
          <span>{t("workspace.details")}</span>
          <button className="workspace-detail-close" aria-label={t("workspace.close")} onClick={workspace.close}><X size={14} /></button>
        </div>
        <div className="workspace-inspector">
          {!workspace.state.active ? <div className="workspace-empty"><PanelLeft size={24} /><p>{t("workspace.select")}</p></div>
            : availability !== "ready" ? <p className="p-4 text-muted-foreground" role="status">{t(outsideScope ? "workspace.otherNamespace" : availability === "missing" ? "workspace.missing" : "workspace.loading")}</p>
            : selected && <>
              <div className="inspector-heading"><h2 className="break-all font-semibold">{selected.label}</h2><p className="mt-1 break-all text-xs text-muted-foreground">{selected.namespace}</p></div>
              {selected.actions && <fieldset disabled={!settled} className="flex flex-wrap gap-1 border-b p-2">{selected.actions}</fieldset>}
              <div className="inspector-section-title">{t("shell.properties")}</div>
              <dl className="workspace-fields">{selected.fields.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value || "—"}</dd></div>)}</dl>
            </>}
        </div>
      </section>
    </div>
  );
}
