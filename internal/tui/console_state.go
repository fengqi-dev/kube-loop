package tui

import "github.com/charmbracelet/lipgloss"

const (
	consoleMinWidth  = 60
	consoleMinHeight = 18
	consoleWideWidth = 110
)

type consoleOverlay int

const (
	overlayNone consoleOverlay = iota
	overlayHelp
	overlayNamespace
	overlayConfirmTask
	overlayConfirmProfile
	overlayConfirmDisconnect
	overlayConfirmServiceUninstall
	overlayProfiles
	overlayProfileAdd
)

type consoleFocus int

const (
	focusList consoleFocus = iota
	focusDetail
)

type consoleInput int

const (
	inputNone consoleInput = iota
	inputCommand
	inputFilter
)

const (
	taskFilterAll = iota
	taskFilterForward
	taskFilterTraffic
	taskFilterSSH
	taskFilterExec
	taskFilterCount
)

type consoleViewState struct {
	cursor       int
	offset       int
	detailOffset int
	focus        consoleFocus
}

type consoleState struct {
	views       [tabCount]consoleViewState
	overlay     consoleOverlay
	query       string
	overlayPos  int
	returnTo    consoleOverlay
	taskFilter  int
	pendingTask int
	inputMode   consoleInput
	inputText   string
	filters     [tabCount]string
}

type consoleRow struct {
	title  string
	meta   string
	copy   string
	status string
	detail string
	kind   string
	index  int
}

var (
	consoleInk     = lipgloss.Color("#E8E4D9")
	consoleDim     = lipgloss.Color("#8D98A5")
	consoleCanvas  = lipgloss.Color("#081018")
	consolePanel   = lipgloss.Color("#101C27")
	consoleBorder  = lipgloss.Color("#29404F")
	consoleAmber   = lipgloss.Color("#FFB454")
	consoleTeal    = lipgloss.Color("#4FD6BE")
	consoleDanger  = lipgloss.Color("#FF6B6B")
	consolePrimary = lipgloss.Color("#167D8D")

	consoleTitle     = lipgloss.NewStyle().Foreground(consoleCanvas).Background(consoleAmber).Bold(true).Padding(0, 1)
	consoleSubtle    = lipgloss.NewStyle().Foreground(consoleDim)
	consoleValue     = lipgloss.NewStyle().Foreground(consoleInk)
	consoleSection   = lipgloss.NewStyle().Foreground(consoleAmber).Bold(true)
	consoleNav       = lipgloss.NewStyle().Foreground(consoleDim).Padding(0, 1)
	consoleNavActive = lipgloss.NewStyle().Foreground(consoleCanvas).Background(consoleAmber).Bold(true).Padding(0, 1)
	consoleCard      = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(consoleBorder).
				Background(consolePanel).
				Padding(1, 2)
	consoleDetail     = consoleCard.BorderForeground(consolePrimary)
	consoleOverlayBox = consoleCard.BorderForeground(consoleAmber).Padding(1, 3)
	consoleSelected   = lipgloss.NewStyle().Foreground(consoleCanvas).Background(consoleTeal).Bold(true)
	consoleButton     = lipgloss.NewStyle().
				Foreground(consoleCanvas).
				Background(consolePrimary).
				Bold(true).
				Padding(0, 1)
	consoleDangerButton = consoleButton.Background(consoleDanger)
	consoleError        = lipgloss.NewStyle().Foreground(consoleDanger).Bold(true)
	consoleOK           = lipgloss.NewStyle().Foreground(consoleTeal).Bold(true)
	consoleCmdPrompt    = lipgloss.NewStyle().
				Foreground(consoleCanvas).
				Background(consoleAmber).
				Bold(true).
				Padding(0, 1)
	consoleFilterPrompt = lipgloss.NewStyle().
				Foreground(consoleCanvas).
				Background(consolePrimary).
				Bold(true).
				Padding(0, 1)
	consoleCmdText = lipgloss.NewStyle().Foreground(consoleInk).Bold(true)
)
