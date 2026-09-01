package trafficbindingclient

import (
	"strings"

	"github.com/google/uuid"
)

var trafficSessionIDNamespace = uuid.MustParse("86c6eb9e-85fa-4aac-8f46-b0f663d88acd")

// TaskIDForIdempotency derives the business Session ID without persisting a
// separate idempotency record. The TrafficBinding itself remains the only
// durable record for the Session.
func TaskIDForIdempotency(sessionID, taskType, identityID, key string) string {
	parts := []string{
		strings.TrimSpace(sessionID),
		strings.TrimSpace(taskType),
		strings.TrimSpace(identityID),
		strings.TrimSpace(key),
	}
	return uuid.NewSHA1(trafficSessionIDNamespace, []byte(strings.Join(parts, "\x00"))).String()
}
