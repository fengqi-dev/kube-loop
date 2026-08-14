// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  authenticationLostEvent,
  managementBase,
  mutation,
  oidcCallbackError,
  request,
  resolveRequestPath,
  waitForAuditExport,
} from "./api";

afterEach(() => vi.restoreAllMocks());

describe("resolveRequestPath", () => {
  it("places admin resources under the configured management base", () => {
    expect(resolveRequestPath("/overview")).toBe(`${managementBase}/overview`);
    expect(resolveRequestPath("providers")).toBe(`${managementBase}/providers`);
  });

  it("keeps management, OAuth, and discovery endpoints rooted", () => {
    expect(resolveRequestPath(`${managementBase}/bootstrap`)).toBe(`${managementBase}/bootstrap`);
    expect(resolveRequestPath("/oauth2/token")).toBe("/oauth2/token");
    expect(resolveRequestPath("/.well-known/kubeloop")).toBe("/.well-known/kubeloop");
  });
});

describe("mutation", () => {
  it("reuses an explicit idempotency key for related mutations", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(() =>
      Promise.resolve(new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })),
    );

    await mutation("/authorization/drafts", "POST", {}, {
      etag: 1,
      idempotencyKey: "draft-and-publish-key",
    });
    await mutation("/authorization/changes/change-1/publish", "POST", {}, {
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

    await expect(request("/overview")).rejects.toMatchObject({ status: 401 });

    expect(listener).toHaveBeenCalledOnce();
    removeEventListener(authenticationLostEvent, listener);
  });
});

describe("waitForAuditExport", () => {
  it("polls a pending job and returns the NDJSON file", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ state: "running" }), {
          status: 202,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response('{"action":"admin.audit/list"}\n', {
          status: 200,
          headers: { "Content-Type": "application/x-ndjson" },
        }),
      );

    const blob = await waitForAuditExport("job-1", {
      attempts: 2,
      delayMs: 0,
    });

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(await blob.text()).toContain("admin.audit/list");
  });
});
