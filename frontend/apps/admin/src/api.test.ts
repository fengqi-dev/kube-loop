// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { managementBase, resolveRequestPath } from "./api";

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
