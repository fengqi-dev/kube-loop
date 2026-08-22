package tui

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

func (m Model) workspaceRawRows() []consoleRow {
	clone := m
	for i := range clone.console.filters {
		clone.console.filters[i] = ""
	}
	switch m.workspace.resource {
	case resourcePods:
		return clone.consoleWorkloadRows()
	case resourceServices:
		return clone.consoleServiceRows()
	case resourceTasks:
		return clone.consoleTaskRows()
	case resourceProfiles:
		rows := make([]consoleRow, 0, len(m.profiles.Profiles))
		for index, profile := range m.profiles.Profiles {
			status := "Ready"
			if profile.ID == m.profiles.ActiveProfileID {
				status = "Active"
			}
			detail := "Endpoint: " + profile.BaseURL +
				"\nTunnel path: " + profile.TunnelPath +
				"\nLast namespace: " + firstNonEmpty(profile.LastNamespace, "-")
			rows = append(rows, consoleRow{
				title: firstNonEmpty(profile.DisplayName, profile.ID),
				meta:  profile.BaseURL, status: status, kind: "profile", index: index, detail: detail,
			})
		}
		return rows
	case resourceNamespaces:
		rows := make([]consoleRow, 0, len(m.namespaces))
		for index, namespace := range m.namespaces {
			status := ""
			if namespace.Name == m.namespace {
				status = "Active"
			}
			rows = append(rows, consoleRow{
				title: namespace.Name, status: status, kind: commandNamespace, index: index,
				detail: "Namespace: " + namespace.Name,
			})
		}
		return rows
	case resourceConnection:
		return nil
	}
	return nil
}

type compiledWorkspaceFilter struct {
	regex   *regexp.Regexp
	fuzzy   string
	inverse bool
}

func compileWorkspaceFilter(raw string) (compiledWorkspaceFilter, error) {
	filter := strings.TrimSpace(raw)
	compiled := compiledWorkspaceFilter{}
	if strings.HasPrefix(filter, "!") {
		compiled.inverse = true
		filter = strings.TrimSpace(strings.TrimPrefix(filter, "!"))
	}
	if after, ok := strings.CutPrefix(filter, "-f"); ok {
		compiled.fuzzy = strings.ToLower(strings.TrimSpace(after))
		return compiled, nil
	}
	if filter == "" {
		return compiled, nil
	}
	regex, err := regexp.Compile("(?i)" + filter)
	if err != nil {
		return compiled, fmt.Errorf("invalid filter: %w", err)
	}
	compiled.regex = regex
	return compiled, nil
}

func (m Model) workspaceFilteredRows() []consoleRow {
	rows := m.workspaceRawRows()
	filter, err := compileWorkspaceFilter(m.workspaceView().filter)
	if err != nil {
		return rows
	}
	if filter.regex == nil && filter.fuzzy == "" {
		return rows
	}
	filtered := make([]consoleRow, 0, len(rows))
	for _, row := range rows {
		value := strings.ToLower(row.title + " " + row.meta + " " + row.status)
		matched := filter.regex != nil && filter.regex.MatchString(value)
		if filter.fuzzy != "" {
			matched = workspaceFuzzyMatch(row.title, filter.fuzzy)
		}
		if filter.inverse {
			matched = !matched
		}
		if matched {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func workspaceFuzzyMatch(value, query string) bool {
	queryRunes := []rune(strings.ToLower(query))
	if len(queryRunes) == 0 {
		return true
	}
	position := 0
	for _, valueRune := range strings.ToLower(value) {
		if valueRune == queryRunes[position] {
			position++
			if position == len(queryRunes) {
				return true
			}
		}
	}
	return false
}

func (m Model) workspaceView() workspaceViewState {
	view := m.workspace.views[m.workspace.resource]
	rows := m.workspaceFilteredRowsWithoutViewRecursion(view.filter)
	if len(rows) == 0 {
		view.cursor, view.offset = 0, 0
	} else {
		view.cursor = minInt(len(rows)-1, max(0, view.cursor))
	}
	return view
}

func (m Model) workspaceFilteredRowsWithoutViewRecursion(filterText string) []consoleRow {
	clone := m
	view := clone.workspace.views[clone.workspace.resource]
	view.filter = filterText
	clone.workspace.views[clone.workspace.resource] = view
	rows := clone.workspaceRawRows()
	filter, err := compileWorkspaceFilter(filterText)
	if err != nil || (filter.regex == nil && filter.fuzzy == "") {
		return rows
	}
	filtered := make([]consoleRow, 0, len(rows))
	for _, row := range rows {
		value := strings.ToLower(row.title + " " + row.meta + " " + row.status)
		matched := filter.regex != nil && filter.regex.MatchString(value)
		if filter.fuzzy != "" {
			matched = workspaceFuzzyMatch(value, filter.fuzzy)
		}
		if filter.inverse {
			matched = !matched
		}
		if matched {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func (m *Model) setWorkspaceView(view workspaceViewState) {
	if m.workspace.views == nil {
		m.workspace.views = map[workspaceResource]workspaceViewState{}
	}
	m.workspace.views[m.workspace.resource] = view
}

func (m *Model) moveWorkspaceCursor(delta int) {
	m.setWorkspaceCursor(m.workspaceView().cursor + delta)
}

func (m *Model) setWorkspaceCursor(cursor int) {
	rows := m.workspaceFilteredRows()
	view := m.workspaceView()
	if len(rows) == 0 {
		view.cursor, view.offset = 0, 0
		m.setWorkspaceView(view)
		return
	}
	view.cursor = minInt(len(rows)-1, max(0, cursor))
	page := m.workspacePageSize()
	if view.cursor < view.offset {
		view.offset = view.cursor
	}
	if view.cursor >= view.offset+page {
		view.offset = view.cursor - page + 1
	}
	m.setWorkspaceView(view)
}

func (m Model) workspacePageSize() int { return max(3, m.height-10) }

func (m Model) workspaceCommandCandidates() []string {
	prefix := strings.ToLower(strings.TrimSpace(m.workspace.inputText))
	set := map[string]struct{}{
		"connect":           {},
		"disconnect":        {},
		"help":              {},
		"logout":            {},
		"q":                 {},
		"uninstall-service": {},
	}
	for _, descriptor := range workspaceResourceRegistry {
		set[string(descriptor.id)] = struct{}{}
		for _, alias := range descriptor.aliases {
			set[alias] = struct{}{}
		}
	}
	for alias := range m.workspace.config.Aliases {
		set[alias] = struct{}{}
	}
	candidates := make([]string, 0, len(set))
	for candidate := range set {
		if prefix == "" || strings.HasPrefix(candidate, prefix) {
			candidates = append(candidates, candidate)
		}
	}
	slices.Sort(candidates)
	return candidates
}
