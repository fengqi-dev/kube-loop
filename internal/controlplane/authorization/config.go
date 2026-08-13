package authorization

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

const maxPolicyBytes = 1 << 20

func Load(path string) (Policy, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Policy{Version: CurrentVersion, Rules: []Rule{}}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return Policy{}, errors.New("open Gateway policy configuration")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxPolicyBytes+1))
	if err != nil {
		return Policy{}, errors.New("read Gateway policy configuration")
	}
	if len(raw) > maxPolicyBytes {
		return Policy{}, errors.New("Gateway policy configuration exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var policy Policy
	if err := decoder.Decode(&policy); err != nil {
		return Policy{}, errors.New("decode Gateway policy configuration")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Policy{}, errors.New("Gateway policy configuration must contain one JSON document")
	}
	return Normalize(policy)
}

// Normalize validates and applies defaults to a Gateway authorization policy.
func Normalize(policy Policy) (Policy, error) {
	if _, err := compile(policy); err != nil {
		return Policy{}, err
	}
	if policy.Version == 0 {
		policy.Version = CurrentVersion
	}
	if policy.Rules == nil {
		policy.Rules = []Rule{}
	}
	return policy, nil
}
