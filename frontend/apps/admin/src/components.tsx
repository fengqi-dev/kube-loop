import { ReactNode, useEffect, useState } from "react";
import { AlertTriangle, CheckCircle2, ChevronRight, LoaderCircle } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button as ShadcnButton } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { useI18n } from "./i18n";

export function Button({ children, kind = "secondary", busy, ...props }: React.ButtonHTMLAttributes<HTMLButtonElement> & { kind?: "primary" | "secondary" | "danger" | "ghost"; busy?: boolean }) {
  const variant = kind === "primary" ? "default" : kind === "danger" ? "destructive" : kind === "ghost" ? "ghost" : "outline";
  return <ShadcnButton className="button" variant={variant} {...props} disabled={busy || props.disabled}>{busy && <LoaderCircle className="spin" size={15} />}{children}</ShadcnButton>;
}
export function Notice({ children, tone = "error" }: { children: ReactNode; tone?: "error" | "success" | "warning" }) {
  return <Alert className={`notice ${tone}`} variant={tone === "error" ? "destructive" : "default"} role={tone === "error" ? "alert" : "status"}>{tone === "success" ? <CheckCircle2 size={18} /> : <AlertTriangle size={18} />}<AlertDescription>{children}</AlertDescription></Alert>;
}
export function PageHeader({ title, description, actions }: { title: string; description: string; actions?: ReactNode }) {
  return <header className="page-header"><div><h1>{title}</h1><p>{description}</p></div>{actions && <div className="header-actions">{actions}</div>}</header>;
}
export function Metric({ label, value, tone }: { label: string; value: ReactNode; tone?: string }) { return <Card className={`metric-card ${tone || ""}`}><span>{label}</span><strong>{value}</strong></Card>; }
export function Empty({ children }: { children: ReactNode }) { return <div className="empty-state">{children}</div>; }
export function Loading() { const { t } = useI18n(); return <div className="loading"><LoaderCircle className="spin" size={20} />{t("loading")}</div>; }
export function ConfirmDialog({ open, title, detail, busy, onClose, onConfirm }: { open: boolean; title: string; detail?: string; busy?: boolean; onClose: () => void; onConfirm: (reason: string) => void }) {
  const { t } = useI18n(); const [reason, setReason] = useState("");
  useEffect(() => { if (open) setReason(""); }, [open]);
  const valid = reason.trim().length >= 8 && reason.trim().length <= 512 && !/[\r\n\0]/u.test(reason);
  return <Dialog open={open} onOpenChange={(next) => !next && onClose()}><DialogContent closeLabel={t("close")}><DialogHeader><div className="dialog-title"><span className="danger-icon"><AlertTriangle size={18} /></span><DialogTitle>{title}</DialogTitle></div>{detail && <DialogDescription className="dialog-detail">{detail}</DialogDescription>}</DialogHeader><label>{t("reason")}<Input autoFocus value={reason} onChange={(event) => setReason(event.target.value)} placeholder={t("operationReason")} maxLength={512} /></label><DialogFooter><Button onClick={onClose}>{t("cancel")}</Button><Button kind="danger" busy={busy} disabled={!valid} onClick={() => onConfirm(reason.trim())}>{t("confirm")}<ChevronRight size={15} /></Button></DialogFooter></DialogContent></Dialog>;
}
