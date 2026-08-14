import { describe, expect, it } from "vitest";
import {
  authorizedView,
  canReadOverview,
  hasCapability,
  validOAuthRedirectURI,
  validOIDCIssuer,
} from "./ui-validation";

describe("validOIDCIssuer", () => {
  it.each([
    ["https://issuer.example/", true],
    ["https://issuer.example/tenant", true],
    ["http://issuer.example", false],
    ["https://issuer.example/?tenant=1", false],
    ["not-a-url", false],
  ])("validates %s", (value, expected) => {
    expect(validOIDCIssuer(value)).toBe(expected);
  });
});

describe("validOAuthRedirectURI", () => {
  it.each([
    ["https://client.example/callback", true],
    ["http://localhost:8080/callback", true],
    ["http://127.0.0.1:8080/callback", true],
    ["http://client.example/callback", false],
    ["https://user@client.example/callback", false],
    ["https://client.example/callback#token", false],
    ["not-a-url", false],
  ])("validates %s", (value, expected) => {
    expect(validOAuthRedirectURI(value)).toBe(expected);
  });
});

describe("hasCapability", () => {
  const capabilities = {
    capabilities: [] as string[],
    namespaceScopes: [
      {
        namespace: "default",
        capabilities: [
          "namespace.tasks.read",
          "namespace.authorization.read",
        ],
      },
    ],
  };

  it("accepts a namespace alternative for scoped navigation", () => {
    expect(
      hasCapability(capabilities, [
        "platform.sessions.read",
        "namespace.tasks.read",
      ]),
    ).toBe(true);
    expect(hasCapability(capabilities, "namespace.authorization.read")).toBe(
      true,
    );
    expect(hasCapability(capabilities, "platform.relays.read")).toBe(false);
  });

  it("falls back from a view whose capability was revoked", () => {
    expect(
      authorizedView(
        capabilities,
        "assignments",
        "platform.authorization.read",
      ),
    ).toBe("overview");
    expect(
      authorizedView(capabilities, "tasks", [
        "platform.tasks.read",
        "namespace.tasks.read",
      ]),
    ).toBe("tasks");
  });
});

describe("canReadOverview", () => {
  it("requires the platform overview capability", () => {
    expect(
      canReadOverview({ capabilities: ["platform.overview.read"] }),
    ).toBe(true);
    expect(
      canReadOverview({ capabilities: ["namespace.tasks.read"] }),
    ).toBe(false);
    expect(canReadOverview({ capabilities: [] })).toBe(false);
  });
});
