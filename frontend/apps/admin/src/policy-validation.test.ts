import { describe, expect, it } from "vitest";
import { roleSupportsNamespaces, validateAssignment } from "./policy-validation";
import type { Assignment, RoleDefinition } from "./types";

const customNamespaceRole: RoleDefinition = {
  id: "support-reader",
  displayName: "Support Reader",
  statements: [{ effect: "allow", capabilities: ["namespace.sessions.read"] }],
};

const assignment = (names: string[]): Assignment => ({
  id: "binding-1",
  roleId: customNamespaceRole.id,
  subject: { type: "principal", principalId: "principal-1" },
  scope: { type: "namespaces", names, labelSelectors: [] },
  managedBy: "platform",
});

describe("policy assignment validation", () => {
  it("detects built-in and custom Namespace-capable roles", () => {
    expect(roleSupportsNamespaces("namespace-admin", [])).toBe(true);
    expect(roleSupportsNamespaces(customNamespaceRole.id, [customNamespaceRole])).toBe(true);
    expect(roleSupportsNamespaces("auditor", [])).toBe(false);
  });

  it("requires a non-empty Namespace scope", () => {
    expect(validateAssignment(assignment([]), [customNamespaceRole])).toBe(
      "Enter at least one Namespace or Namespace label.",
    );
  });

  it("rejects invalid and duplicate Namespace names", () => {
    expect(validateAssignment(assignment(["Team_A"]), [customNamespaceRole])).toBe(
      "Namespace names must use valid Kubernetes DNS labels.",
    );
    expect(
      validateAssignment(assignment(["team-a", "team-a"]), [customNamespaceRole]),
    ).toBe("Namespace names must not contain duplicates.");
  });

  it("accepts a valid explicit Namespace association", () => {
    expect(
      validateAssignment(assignment(["team-a", "team-b"]), [customNamespaceRole]),
    ).toBeUndefined();
  });
});
