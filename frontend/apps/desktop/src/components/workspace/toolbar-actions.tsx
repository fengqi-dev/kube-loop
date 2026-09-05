import type { ReactNode } from "react";
import { Ellipsis } from "lucide-react";
import { useI18n } from "@/i18n";
export function ToolbarActions({ children }: { children: ReactNode }) {
  const { t } = useI18n();
  return <>
    <div className="hidden items-center gap-1 sm:flex">{children}</div>
    <details className="toolbar-more relative sm:hidden">
      <summary className="flex h-7 cursor-pointer list-none items-center rounded border px-2" aria-label={t("workspace.more")}><Ellipsis size={16} /></summary>
      <div className="absolute right-0 z-30 mt-1 flex min-w-max flex-col gap-1 rounded border bg-popover p-2 shadow-md" onClick={event => {
        if ((event.target as HTMLElement).closest("button:not(:disabled)")) event.currentTarget.closest("details")?.removeAttribute("open");
      }}>{children}</div>
    </details>
  </>;
}
