// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  authenticationLostEvent,
  managementBase,
  mutation,
  oidcCallbackError,
  request,
  resolveRequestPath,
} from "./api";

afterEach(() => {
  sessionStorage.clear();
  vi.restoreAllMocks();
});

describe("resolveRequestPath", () => {
  it("places admin resources under the configured management base", () => {
    expect(resolveRequestPath("/users")).toBe(`${managementBase}/users`);
    expect(resolveRequestPath("providers")).toBe(`${managementBase}/providers`);
  });

  it("keeps management, OAuth, and discovery endpoints rooted", () => {
    expect(resolveRequestPath(`${managementBase}/bootstrap`)).toBe(`${managementBase}/bootstrap`);
    expect(resolveRequestPath("/oauth2/token")).toBe("/oauth2/token");
    expect(resolveRequestPath("/.well-known/kubeloop")).toBe("/.well-known/kubeloop");
  });
});

describe("mutation", () => {
  it("uses the CSRF cookie when a new tab has no session storage token", async () => {
    vi.spyOn(document, "cookie", "get").mockReturnValue(
      "__Host-kubeloop-admin-csrf=cookie-token",
    );
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    await mutation("/users", "POST", {});

    expect(fetchMock).toHaveBeenCalledWith(
      `${managementBase}/users`,
      expect.objectContaining({
        headers: expect.objectContaining({
          "X-KubeLoop-CSRF": "cookie-token",
        }),
      }),
    );
  });

  it("uses the HTTP CSRF cookie when TLS is disabled", async () => {
    vi.spyOn(document, "cookie", "get").mockReturnValue(
      "kubeloop-admin-csrf=http-cookie-token",
    );
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    await mutation("/users", "POST", {});

    expect(fetchMock).toHaveBeenCalledWith(
      `${managementBase}/users`,
      expect.objectContaining({
        headers: expect.objectContaining({
          "X-KubeLoop-CSRF": "http-cookie-token",
        }),
      }),
    );
  });

  it("reuses an explicit idempotency key for related mutations", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(() =>
      Promise.resolve(new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })),
    );

    await mutation("/oauth-clients", "POST", {}, {
      etag: 1,
      idempotencyKey: "draft-and-publish-key",
    });
    await mutation("/oauth-clients/client-1/secret", "POST", {}, {
      etag: 1,
      idempotencyKey: "draft-and-publish-key",
    });

    expect(fetchMock).toHaveBeenCalledTimes(2);
    for (const [, init] of fetchMock.mock.calls) {
      expect(init).toEqual(
        expect.objectContaining({
          headers: expect.objectContaining({
            "Idempotency-Key": "draft-and-publish-key",
          }),
        }),
      );
    }
  });
});

describe("authentication failure", () => {
  it("maps a cancelled OAuth authorization to a stable UI error", () => {
    expect(oidcCallbackError("access_denied")).toMatchObject({
      status: 400,
      code: "OIDC_ACCESS_DENIED",
      message: "Authorization was cancelled.",
    });
    expect(oidcCallbackError(null)).toBeUndefined();
  });

  it("notifies the app when a management request loses authentication", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ message: "authentication failed" }), {
        status: 401,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const listener = vi.fn();
    addEventListener(authenticationLostEvent, listener);

    await expect(request("/users")).rejects.toMatchObject({ status: 401 });

    expect(listener).toHaveBeenCalledOnce();
    removeEventListener(authenticationLostEvent, listener);
  });
});
