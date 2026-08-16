import type { SessionState } from "@/types";

export function isBusyPhase(phase: SessionState["phase"]) {
  return (
    phase === "checking" ||
    phase === "installing-gateway" ||
    phase === "discovering-network" ||
    phase === "starting-tunnel"
  );
}
