import type { TranslationKey } from "@/i18n";

export type AppView =
  | "overview"
  | "clusters"
  | "connections"
  | "workload"
  | "network"
  | "host-aliases"
  | "traffic-inspection"
  | "mcp"
  | "settings";

export const navKeys: Record<AppView, TranslationKey> = {
  overview: "nav.overview",
  clusters: "nav.clusters",
  connections: "nav.connections",
  workload: "nav.workload",
  network: "nav.network",
  "host-aliases": "nav.hostAliases",
  "traffic-inspection": "nav.trafficInspection",
  mcp: "nav.mcp",
  settings: "nav.settings",
};
