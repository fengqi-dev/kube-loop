export function validOIDCIssuer(value: string) {
  try {
    const parsed = new URL(value);
    return (
      parsed.protocol === "https:" &&
      !!parsed.host &&
      !parsed.username &&
      !parsed.password &&
      !parsed.search &&
      !parsed.hash
    );
  } catch {
    return false;
  }
}

export function validOAuthRedirectURI(value: string) {
  try {
    const parsed = new URL(value);
    const loopback = ["localhost", "127.0.0.1", "[::1]", "::1"].includes(
      parsed.hostname,
    );
    return (
      !!parsed.host &&
      !parsed.username &&
      !parsed.password &&
      !parsed.hash &&
      (parsed.protocol === "https:" ||
        (parsed.protocol === "http:" && loopback))
    );
  } catch {
    return false;
  }
}

export function hasCapability(
  capabilities: { capabilities: string[]; namespaceScopes: Array<{ capabilities?: string[] }> },
  required?: string | string[],
) {
  if (!required) return true;
  const alternatives = Array.isArray(required) ? required : [required];
  return alternatives.some(
    (capability) =>
      capabilities.capabilities.includes(capability) ||
      capabilities.namespaceScopes.some((scope) =>
        scope.capabilities?.includes(capability),
      ),
  );
}

export function authorizedView<T extends string>(
  capabilities: Parameters<typeof hasCapability>[0],
  current: T,
  required?: string | string[],
): T | "overview" {
  return hasCapability(capabilities, required) ? current : "overview";
}

export function canReadOverview(capabilities: { capabilities: string[] }) {
  return capabilities.capabilities.includes("platform.overview.read");
}
