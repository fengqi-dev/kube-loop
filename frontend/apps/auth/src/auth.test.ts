import { describe, expect, it } from "vitest";
import { authenticationError } from "./auth-error";

describe("authorization UI contract", () => {
  it("persists only the locale", () => {
    const persistedKeys = ["kubeloop.locale"];
    expect(persistedKeys).toEqual(["kubeloop.locale"]);
    expect(persistedKeys.join(" ")).not.toMatch(
      /transaction|csrf|password/,
    );
  });

  it("posts only to same-origin OAuth endpoints", () => {
    const endpoints = ["/oauth2/login/local"];
    expect(endpoints.every((endpoint) => endpoint.startsWith("/oauth2/"))).toBe(
      true,
    );
  });

  it("shows a generic localized error for rejected credentials", () => {
    expect(authenticationError("zh-CN", "authentication_failed")).toBe(
      "登录失败，请检查凭据后重试。",
    );
    expect(authenticationError("en-US", "authentication_failed")).toBe(
      "Sign-in failed. Check your credentials and try again.",
    );
    expect(authenticationError("en-US", "unknown")).toBe("");
  });
});
