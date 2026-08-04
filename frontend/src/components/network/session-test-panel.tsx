import {
  Activity,
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Loader2,
  RefreshCw,
  XCircle,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { useI18n } from "@/i18n";
import { cn } from "@/lib/utils";

export type SessionTestStatus = "running" | "success" | "error";
export type SessionTestNodeRole = "user" | "accent" | "alt";

export interface SessionTestFlow {
  title: string;
  description: string;
  routes: Array<{
    label?: string;
    mode?: "roundtrip" | "shadow";
    branchFromPrevious?: number;
    testedSegments?: number[];
    nodes: Array<
      | string
      | {
          label: string;
          role: SessionTestNodeRole;
        }
    >;
  }>;
}

export function SessionTestPanel({
  flow,
  status,
  error,
  failure,
  onRetest,
  onClose,
}: {
  flow: SessionTestFlow | null;
  status: SessionTestStatus;
  error?: string;
  failure?: { route: number; segment: number };
  onRetest(): void | Promise<void>;
  onClose(): void;
}) {
  const { t } = useI18n();
  if (!flow) return null;
  const hasShadow = flow.routes.some((route) => route.mode === "shadow");
  const hasTopologyOnlyPath = flow.routes.some(
    (route) =>
      route.branchFromPrevious === undefined &&
      route.testedSegments !== undefined &&
      route.testedSegments.length < route.nodes.length - 1,
  );

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && status !== "running") onClose();
      }}
    >
      <DialogContent
        className="scrollbar-none max-h-[calc(100vh-2rem)] overflow-x-hidden overflow-y-auto sm:max-w-3xl"
        showCloseButton={status !== "running"}
      >
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Activity className="size-4 text-primary" />
            {t("network.sessionTestTitle")}
          </DialogTitle>
          <DialogDescription>
            {flow.title} · {flow.description}
          </DialogDescription>
        </DialogHeader>

        <div className="min-w-0 space-y-3 rounded-lg border bg-muted/20 p-3 sm:p-4">
          <div className="flex flex-wrap items-center justify-end gap-x-4 gap-y-1 text-[10px] font-medium text-muted-foreground">
            <span className="inline-flex items-center gap-1 text-success">
              <ArrowRight className="size-3.5" />
              {t("network.flowRequest")}
            </span>
            <span className="inline-flex items-center gap-1 text-primary">
              <ArrowLeft className="size-3.5" />
              {t("network.flowResponse")}
            </span>
            {hasShadow ? (
              <span className="inline-flex items-center gap-1 text-muted-foreground">
                <ArrowRight className="size-3.5" />
                {t("network.flowShadowCopy")}
              </span>
            ) : null}
          </div>
          {flow.routes.map((route, routeIndex) => {
            const previousRoute = flow.routes[routeIndex - 1];
            const testedSegments =
              route.testedSegments ??
              route.nodes.slice(0, -1).map((_, index) => index);
            if (
              route.branchFromPrevious !== undefined &&
              previousRoute &&
              route.nodes[0]
            ) {
              const rawNode = route.nodes[0];
              const node =
                typeof rawNode === "string"
                  ? { label: rawNode, role: "alt" as const }
                  : rawNode;
              return (
                <div
                  key={`${route.label ?? "branch"}-${routeIndex}`}
                  className="-mt-2"
                >
                  <div
                    className="grid"
                    style={{
                      gridTemplateColumns: flowGridColumns(previousRoute.nodes.length),
                    }}
                  >
                    <div
                      className="relative flex min-w-0 flex-col items-stretch"
                      style={{
                        gridColumn: `${route.branchFromPrevious * 2 + 1}`,
                      }}
                    >
                      {route.label ? (
                        <div className="absolute top-1 left-[calc(50%+1rem)] z-20 whitespace-nowrap text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
                          {route.label}
                        </div>
                      ) : null}
                      <VerticalTrafficConnector
                        status={status}
                        failure={failure}
                        route={routeIndex}
                        segment={0}
                        tested={testedSegments.includes(0)}
                        shadowLabel={t("network.flowShadowCopy")}
                        requestSequence={route.branchFromPrevious + 1}
                      />
                      <FlowNode
                        label={node.label}
                        role={node.role}
                        status={status}
                        tested={testedSegments.includes(0)}
                      />
                    </div>
                  </div>
                </div>
              );
            }
            return (
              <div
                key={`${route.label ?? "route"}-${routeIndex}`}
                className="space-y-2"
              >
                {route.label ? (
                  <div className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
                    {route.label}
                  </div>
                ) : null}
                <div
                  className="grid items-stretch py-2"
                  style={{ gridTemplateColumns: flowGridColumns(route.nodes.length) }}
                >
                  {route.nodes.map((rawNode, nodeIndex) => {
                  const node =
                    typeof rawNode === "string"
                      ? { label: rawNode, role: "alt" as const }
                      : rawNode;
                  const nodeIsTested =
                    testedSegments.includes(nodeIndex - 1) ||
                    testedSegments.includes(nodeIndex);
                  return (
                    <div key={`${node.label}-${nodeIndex}`} className="contents">
                      <FlowNode
                        label={node.label}
                        role={node.role}
                        status={status}
                        tested={nodeIsTested}
                      />
                      {nodeIndex < route.nodes.length - 1 ? (
                        <TrafficConnector
                          status={status}
                          failure={failure}
                          route={routeIndex}
                          segment={nodeIndex}
                          tested={testedSegments.includes(nodeIndex)}
                          requestSequence={nodeIndex + 1}
                          responseSequence={
                            route.nodes.length * 2 - 2 - nodeIndex
                          }
                          requestLabel={t("network.flowRequest")}
                          responseLabel={t("network.flowResponse")}
                        />
                      ) : null}
                    </div>
                  );
                  })}
                </div>
              </div>
            );
          })}
          {hasTopologyOnlyPath ? (
            <div className="text-[10px] leading-4 text-muted-foreground">
              {t("network.flowTopologyOnly")}
            </div>
          ) : null}
        </div>

        <div
          className={cn(
            "flex items-start gap-2 rounded-md border px-3 py-2.5 text-[12px]",
            status === "running" && "border-primary/25 bg-primary/5",
            status === "success" && "border-success/30 bg-success/8",
            status === "error" && "border-destructive/30 bg-destructive/8",
          )}
        >
          {status === "running" ? (
            <Loader2 className="mt-0.5 size-4 shrink-0 animate-spin text-primary" />
          ) : status === "success" ? (
            <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-success" />
          ) : (
            <XCircle className="mt-0.5 size-4 shrink-0 text-destructive" />
          )}
          <div className="min-w-0">
            <div className="font-medium">
              {status === "running"
                ? t("network.testingSession")
                : status === "success"
                  ? t("network.sessionTestPassed")
                  : t("network.sessionTestFailed")}
            </div>
            {error ? (
              <div className="mt-0.5 break-words text-muted-foreground">{error}</div>
            ) : null}
          </div>
        </div>
        <div className="flex justify-end">
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={status === "running"}
            onClick={() => void onRetest()}
          >
            <RefreshCw
              className={cn("size-3.5", status === "running" && "animate-spin")}
            />
            {t("network.retest")}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function FlowNode({
  label,
  role,
  status,
  tested,
}: {
  label: string;
  role: SessionTestNodeRole;
  status: SessionTestStatus;
  tested: boolean;
}) {
  return (
    <div
      className={cn(
        "flex min-h-14 min-w-0 items-center justify-center rounded-xl border px-2 py-2 text-center",
        "font-mono text-[11px] leading-4 shadow-xs transition-colors [overflow-wrap:anywhere]",
        nodeRoleClass(role),
        tested && status === "running" && "ring-1 ring-success/10",
        tested && status === "success" && "ring-1 ring-success/15",
      )}
    >
      {label}
    </div>
  );
}

function VerticalTrafficConnector({
  status,
  failure,
  route,
  segment,
  tested,
  shadowLabel,
  requestSequence,
}: {
  status: SessionTestStatus;
  failure?: { route: number; segment: number };
  route: number;
  segment: number;
  tested: boolean;
  shadowLabel: string;
  requestSequence: number;
}) {
  const state = segmentVisualState(status, failure, route, segment, tested);
  const tone = laneTone(state, "shadow");
  return (
    <div className="relative mx-auto h-12 w-8 shrink-0">
      <span className="sr-only">{shadowLabel}</span>
      <span
        className={cn(
          "absolute top-0 bottom-0 left-1/2 -translate-x-1/2 border-l-2 border-dashed",
          tone.track,
        )}
      />
      <span
        aria-hidden
        className="absolute top-1/2 left-[calc(50%+0.5rem)] z-20 -translate-y-1/2 rounded-full border bg-background px-1 font-mono text-[9px] leading-4 text-muted-foreground shadow-xs"
      >
        {requestSequence}
      </span>
      <ChevronDown
        aria-hidden
        className={cn(
          "absolute -bottom-1 left-1/2 z-10 size-3.5 -translate-x-1/2",
          tone.arrow,
        )}
        strokeWidth={2.4}
      />
      {state !== "pending" && state !== "neutral" ? (
        <span
          className={cn(
            "absolute left-1/2 size-2 -translate-x-1/2 rounded-full",
            state === "active" && "animate-session-flow-down top-0",
            state === "passed" && "bottom-0",
            state === "failed" && "top-1/2 -translate-y-1/2",
            tone.packet,
          )}
          style={{ animationDelay: `${(requestSequence - 1) * 300}ms` }}
        />
      ) : null}
    </div>
  );
}

function TrafficConnector({
  status,
  failure,
  route,
  segment,
  tested,
  requestSequence,
  responseSequence,
  requestLabel,
  responseLabel,
}: {
  status: SessionTestStatus;
  failure?: { route: number; segment: number };
  route: number;
  segment: number;
  tested: boolean;
  requestSequence: number;
  responseSequence: number;
  requestLabel: string;
  responseLabel: string;
}) {
  const state = segmentVisualState(status, failure, route, segment, tested);
  const requestTone = laneTone(state, "request");
  const responseTone = laneTone(state, "response");
  return (
    <div className="relative min-w-0 overflow-visible">
      <span className="sr-only">{`${requestLabel}; ${responseLabel}`}</span>
      <TrafficLane
        direction="right"
        position="top"
        state={state}
        tone={requestTone}
        delay={`${(requestSequence - 1) * 300}ms`}
        sequence={requestSequence}
      />
      <TrafficLane
        direction="left"
        position="bottom"
        state={state}
        tone={responseTone}
        delay={`${(responseSequence - 1) * 300}ms`}
        sequence={responseSequence}
      />
    </div>
  );
}

function TrafficLane({
  direction,
  position,
  state,
  tone,
  delay,
  sequence,
}: {
  direction: "left" | "right";
  position: "top" | "bottom";
  state: SegmentVisualState;
  tone: LaneTone;
  delay: string;
  sequence?: number;
}) {
  const vertical = position === "top" ? "top-[32%]" : "top-[68%]";
  const Arrow = direction === "right" ? ChevronRight : ChevronLeft;
  return (
    <>
      <span
        className={cn(
          "absolute right-0 left-0 border-t-2",
          vertical,
          "border-dashed",
          tone.track,
        )}
      />
      <Arrow
        aria-hidden
        className={cn(
          "absolute z-10 size-3.5 -translate-y-1/2",
          vertical,
          direction === "right" ? "-right-1" : "-left-1",
          tone.arrow,
        )}
        strokeWidth={2.4}
      />
      {sequence !== undefined ? (
        <span
          aria-hidden
          className={cn(
            "absolute left-1/2 z-20 -translate-x-1/2 rounded-full border bg-background px-1",
            "font-mono text-[9px] leading-4 text-muted-foreground shadow-xs",
            position === "top"
              ? "top-[32%] -translate-y-[calc(100%+2px)]"
              : "top-[68%] translate-y-[2px]",
          )}
        >
          {sequence}
        </span>
      ) : null}
      {state !== "pending" && state !== "neutral" ? (
        <span
          className={cn(
            "absolute size-2 -translate-y-1/2 rounded-full",
            vertical,
            packetPosition(state, direction),
            state === "active" &&
              (direction === "right"
                ? "animate-session-flow"
                : "animate-session-flow-reverse"),
            tone.packet,
          )}
          style={{ animationDelay: delay }}
        />
      ) : null}
    </>
  );
}

type SegmentVisualState = "active" | "passed" | "failed" | "pending" | "neutral";
type TrafficLaneTone = "request" | "response" | "shadow";
type LaneTone = { track: string; arrow: string; packet: string };

function segmentVisualState(
  status: SessionTestStatus,
  failure: { route: number; segment: number } | undefined,
  route: number,
  segment: number,
  tested: boolean,
) : SegmentVisualState {
  if (!tested) return "neutral";
  if (status === "running") return "active";
  if (status === "success") return "passed";
  if (!failure) return "pending";
  if (route < failure.route || (route === failure.route && segment < failure.segment)) {
    return "passed";
  }
  if (route === failure.route && segment === failure.segment) {
    return "failed";
  }
  return "pending";
}

function laneTone(state: SegmentVisualState, lane: TrafficLaneTone): LaneTone {
  if (state === "failed") {
    return {
      track: "border-destructive/60",
      arrow: "text-destructive",
      packet: "bg-destructive shadow-[0_0_6px_var(--destructive)]",
    };
  }
  if (state === "pending") {
    return {
      track: "border-border",
      arrow: "text-muted-foreground/40",
      packet: "bg-muted-foreground/40",
    };
  }
  if (state === "neutral") {
    if (lane === "response") {
      return {
        track: "border-primary/20",
        arrow: "text-primary/35",
        packet: "bg-primary/35",
      };
    }
    if (lane === "shadow") {
      return {
        track: "border-muted-foreground/20",
        arrow: "text-muted-foreground/35",
        packet: "bg-muted-foreground/35",
      };
    }
    return {
      track: "border-success/20",
      arrow: "text-success/35",
      packet: "bg-success/35",
    };
  }
  if (lane === "response") {
    return {
      track: "border-primary/55",
      arrow: "text-primary",
      packet: "bg-primary shadow-md shadow-primary/45",
    };
  }
  if (lane === "shadow") {
    return {
      track: "border-muted-foreground/45",
      arrow: "text-muted-foreground",
      packet: "bg-muted-foreground shadow-[0_0_5px_rgba(91,101,94,0.4)]",
    };
  }
  return {
    track: "border-success/55",
    arrow: "text-success",
    packet: "bg-success shadow-md shadow-success/50",
  };
}

function packetPosition(state: SegmentVisualState, direction: "left" | "right") {
  if (state === "failed") return "left-1/2 -translate-x-1/2";
  if (state === "passed") {
    return direction === "right" ? "right-0" : "left-0";
  }
  return direction === "right" ? "left-0" : "right-0";
}

function nodeRoleClass(role: SessionTestNodeRole) {
  if (role === "user") {
    return "border-foreground/15 bg-muted/45";
  }
  if (role === "accent") {
    return "border-success/35 bg-success/8";
  }
  return "border-success/20 bg-success/5";
}

function flowGridColumns(nodeCount: number) {
  return Array.from({ length: nodeCount * 2 - 1 }, (_, index) =>
    index % 2 === 0 ? "minmax(0, 1fr)" : "clamp(2rem, 5vw, 3rem)",
  ).join(" ");
}
