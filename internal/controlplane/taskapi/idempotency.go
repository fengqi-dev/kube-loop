package taskapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
)

const MaximumIdempotencyKeyBytes = 128

// IdempotencyKey requires exactly one bounded, log-safe key. The restricted
// alphabet keeps the value safe in headers, audit records and database keys.
func IdempotencyKey(request *http.Request) (string, *controlplaneapi.Error) {
	values := request.Header.Values(sessionapi.IdempotencyHeader)
	if len(values) != 1 {
		return "", invalidKey("Idempotency-Key must be provided once")
	}
	value := strings.TrimSpace(values[0])
	if value == "" || len(value) > MaximumIdempotencyKeyBytes {
		return "", invalidKey("Idempotency-Key is invalid")
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '.' || character == '_' || character == ':' {
			continue
		}
		return "", invalidKey("Idempotency-Key contains unsupported characters")
	}
	return value, nil
}

// RequestHash binds a create request to its owning Session and namespace.
func RequestHash(sessionID, namespace string, spec any) (string, error) {
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	payload := make([]byte, 0, len(sessionID)+len(namespace)+len(encoded)+2)
	payload = append(payload, sessionID...)
	payload = append(payload, '\n')
	payload = append(payload, namespace...)
	payload = append(payload, '\n')
	payload = append(payload, encoded...)
	return Hash(payload), nil
}

func Hash(encoded []byte) string {
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func Scope(taskType, identityID string) string {
	return "task:" + taskType + ":" + identityID
}

func invalidKey(message string) *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code: controlplaneapi.CodeInvalidArgument, Field: sessionapi.IdempotencyHeader, Message: message,
	}
}
