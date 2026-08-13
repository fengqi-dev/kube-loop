package authorization

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
)

const CurrentVersion = 1

var (
	identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)
	namespacePattern  = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)
)

type Subject struct {
	ID       string
	Provider string
	Groups   []string
}

type Request struct {
	Operation       string
	Namespace       string
	ResourceKind    string
	ResourceName    string
	NamespaceLabels map[string]string
	LabelsAvailable bool
}

type Decision struct {
	Allowed bool
	RuleID  string
}

type Authorizer interface {
	Authorize(context.Context, Subject, Request) Decision
}

type Policy struct {
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
}

type Rule struct {
	ID            string   `json:"id"`
	Subjects      []string `json:"subjects,omitempty"`
	Groups        []string `json:"groups,omitempty"`
	Namespaces    []string `json:"namespaces"`
	Operations    []string `json:"operations"`
	ResourceKinds []string `json:"resourceKinds"`
}

type Engine struct {
	policy atomic.Pointer[compiledPolicy]
}

type compiledPolicy struct {
	rules []compiledRule
}

type compiledRule struct {
	id            string
	subjects      map[string]struct{}
	groups        map[string]struct{}
	namespaces    map[string]struct{}
	operations    map[string]struct{}
	resourceKinds map[string]struct{}
}

func New(policy Policy) (*Engine, error) {
	compiled, err := compile(policy)
	if err != nil {
		return nil, err
	}
	engine := &Engine{}
	engine.policy.Store(compiled)
	return engine, nil
}

func NewDenyAll() *Engine {
	engine, _ := New(Policy{Version: CurrentVersion, Rules: []Rule{}})
	return engine
}

func (engine *Engine) Update(policy Policy) error {
	compiled, err := compile(policy)
	if err != nil {
		return err
	}
	engine.policy.Store(compiled)
	return nil
}

func (engine *Engine) Authorize(_ context.Context, subject Subject, request Request) Decision {
	if engine == nil {
		return Decision{}
	}
	policy := engine.policy.Load()
	if policy == nil {
		return Decision{}
	}
	subject.ID = strings.TrimSpace(subject.ID)
	request.Operation = strings.ToLower(strings.TrimSpace(request.Operation))
	request.Namespace = strings.TrimSpace(request.Namespace)
	request.ResourceKind = strings.ToLower(strings.TrimSpace(request.ResourceKind))
	if subject.ID == "" || request.Operation == "" || request.ResourceKind == "" {
		return Decision{}
	}
	for _, rule := range policy.rules {
		if !matchesSelector(rule.subjects, subject.ID) || !matchesGroups(rule.groups, subject.Groups) ||
			!matchesSelector(rule.namespaces, namespaceSelector(request.Namespace)) ||
			!matchesSelector(rule.operations, request.Operation) ||
			!matchesSelector(rule.resourceKinds, request.ResourceKind) {
			continue
		}
		return Decision{Allowed: true, RuleID: rule.id}
	}
	return Decision{}
}

func compile(policy Policy) (*compiledPolicy, error) {
	if policy.Version == 0 {
		policy.Version = CurrentVersion
	}
	if policy.Version != CurrentVersion {
		return nil, fmt.Errorf("unsupported policy version %d", policy.Version)
	}
	seen := make(map[string]struct{}, len(policy.Rules))
	compiled := &compiledPolicy{rules: make([]compiledRule, 0, len(policy.Rules))}
	for index, rule := range policy.Rules {
		rule.ID = strings.TrimSpace(rule.ID)
		if !validRuleID(rule.ID) {
			return nil, fmt.Errorf("policy rule %d has an invalid ID", index)
		}
		if _, exists := seen[rule.ID]; exists {
			return nil, fmt.Errorf("policy rule %d has duplicate ID %q", index, rule.ID)
		}
		seen[rule.ID] = struct{}{}
		if len(rule.Subjects) == 0 && len(rule.Groups) == 0 {
			return nil, fmt.Errorf("policy rule %q requires subjects or groups", rule.ID)
		}
		if len(rule.Namespaces) == 0 {
			return nil, fmt.Errorf("policy rule %q namespaces must not be empty", rule.ID)
		}
		compiledRule := compiledRule{id: rule.ID}
		var err error
		if compiledRule.subjects, err = compileSelector(rule.Subjects, false); err != nil {
			return nil, fmt.Errorf("policy rule %q subjects: %w", rule.ID, err)
		}
		if compiledRule.groups, err = compileSelector(rule.Groups, false); err != nil {
			return nil, fmt.Errorf("policy rule %q groups: %w", rule.ID, err)
		}
		if compiledRule.namespaces, err = compileSelector(rule.Namespaces, true); err != nil {
			return nil, fmt.Errorf("policy rule %q namespaces: %w", rule.ID, err)
		}
		if compiledRule.operations, err = compileRestrictedSelector(rule.Operations); err != nil {
			return nil, fmt.Errorf("policy rule %q operations: %w", rule.ID, err)
		}
		if compiledRule.resourceKinds, err = compileRestrictedSelector(rule.ResourceKinds); err != nil {
			return nil, fmt.Errorf("policy rule %q resourceKinds: %w", rule.ID, err)
		}
		compiled.rules = append(compiled.rules, compiledRule)
	}
	return compiled, nil
}

func compileSelector(values []string, allowCluster bool) (map[string]struct{}, error) {
	if len(values) == 0 {
		return nil, nil
	}
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 256 || (!allowCluster && value == "$cluster") {
			return nil, errors.New("selector contains an invalid value")
		}
		if allowCluster && value != "*" && value != "$cluster" && !namespacePattern.MatchString(value) {
			return nil, fmt.Errorf("invalid namespace %q", value)
		}
		result[value] = struct{}{}
	}
	return result, nil
}

func compileRestrictedSelector(values []string) (map[string]struct{}, error) {
	if len(values) == 0 {
		return nil, errors.New("selector must not be empty")
	}
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "*" && !identifierPattern.MatchString(value) {
			return nil, fmt.Errorf("invalid value %q", value)
		}
		result[value] = struct{}{}
	}
	return result, nil
}

func validRuleID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-._", character) {
			continue
		}
		return false
	}
	return true
}

func matchesSelector(selector map[string]struct{}, value string) bool {
	if len(selector) == 0 {
		return true
	}
	if _, wildcard := selector["*"]; wildcard {
		return true
	}
	_, exists := selector[value]
	return exists
}

func matchesGroups(selector map[string]struct{}, groups []string) bool {
	if len(selector) == 0 {
		return true
	}
	if _, wildcard := selector["*"]; wildcard {
		return true
	}
	for _, group := range groups {
		if _, exists := selector[group]; exists {
			return true
		}
	}
	return false
}

func namespaceSelector(namespace string) string {
	if namespace == "" {
		return "$cluster"
	}
	return namespace
}
