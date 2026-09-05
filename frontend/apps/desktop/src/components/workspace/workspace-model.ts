export type ResourceIdentity = { profileId: string; namespace: string; kind: string; id: string };
export type ResourceSelection = { key: string; label: string; namespace: string };
export function resourceKey(resource: ResourceIdentity): string {
  return JSON.stringify([resource.profileId, resource.namespace, resource.kind, resource.id]);
}
export function resolveSplit(value: unknown): number {
  const number = typeof value === "number" ? value : typeof value === "string" && value.trim() ? Number(value) : NaN;
  return Number.isFinite(number) ? Math.max(25, Math.min(75, number)) : 55;
}
export function resourceAvailability(key: string, keys: string[], settled: boolean): "ready" | "missing" | "loading" {
  return keys.includes(key) ? "ready" : settled ? "missing" : "loading";
}
