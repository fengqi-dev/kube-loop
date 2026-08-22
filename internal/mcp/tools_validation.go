package mcp

import "strings"

func validateMutationIdentity(profileID, sessionID, namespace string) error {
	if strings.TrimSpace(profileID) == "" {
		return invalid("profileId", "profileId is required")
	}
	if strings.TrimSpace(sessionID) == "" {
		return invalid("sessionId", "sessionId is required")
	}
	if strings.TrimSpace(namespace) == "" {
		return invalid(resourceNamespace, "namespace is required")
	}
	return nil
}

func validTrafficType(value string) bool {
	return value == trafficTypeExchange || value == trafficTypeMirror ||
		value == trafficTypePreview || value == trafficTypePortForward
}
