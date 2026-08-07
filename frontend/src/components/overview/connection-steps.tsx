import { Check } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { useI18n } from "@/i18n";
import { cn } from "@/lib/utils";
import type { SessionState } from "@/types";

const order: SessionState["phase"][] = [
  "checking",
  "installing-gateway",
  "discovering-network",
  "starting-tunnel",
  "connected",
];

export function ConnectionSteps({ phase }: { phase: SessionState["phase"] }) {
  const { t } = useI18n();
  const current = order.indexOf(phase === "error" ? "checking" : phase);
  const completed = phase === "connected";
  const steps = [
    { label: t("step.access"), detail: t("step.accessDetail") },
    { label: t("step.gateway"), detail: t("step.gatewayDetail") },
    { label: t("step.discovery"), detail: t("step.discoveryDetail") },
    { label: t("step.tun"), detail: t("step.tunDetail") },
  ];

  return (
    <div className="mt-8 w-full max-w-[760px]">
      <div className="relative grid grid-cols-4">
        <div className="absolute top-[15px] right-[12.5%] left-[12.5%] h-[2px] overflow-hidden rounded-full bg-border">
          <div
            className="h-full origin-left rounded-full bg-primary transition-transform duration-700 ease-out"
            style={{
              transform: `scaleX(${
                completed
                  ? 1
                  : Math.max(0, Math.min(1, current / Math.max(steps.length - 1, 1)))
              })`,
            }}
          />
        </div>

        {steps.map((step, index) => {
          const done = current > index || completed;
          const active = current === index && !completed;
          return (
            <div
              key={step.label}
              className={cn(
                "relative flex flex-col items-center px-2 transition-opacity duration-500",
                !done && !active && "opacity-45",
              )}
            >
              <div className="relative grid size-8 place-items-center">
                {active && (
                  <span className="absolute inset-0 animate-ping rounded-full bg-primary/25" />
                )}
                {active && (
                  <span className="absolute inset-[-3px] animate-pulse rounded-full ring-2 ring-primary/40" />
                )}
                <div
                  className={cn(
                    "relative z-10 grid size-8 place-items-center rounded-full border-2 transition-all duration-500",
                    done &&
                      "scale-100 border-primary bg-primary text-primary-foreground",
                    active &&
                      "scale-110 border-primary bg-background text-primary shadow-sm",
                    !done &&
                      !active &&
                      "scale-95 border-border bg-background text-muted-foreground",
                  )}
                >
                  {done ? (
                    <Check
                      key={`done-${index}`}
                      size={14}
                      strokeWidth={2.6}
                      className="animate-step-check"
                    />
                  ) : active ? (
                    <Spinner className="size-3.5" />
                  ) : (
                    <span className="text-[11px] font-semibold">{index + 1}</span>
                  )}
                </div>
              </div>
              <div className="mt-2.5 text-center">
                <div
                  className={cn(
                    "text-[12px] font-medium tracking-tight",
                    active && "text-primary",
                    done && "text-foreground",
                    !done && !active && "text-muted-foreground",
                  )}
                >
                  {step.label}
                </div>
                <div className="mt-0.5 text-[10px] leading-snug text-muted-foreground">
                  {step.detail}
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
