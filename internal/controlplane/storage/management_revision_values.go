package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	ManagementConfigurationPolicy   = "policy"
	ManagementConfigurationProvider = "provider"
	ManagementPolicyID              = "global"

	RevisionValidationDraft   = "draft"
	RevisionValidationValid   = "valid"
	RevisionValidationInvalid = "invalid"

	ChangeStatusDraft         = "draft"
	ChangeStatusValidated     = "validated"
	ChangeStatusPublished     = "published"
	ChangeStatusRejected      = "rejected"
	ChangeStatusRolledBack    = "rolled-back"
	ManagementActorBreakGlass = "break-glass"
)

func normalizeManagementActor(id, authenticationType string) (string, string, error) {
	id = strings.TrimSpace(id)
	authenticationType = strings.TrimSpace(authenticationType)
	switch authenticationType {
	case "normal", "bootstrap":
		if validateUUID(id, "management actor") != nil {
			return "", "", errors.New("management actor is invalid")
		}
	case "break-glass":
		if id != ManagementActorBreakGlass {
			return "", "", errors.New("break-glass management actor is invalid")
		}
	default:
		return "", "", errors.New("management actor authentication type is invalid")
	}
	return id, authenticationType, nil
}

var (
	managementIdentifier = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,126}[A-Za-z0-9])?$`)
	dns1123Label         = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)
)

func canonicalJSONObject(value json.RawMessage, field string) (json.RawMessage, error) {
	if len(value) == 0 {
		return nil, errors.New(field + " is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, errors.New(field + " must be a JSON object")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New(field + " must contain one JSON object")
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, errors.New("encode " + field)
	}
	return canonical, nil
}

func jsonSHA256(value json.RawMessage) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func validSHA256Hex(value string) bool {
	if len(value) != sha256Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func normalizeManagementIdentifier(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if !managementIdentifier.MatchString(value) {
		return "", errors.New(field + " is invalid")
	}
	return value, nil
}

func normalizeReason(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) {
		return "", errors.New("management change reason must be between 1 and 1024 UTF-8 bytes")
	}
	return value, nil
}

func validRevisionState(value string) bool {
	switch value {
	case RevisionValidationDraft, RevisionValidationValid, RevisionValidationInvalid:
		return true
	default:
		return false
	}
}

func normalizeConfigurationIdentity(kind, id string) (string, string, error) {
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	switch kind {
	case ManagementConfigurationPolicy:
		if id != ManagementPolicyID {
			return "", "", errors.New("policy configuration ID must be global")
		}
	case ManagementConfigurationProvider:
		var err error
		if id, err = normalizeManagementIdentifier(id, "provider configuration ID"); err != nil {
			return "", "", err
		}
	default:
		return "", "", errors.New("management configuration type is invalid")
	}
	return kind, id, nil
}

func normalizeSecretAliases(value json.RawMessage) (json.RawMessage, error) {
	canonical, err := canonicalJSONObject(value, "provider Secret aliases")
	if err != nil {
		return nil, err
	}
	var aliases map[string]any
	if err := json.Unmarshal(canonical, &aliases); err != nil {
		return nil, errors.New("decode provider Secret aliases")
	}
	for use, rawAlias := range aliases {
		alias, ok := rawAlias.(string)
		if !ok {
			return nil, errors.New("provider Secret alias values must be strings")
		}
		if _, err := normalizeManagementIdentifier(use, "provider Secret use"); err != nil {
			return nil, err
		}
		if _, err := normalizeManagementIdentifier(alias, "provider Secret alias"); err != nil {
			return nil, err
		}
	}
	return canonical, nil
}

func normalizeNamespaces(value json.RawMessage) (json.RawMessage, []string, error) {
	if len(value) == 0 {
		value = json.RawMessage(`[]`)
	}
	var namespaces []string
	if err := json.Unmarshal(value, &namespaces); err != nil || namespaces == nil || len(namespaces) > 256 {
		return nil, nil, errors.New("assignment namespaces must be a JSON string array with at most 256 items")
	}
	seen := make(map[string]struct{}, len(namespaces))
	for index, namespace := range namespaces {
		namespace = strings.TrimSpace(namespace)
		if !dns1123Label.MatchString(namespace) {
			return nil, nil, errors.New("assignment namespace is invalid")
		}
		if _, exists := seen[namespace]; exists {
			return nil, nil, errors.New("assignment namespace is duplicated")
		}
		seen[namespace] = struct{}{}
		namespaces[index] = namespace
	}
	sort.Strings(namespaces)
	canonical, _ := json.Marshal(namespaces)
	return canonical, namespaces, nil
}

func normalizeAssignmentValues(value json.RawMessage, field string, requireUUID bool) (json.RawMessage, []string, error) {
	if len(value) == 0 {
		value = json.RawMessage(`[]`)
	}
	var values []string
	if err := json.Unmarshal(value, &values); err != nil || values == nil || len(values) > 512 {
		return nil, nil, errors.New("assignment " + field + " must be a JSON string array with at most 512 items")
	}
	seen := make(map[string]struct{}, len(values))
	for index, item := range values {
		item = strings.TrimSpace(item)
		if item == "" || item == "*" || item == "$cluster" || len(item) > 512 || strings.ContainsAny(item, "\x00\r\n") {
			return nil, nil, errors.New("assignment " + field + " contains an invalid exact value")
		}
		if requireUUID && validateUUID(item, "management assignment subject") != nil {
			return nil, nil, errors.New("management assignment subject is invalid")
		}
		if _, exists := seen[item]; exists {
			return nil, nil, errors.New("assignment " + field + " contains a duplicate value")
		}
		seen[item] = struct{}{}
		values[index] = item
	}
	sort.Strings(values)
	canonical, _ := json.Marshal(values)
	return canonical, values, nil
}

func validChangeTransition(current, next string) bool {
	switch current {
	case ChangeStatusDraft:
		return next == ChangeStatusValidated || next == ChangeStatusRejected
	case ChangeStatusValidated:
		return next == ChangeStatusPublished || next == ChangeStatusRejected
	case ChangeStatusPublished:
		return next == ChangeStatusRolledBack
	default:
		return false
	}
}
