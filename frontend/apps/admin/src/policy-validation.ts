import type { Assignment, RoleDefinition } from "./types";

const namespacePattern = /^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$/;

export function roleSupportsNamespaces(
  roleId: string,
  roles: RoleDefinition[],
) {
  return (
    roleId.startsWith("namespace-") ||
    roles.some(
      (role) =>
        role.id === roleId &&
        role.statements.some((statement) =>
          statement.capabilities.some((capability) =>
            capability.startsWith("namespace."),
          ),
        ),
    )
  );
}

export function validateAssignment(
  assignment: Assignment,
  roles: RoleDefinition[],
): string | undefined {
  if (assignment.subject.type === "principal") {
    if (!assignment.subject.principalId?.trim())
      return "Select a user for every Principal assignment.";
  } else if (
    !assignment.subject.providerId?.trim() ||
    !assignment.subject.groupName?.trim()
  ) {
    return "Provider ID and group name are required for every group assignment.";
  }

  const names = assignment.scope.names || [];
  const selectors = assignment.scope.labelSelectors || [];
  const supportsNamespaces = roleSupportsNamespaces(assignment.roleId, roles);
  if (!supportsNamespaces) {
    if (
      assignment.scope.type !== "platform" ||
      names.length > 0 ||
      selectors.length > 0
    )
      return "Platform roles cannot contain Namespace scope values.";
    return undefined;
  }
  if (assignment.scope.type !== "namespaces")
    return "A Namespace-capable role must use Namespace scope.";
  if (names.length === 0 && selectors.length === 0)
    return "Enter at least one Namespace or Namespace label.";
  if (names.some((name) => !namespacePattern.test(name)))
    return "Namespace names must use valid Kubernetes DNS labels.";
  if (new Set(names).size !== names.length)
    return "Namespace names must not contain duplicates.";
  return undefined;
}
