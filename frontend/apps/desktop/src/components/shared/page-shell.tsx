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
    <div className="page-shell flex min-h-0 min-w-0 flex-1 flex-col">
      <header className="page-heading" data-has-action={Boolean(action)}>
        <div className="page-heading-copy">
          <h2>{title}</h2>
          <p>{description}</p>
        </div>
        {action}
      </header>
      {children}
    </div>
  );
}
