import type { TranslationKey } from "@/i18n";
import type { SessionState } from "@/types";

export const phaseKeys: Record<SessionState["phase"], TranslationKey> = {
  idle: "phase.idle",
  checking: "phase.checking",
  "installing-gateway": "phase.installing-gateway",
  "discovering-network": "phase.discovering-network",
  "starting-tunnel": "phase.starting-tunnel",
  connected: "phase.connected",
  error: "phase.error",
};

export function isBusyPhase(phase: SessionState["phase"]) {
  return (
    phase === "checking" ||
    phase === "installing-gateway" ||
    phase === "discovering-network" ||
    phase === "starting-tunnel"
  );
}
