import { CircleAlert, Power } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { isBusyPhase } from "@/lib/phase";
import { cn } from "@/lib/utils";
import type { SessionState } from "@/types";

export function ConnectionOrb({
  phase,
  busy = false,
  disabled,
  ariaLabel,
  onClick,
}: {
  phase: SessionState["phase"];
  busy?: boolean;
  disabled?: boolean;
  ariaLabel: string;
  onClick(): void;
}) {
  const working = busy || isBusyPhase(phase);
  const ready = phase === "connected" && !working;
  const error = phase === "error" && !working;

  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      aria-label={ariaLabel}
      aria-busy={working}
      className={cn(
        "relative grid size-[72px] place-items-center rounded-full border transition-all outline-none",
        "focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50",
        "disabled:pointer-events-none disabled:opacity-50",
        "active:translate-y-px active:scale-[0.98]",
        // Connected = running (click to stop)
        ready &&
          "border-success/40 bg-success text-success-foreground shadow-lg shadow-success/30 hover:bg-success/90",
        // Error
        error &&
          "border-destructive/40 bg-destructive text-white shadow-[0_8px_24px_-8px_rgba(229,72,77,0.45)] hover:bg-destructive/90",
        // Connecting / busy
        working &&
          "border-chart-5/45 bg-chart-5/15 text-chart-5 hover:bg-chart-5/20",
        // Idle = start
        !ready &&
          !error &&
          !working &&
          "border-primary/50 bg-primary text-primary-foreground shadow-lg shadow-primary/30 hover:bg-primary/90",
      )}
    >
      {working ? (
        <Spinner className="size-[30px]" />
      ) : error ? (
        <CircleAlert size={30} strokeWidth={1.8} />
      ) : (
        <Power
          size={30}
          strokeWidth={1.8}
          className={cn(ready && "drop-shadow-sm")}
        />
      )}
      {ready ? (
        <span
          aria-hidden
          className="pointer-events-none absolute inset-[-5px] rounded-full border border-success/35"
        />
      ) : null}
    </button>
  );
}
