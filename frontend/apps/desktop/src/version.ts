/** Injected at build time via VITE_APP_VERSION (release tag). Defaults to dev. */
export const appVersion =
  (import.meta.env.VITE_APP_VERSION as string | undefined)?.trim() || "dev";
