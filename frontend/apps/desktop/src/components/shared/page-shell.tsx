import type { ReactNode } from "react";

export function PageShell({
  title,
  description,
  action,
  children,
}: {
  title: string;
  description: string;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="mx-auto max-w-[1080px]">
      <div className="mb-6 flex items-center justify-between gap-4">
        <div>
          <h2 className="text-[16px] font-bold tracking-tight">{title}</h2>
          <p className="mt-1 text-[12px] text-muted-foreground">{description}</p>
        </div>
        {action}
      </div>
      {children}
    </div>
  );
}
