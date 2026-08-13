export const managementBase =
  document
    .querySelector<HTMLMetaElement>('meta[name="kubeloop-management-path"]')
    ?.content.replace(/\/$/, "") || "/api/admin";
const authBase = "/oauth2";
export const csrfStorageKey = "kubeloop.admin.csrf";
const oidcStorageKey = "kubeloop.admin.oidc";
const deviceStorageKey = "kubeloop.admin.device";

export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
    public code: string,
    public requestId?: string,
  ) {
    super(message);
  }
}

export function resolveRequestPath(path: string) {
  const isRootEndpoint =
    path.startsWith(managementBase) ||
    path.startsWith(`${authBase}/`) ||
    path.startsWith("/.well-known/");
  return isRootEndpoint
    ? path
    : `${managementBase}${path.startsWith("/") ? path : `/${path}`}`;
}

export async function request<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const target = resolveRequestPath(path);
  const response = await fetch(target, {
    credentials: "same-origin",
    cache: "no-store",
    ...init,
    headers: { Accept: "application/json", ...init.headers },
  });
  let body: unknown = null;
  if (response.status !== 204) {
    try {
      body = await response.json();
    } catch {
      body = null;
    }
  }
  if (!response.ok) {
    const value = body as {
      error?: { message?: string; code?: string; requestId?: string };
      message?: string;
      code?: string;
    } | null;
    throw new ApiError(
      value?.error?.message ||
        value?.message ||
        `Request failed (${response.status})`,
      response.status,
      value?.error?.code || value?.code || "REQUEST_FAILED",
      value?.error?.requestId,
    );
  }
  if (response.status !== 204 && body === null)
    throw new ApiError(
      "The service returned an invalid response.",
      response.status,
      "INVALID_RESPONSE",
    );
  return body as T;
}

export function mutation<T>(
  path: string,
  method: string,
  body?: unknown,
  options: { etag?: number; idempotent?: boolean } = {},
) {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    "X-KubeLoop-CSRF": sessionStorage.getItem(csrfStorageKey) || "",
  };
  if (options.etag !== undefined) headers["If-Match"] = `"${options.etag}"`;
  if (options.idempotent) headers["Idempotency-Key"] = crypto.randomUUID();
  return request<T>(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

function base64url(bytes: Uint8Array) {
  let binary = "";
  bytes.forEach((value) => {
    binary += String.fromCharCode(value);
  });
  return btoa(binary)
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replaceAll("=", "");
}
function randomValue(bytes = 32) {
  const value = new Uint8Array(bytes);
  crypto.getRandomValues(value);
  return base64url(value);
}
async function pkceChallenge(verifier: string) {
  return base64url(
    new Uint8Array(
      await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier)),
    ),
  );
}
function deviceId() {
  let value = sessionStorage.getItem(deviceStorageKey);
  if (!value) {
    value = crypto.randomUUID();
    sessionStorage.setItem(deviceStorageKey, value);
  }
  return value;
}

export async function startOIDC(provider: string) {
  const transaction = {
    verifier: randomValue(),
    state: randomValue(),
    nonce: randomValue(),
    deviceId: deviceId(),
  };
  sessionStorage.setItem(oidcStorageKey, JSON.stringify(transaction));
  const authorize = new URL(`${authBase}/authorize`, location.origin);
  authorize.search = new URLSearchParams({
    response_type: "code",
    client_id: "kubeloop-management",
    redirect_uri: `${location.origin}${managementBase}/ui/callback`,
    scope: "openid profile email offline_access kubeloop.api",
    state: transaction.state,
    nonce: transaction.nonce,
    code_challenge: await pkceChallenge(transaction.verifier),
    code_challenge_method: "S256",
    provider,
  }).toString();
  location.assign(authorize.toString());
}

export async function finishOIDCCallback() {
  const query = new URLSearchParams(location.search);
  const code = query.get("code");
  const returnedState = query.get("state");
  if (!code && !returnedState) return false;
  const storedRaw = sessionStorage.getItem(oidcStorageKey);
  sessionStorage.removeItem(oidcStorageKey);
  history.replaceState({}, "", `${managementBase}/ui${location.hash}`);
  if (!storedRaw)
    throw new ApiError(
      "OIDC transaction state is missing.",
      400,
      "OIDC_STATE_MISSING",
    );
  const stored = JSON.parse(storedRaw) as {
    verifier?: string;
    state?: string;
    deviceId?: string;
  };
  if (
    !returnedState ||
    returnedState !== stored.state ||
    !stored.verifier ||
    !stored.deviceId
  )
    throw new ApiError(
      "OIDC transaction validation failed.",
      400,
      "OIDC_STATE_INVALID",
    );
  const form = new URLSearchParams({
    grant_type: "authorization_code",
    code: code!,
    code_verifier: stored.verifier,
    client_id: "kubeloop-management",
    redirect_uri: `${location.origin}${managementBase}/ui/callback`,
    device_id: stored.deviceId,
  });
  const tokens = await request<{
    access_token: string;
    refresh_token?: string;
  }>(`${authBase}/token`, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: form.toString(),
  });
  try {
    const issued = await request<{ csrfToken: string }>(
      `${managementBase}/sessions/token`,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${tokens.access_token}`,
        },
        body: "{}",
      },
    );
    sessionStorage.setItem(csrfStorageKey, issued.csrfToken);
  } finally {
    tokens.access_token = "";
    tokens.refresh_token = "";
  }
  return true;
}

export async function logout() {
  const csrf = sessionStorage.getItem(csrfStorageKey) || "";
  let failure: unknown;
  try {
    for (const operation of [
      () => request(`${authBase}/logout`, { method: "POST" }),
      () =>
        request(`${managementBase}/sessions/current`, {
          method: "DELETE",
          headers: { "X-KubeLoop-CSRF": csrf },
        }),
    ]) {
      try {
        await operation();
      } catch (error) {
        if (!(error instanceof ApiError) || error.status !== 401) {
          failure ||= error;
        }
      }
    }
  } finally {
    sessionStorage.removeItem(csrfStorageKey);
    sessionStorage.removeItem(oidcStorageKey);
  }
  if (failure) throw failure;
}
