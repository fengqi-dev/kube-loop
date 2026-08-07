import { CircleAlert, Power } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { isBusyPhase } from "@/lib/phase";
import { cn } from "@/lib/utils";
import type { SessionState } from "@/types";
import type { CSSProperties } from "react";

const phaseProgress: Partial<Record<SessionState["phase"], number>> = {
  checking: 18,
  "installing-gateway": 42,
  "discovering-network": 68,
  "starting-tunnel": 90,
  connected: 100,
};

const ringRadius = 40;
const ringCircumference = 2 * Math.PI * ringRadius;
const magicSparkles = [
  { x: -44, y: -28, size: 10, delay: -0.1, duration: 1.9 },
  { x: -50, y: 8, size: 6, delay: -1.2, duration: 2.2 },
  { x: -32, y: 39, size: 8, delay: -0.7, duration: 1.7 },
  { x: 4, y: -49, size: 7, delay: -1.6, duration: 2.4 },
  { x: 43, y: -33, size: 13, delay: -0.4, duration: 2.1 },
  { x: 51, y: 5, size: 7, delay: -1.8, duration: 1.8 },
  { x: 31, y: 42, size: 9, delay: -1, duration: 2.3 },
];

export function ConnectionOrb({
  phase,
  busy = false,
  disconnecting = false,
  disabled,
  ariaLabel,
  onClick,
}: {
  phase: SessionState["phase"];
  busy?: boolean;
  disconnecting?: boolean;
  disabled?: boolean;
  ariaLabel: string;
  onClick(): void;
}) {
  const working = busy || isBusyPhase(phase);
  const ready = phase === "connected" && !working;
  const error = phase === "error" && !working;
  const connecting =
    !disconnecting && (isBusyPhase(phase) || (busy && phase !== "connected"));
  const progress = phaseProgress[phase] ?? (connecting ? 8 : 0);

  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      aria-label={ariaLabel}
      aria-busy={working}
      className={cn(
        "group relative grid size-[72px] place-items-center rounded-full border transition-all duration-300 outline-none",
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
          "border-primary/45 bg-primary/10 text-primary hover:bg-primary/15",
        // Idle = start
        !ready &&
          !error &&
          !working &&
          "border-primary/50 bg-primary text-primary-foreground shadow-lg shadow-primary/30 hover:bg-primary/90",
      )}
    >
      {disconnecting ? (
        <>
          <span
            aria-hidden
            className="pointer-events-none absolute inset-0 motion-reduce:hidden"
          >
            {magicSparkles.map((sparkle, index) => (
              <span
                key={index}
                className="connection-magic-collapse absolute top-1/2 left-1/2 motion-reduce:animate-none"
                style={
                  {
                    "--collapse-x": `${sparkle.x}px`,
                    "--collapse-y": `${sparkle.y}px`,
                    "--collapse-mid-x": `${sparkle.x * 0.28}px`,
                    "--collapse-mid-y": `${sparkle.y * 0.28}px`,
                    width: sparkle.size,
                    height: sparkle.size,
                    animationDelay: `${index * 35}ms`,
                  } as CSSProperties
                }
              />
            ))}
          </span>
          <svg
            aria-hidden
            viewBox="0 0 88 88"
            className="pointer-events-none absolute inset-[-8px] size-[88px] -rotate-90 overflow-visible text-primary"
          >
            <circle
              cx="44"
              cy="44"
              r={ringRadius}
              fill="none"
              stroke="currentColor"
              strokeWidth="3"
              strokeLinecap="round"
              strokeDasharray={ringCircumference}
              className="animate-connection-close-ring motion-reduce:animate-none"
            />
          </svg>
          <span className="relative z-10 grid place-items-center">
            <Power
              size={22}
              strokeWidth={1.9}
              className="animate-connection-close-power motion-reduce:animate-none"
            />
          </span>
        </>
      ) : connecting ? (
        <>
          <span
            aria-hidden
            className="pointer-events-none absolute inset-[-8px] animate-connection-breathe rounded-full bg-primary/10 blur-md motion-reduce:animate-none"
          />
          <span
            aria-hidden
            className="pointer-events-none absolute inset-0 motion-reduce:hidden"
          >
            {magicSparkles.map((sparkle, index) => (
              <span
                key={index}
                className="absolute"
                style={{
                  left: `calc(50% + ${sparkle.x}px)`,
                  top: `calc(50% + ${sparkle.y}px)`,
                }}
              >
                <span
                  className="connection-magic-star block animate-connection-magic-twinkle motion-reduce:animate-none"
                  style={{
                    width: sparkle.size,
                    height: sparkle.size,
                    animationDelay: `${sparkle.delay}s`,
                    animationDuration: `${sparkle.duration}s`,
                  }}
                />
              </span>
            ))}
          </span>
          <svg
            aria-hidden
            viewBox="0 0 88 88"
            className="pointer-events-none absolute inset-[-8px] size-[88px] -rotate-90 overflow-visible drop-shadow-[0_0_5px_color-mix(in_srgb,var(--primary)_45%,transparent)]"
          >
            <circle
              cx="44"
              cy="44"
              r={ringRadius}
              fill="none"
              stroke="currentColor"
              strokeWidth="3"
              className="opacity-15"
            />
            <circle
              cx="44"
              cy="44"
              r={ringRadius}
              fill="none"
              stroke="currentColor"
              strokeWidth="3"
              strokeLinecap="round"
              strokeDasharray={ringCircumference}
              strokeDashoffset={ringCircumference * (1 - progress / 100)}
              className="transition-[stroke-dashoffset] duration-700 ease-out"
            />
          </svg>
          <span
            aria-hidden
            className="pointer-events-none absolute inset-[-12px] animate-connection-magic-sweep motion-reduce:hidden"
          >
            <span className="connection-magic-comet absolute top-0 left-1/2 h-2 w-7 -translate-x-1/2 rounded-full" />
            <span className="connection-magic-star absolute top-[-2px] left-1/2 size-3 -translate-x-1/2" />
          </span>
          <span className="relative z-10 flex flex-col items-center gap-0.5">
            <Spinner className="size-[19px]" />
            <span className="inline-block w-[4ch] text-center font-mono text-[10px] font-semibold leading-none tabular-nums">
              {progress}%
            </span>
          </span>
        </>
      ) : working ? (
        <Spinner className="size-[30px]" />
      ) : error ? (
        <CircleAlert
          size={30}
          strokeWidth={1.8}
          className="transition-transform duration-300 group-hover:scale-110"
        />
      ) : (
        <Power
          size={30}
          strokeWidth={1.8}
          className={cn(
            "transition-transform duration-300 group-hover:scale-110 group-hover:rotate-6",
            ready && "drop-shadow-sm",
          )}
        />
      )}
      {ready ? (
        <>
          <span
            aria-hidden
            className="pointer-events-none absolute inset-0 motion-reduce:hidden"
          >
            {Array.from({ length: 8 }, (_, index) => (
              <span
                key={index}
                className="absolute inset-[-7px]"
                style={{ transform: `rotate(${index * 45}deg)` }}
              >
                <span
                  className="connection-magic-star connection-magic-star-success absolute top-0 left-1/2 size-2 animate-connection-magic-burst motion-reduce:animate-none"
                  style={{ animationDelay: `${index * 45}ms` }}
                />
              </span>
            ))}
          </span>
          <span
            aria-hidden
            className="pointer-events-none absolute inset-[-5px] animate-connection-ready rounded-full border border-success/35 motion-reduce:animate-none"
          />
          <span
            aria-hidden
            className="pointer-events-none absolute inset-[-9px] animate-connection-ready rounded-full border border-success/15 motion-reduce:animate-none [animation-delay:700ms]"
          />
        </>
      ) : null}
    </button>
  );
}
