import type { TranslationKey } from "@/i18n";

export type AppView =
  | "overview"
  | "clusters"
  | "workload"
  | "network"
  | "sessions"
  | "host-aliases"
  | "mcp"
  | "settings";

export const navKeys: Record<AppView, TranslationKey> = {
  overview: "nav.overview",
  clusters: "nav.clusters",
  workload: "nav.workload",
  network: "nav.network",
  sessions: "nav.sessions",
  "host-aliases": "nav.hostAliases",
  mcp: "nav.mcp",
  settings: "nav.settings",
};
