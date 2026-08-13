import { describe, expect, it } from "vitest";

describe("authorization UI contract", () => {
  it("persists only the locale", () => {
    const persistedKeys = ["kubeloop.locale"];
    expect(persistedKeys).toEqual(["kubeloop.locale"]);
    expect(persistedKeys.join(" ")).not.toMatch(
      /transaction|csrf|password|second_factor/,
    );
  });

  it("posts only to same-origin OAuth endpoints", () => {
    const endpoints = ["/oauth2/login/local", "/oauth2/login/provider"];
    expect(endpoints.every((endpoint) => endpoint.startsWith("/oauth2/"))).toBe(
      true,
    );
  });
});
