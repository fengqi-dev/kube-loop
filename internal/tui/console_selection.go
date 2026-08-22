package tui

import "strings"

func consoleRowMatchesFilter(row consoleRow, filter string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(row.title), filter) ||
		strings.Contains(strings.ToLower(row.meta), filter) ||
		strings.Contains(strings.ToLower(row.status), filter)
}

// selectedConsoleRow returns the filtered row under the cursor for the active tab.
func (m Model) selectedConsoleRow() (consoleRow, bool) {
	var rows []consoleRow
	switch m.activeTab {
	case tabWorkloads:
		rows = m.consoleWorkloadRows()
	case tabServices:
		rows = m.consoleServiceRows()
	case tabTasks:
		rows = m.consoleTaskRows()
	case tabConnection, tabCount:
		return consoleRow{}, false
	}
	if m.cursor < 0 || m.cursor >= len(rows) {
		return consoleRow{}, false
	}
	return rows[m.cursor], true
}

func (m *Model) switchConsoleTab(delta int) {
	next := (int(m.activeTab) + delta + int(tabCount)) % int(tabCount)
	m.selectConsoleTab(tab(next))
}

func (m *Model) selectConsoleTab(next tab) {
	m.console.views[m.activeTab].cursor = m.cursor
	m.activeTab = next
	m.cursor = m.console.views[next].cursor
	m.err, m.status = "", ""
}

func (m *Model) moveConsoleSelection(delta int) {
	state := &m.console.views[m.activeTab]
	if state.focus == focusDetail {
		state.detailOffset = max(0, state.detailOffset+delta)
		return
	}
	m.setConsoleCursor(state.cursor + delta)
}

func (m *Model) setConsoleCursor(cursor int) {
	total := m.consoleItemCount()
	state := &m.console.views[m.activeTab]
	if total == 0 {
		state.cursor, state.offset, m.cursor = 0, 0, 0
		return
	}
	state.cursor = minInt(total-1, max(0, cursor))
	page := m.consolePageSize()
	if state.cursor < state.offset {
		state.offset = state.cursor
	}
	if state.cursor >= state.offset+page {
		state.offset = state.cursor - page + 1
	}
	state.detailOffset = 0
	m.cursor = state.cursor
}

func (m Model) consolePageSize() int { return max(3, m.height-12) }

func (m Model) consoleListGeometry() (int, int) {
	rowY := 7
	if m.width < consoleWideWidth {
		rowY = 8
	}
	stride := 1
	contentWidth := m.width - 2
	if m.width >= consoleWideWidth {
		contentWidth = m.width - 25
	}
	listWidth := contentWidth
	if contentWidth >= 84 {
		listWidth = contentWidth*3/5 - 1
	}
	if listWidth >= 52 {
		stride = 2
	}
	return rowY, stride
}

func (m Model) consoleDetailStartX() int {
	contentStart, contentWidth := 0, m.width-2
	if m.width >= consoleWideWidth {
		contentStart, contentWidth = 23, m.width-25
	}
	if contentWidth < 84 {
		return m.width
	}
	return contentStart + contentWidth*3/5
}
