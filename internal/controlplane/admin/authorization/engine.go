package authorization

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
)

var namespacePattern = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)

type Engine struct {
	snapshot atomic.Pointer[compiledSnapshot]
}

type compiledSnapshot struct {
	available bool
	groups    map[string]compiledGroup
}

type compiledGroup struct {
	administrator bool
	namespaces    map[string]struct{}
}

func New(snapshot Snapshot) (*Engine, error) {
	compiled, err := compileSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	engine := &Engine{}
	engine.snapshot.Store(compiled)
	return engine, nil
}

func NewDenyAll() (*Engine, error) {
	return New(Snapshot{Version: CurrentVersion, Groups: []GroupAccess{}})
}

func (engine *Engine) Available() bool {
	snapshot := engineSnapshot(engine)
	return snapshot != nil && snapshot.available
}

func (engine *Engine) Apply(snapshot Snapshot) error { return engine.Update(snapshot) }

func (engine *Engine) Update(snapshot Snapshot) error {
	if engine == nil {
		return errors.New("authorization engine is nil")
	}
	compiled, err := compileSnapshot(snapshot)
	if err != nil {
		return err
	}
	engine.snapshot.Store(compiled)
	return nil
}

func (engine *Engine) FailClosed() {
	if engine != nil {
		engine.snapshot.Store(&compiledSnapshot{})
	}
}

func (engine *Engine) Authorize(ctx context.Context, subject Subject, request Request) Decision {
	if ctx == nil {
		ctx = context.Background()
	}
	subject, request, valid := normalize(subject, request)
	if !valid {
		return Decision{Reason: ReasonInvalidRequest}
	}
	if subject.Authentication != AuthenticationNormal {
		return Decision{Reason: ReasonInvalidRequest}
	}
	return evaluate(engineSnapshot(engine), subject, request)
}

func evaluate(snapshot *compiledSnapshot, subject Subject, request Request) Decision {
	decision := Decision{Reason: ReasonNoMatchingAllow, Authentication: AuthenticationNormal}
	if snapshot == nil || !snapshot.available {
		return decision
	}
	for _, groupID := range subject.Groups {
		group, exists := snapshot.groups[groupID]
		if !exists {
			continue
		}
		if group.administrator {
			decision.Allowed, decision.Reason = true, ReasonAllowed
			decision.MatchingAllow = []Match{{GroupID: groupID, Namespace: request.Namespace}}
			return decision
		}
		if request.Namespace != "" {
			if _, allowed := group.namespaces[request.Namespace]; allowed {
				decision.Allowed, decision.Reason = true, ReasonAllowed
				decision.MatchingAllow = []Match{{GroupID: groupID, Namespace: request.Namespace}}
				return decision
			}
		}
	}
	return decision
}

func (engine *Engine) DryRun(ctx context.Context, subject Subject, request Request) Decision {
	subject.Authentication = AuthenticationNormal
	return engine.Authorize(ctx, subject, request)
}

func (engine *Engine) AuthorizedNamespaces(subject Subject) []string {
	result := make(map[string]struct{})
	if snapshot := engineSnapshot(engine); snapshot != nil {
		for _, groupID := range subject.Groups {
			group, exists := snapshot.groups[groupID]
			if !exists || group.administrator {
				continue
			}
			for namespace := range group.namespaces {
				result[namespace] = struct{}{}
			}
		}
	}
	values := make([]string, 0, len(result))
	for value := range result {
		values = append(values, value)
	}
	slices.Sort(values)
	return values
}

func compileSnapshot(snapshot Snapshot) (*compiledSnapshot, error) {
	if snapshot.Version == 0 {
		snapshot.Version = CurrentVersion
	}
	if snapshot.Version != CurrentVersion {
		return nil, fmt.Errorf("unsupported authorization policy version %d", snapshot.Version)
	}
	compiled := &compiledSnapshot{available: true, groups: make(map[string]compiledGroup, len(snapshot.Groups))}
	for _, item := range snapshot.Groups {
		item.GroupID = strings.TrimSpace(item.GroupID)
		if uuid.Validate(item.GroupID) != nil {
			return nil, errors.New("authorization group ID is invalid")
		}
		if _, duplicate := compiled.groups[item.GroupID]; duplicate {
			return nil, fmt.Errorf("duplicate authorization group %q", item.GroupID)
		}
		group := compiledGroup{administrator: item.Administrator, namespaces: make(map[string]struct{}, len(item.Namespaces))}
		for _, namespace := range item.Namespaces {
			namespace = strings.TrimSpace(namespace)
			if !namespacePattern.MatchString(namespace) {
				return nil, fmt.Errorf("authorization group %q has an invalid namespace", item.GroupID)
			}
			group.namespaces[namespace] = struct{}{}
		}
		compiled.groups[item.GroupID] = group
	}
	return compiled, nil
}

func normalize(subject Subject, request Request) (Subject, Request, bool) {
	subject.ID = strings.TrimSpace(subject.ID)
	if subject.Authentication == "" {
		subject.Authentication = AuthenticationNormal
	}
	if uuid.Validate(subject.ID) != nil {
		return Subject{}, Request{}, false
	}
	groups, err := compileSubjects(subject.Groups, "groups")
	if err != nil {
		return Subject{}, Request{}, false
	}
	subject.Groups = groups
	request.Capability = Capability(strings.TrimSpace(request.Key()))
	if _, exists := capabilitySet[request.Capability]; !exists {
		return Subject{}, Request{}, false
	}
	request.OrganizationID, request.Namespace, request.ResourceName = strings.TrimSpace(request.OrganizationID), strings.TrimSpace(request.Namespace), strings.TrimSpace(request.ResourceName)
	if request.OrganizationID != "" && uuid.Validate(request.OrganizationID) != nil || request.Namespace != "" && !namespacePattern.MatchString(request.Namespace) || len(request.ResourceName) > 512 {
		return Subject{}, Request{}, false
	}
	if strings.HasPrefix(string(request.Capability), "namespace.") != (request.Namespace != "") {
		return Subject{}, Request{}, false
	}
	return subject, request, true
}

func compileSubjects(values []string, field string) ([]string, error) {
	if len(values) > 512 {
		return nil, fmt.Errorf("%s exceeds 512 entries", field)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if uuid.Validate(value) != nil {
			return nil, fmt.Errorf("%s contains an invalid identity", field)
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, fmt.Errorf("%s contains duplicate values", field)
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	slices.Sort(result)
	return result, nil
}

func engineSnapshot(engine *Engine) *compiledSnapshot {
	if engine == nil {
		return nil
	}
	return engine.snapshot.Load()
}
