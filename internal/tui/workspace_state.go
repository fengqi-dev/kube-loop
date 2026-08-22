package tui

import (
	"slices"
	"strings"
)

const (
	workspaceMinWidth  = 60
	workspaceMinHeight = 18
)

type workspaceResource string

const (
	resourceConnection workspaceResource = "connection"
	resourcePods       workspaceResource = "pods"
	resourceServices   workspaceResource = "services"
	resourceTasks      workspaceResource = "sessions"
	resourceProfiles   workspaceResource = "servers"
	resourceNamespaces workspaceResource = "namespaces"
)

type workspaceResourceDescriptor struct {
	id        workspaceResource
	title     string
	aliases   []string
	legacyTab tab
	hasTab    bool
	actions   string
}

var workspaceResourceRegistry = []workspaceResourceDescriptor{
	{
		id: resourceConnection, title: tabNameConnection,
		aliases: []string{"c", commandConnection}, legacyTab: tabConnection, hasTab: true,
		actions: "a add server  c connect  enter toggle  m mode  u uninstall service  L logout",
	},
	{
		id: resourcePods, title: "Pods",
		aliases:   []string{"w", "workload", "workloads", "po", resourceKindPod},
		legacyTab: tabWorkloads, hasTab: true,
		actions: "enter inspect  n namespace  f forward  s ssh",
	},
	{
		id: resourceServices, title: "Services",
		aliases: []string{"v", commandService, resourceKindService}, legacyTab: tabServices, hasTab: true,
		actions: "enter inspect  n namespace  f forward  x exchange  m mirror  p preview",
	},
	{
		id: resourceTasks, title: "Sessions",
		aliases:   []string{"s", "session", "fw", taskKindForward, "forwards"},
		legacyTab: tabTasks, hasTab: true,
		actions: "enter inspect  d stop  e rerun  y copy  C clear",
	},
	{
		id: resourceProfiles, title: "Servers", aliases: []string{"server"},
		actions: "enter select  a add  l login  L logout  d delete",
	},
	{
		id:      resourceNamespaces,
		title:   "Namespaces",
		aliases: []string{"n", "ns", commandNamespace},
		actions: "enter select",
	},
}

type workspaceInput int

const (
	workspaceInputNone workspaceInput = iota
	workspaceInputCommand
	workspaceInputFilter
)

type workspaceViewState struct {
	cursor int
	offset int
	filter string
	detail bool
}

type workspaceState struct {
	initialized    bool
	resource       workspaceResource
	loadGeneration uint64
	views          map[workspaceResource]workspaceViewState
	input          workspaceInput
	inputText      string
	inputBefore    string
	suggestion     int
	commands       []string
	commandPos     int
	history        []workspaceResource
	historyPos     int
	help           bool
	config         workspaceConfig
	warning        string
}

func newWorkspaceState(configPath string) workspaceState {
	config, warning := loadWorkspaceConfig(configPath)
	return workspaceState{
		initialized: true,
		resource:    resourceConnection,
		views:       map[workspaceResource]workspaceViewState{},
		commandPos:  -1,
		history:     []workspaceResource{resourceConnection},
		config:      config,
		warning:     warning,
	}
}

func builtinWorkspaceResource(value string) (workspaceResource, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, descriptor := range workspaceResourceRegistry {
		if value == string(descriptor.id) {
			return descriptor.id, true
		}
		if slices.Contains(descriptor.aliases, value) {
			return descriptor.id, true
		}
	}
	return "", false
}

func resolveWorkspaceResource(value string, config workspaceConfig) (workspaceResource, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if target, ok := config.Aliases[value]; ok {
		value = target
	}
	return builtinWorkspaceResource(value)
}

func workspaceDescriptor(id workspaceResource) workspaceResourceDescriptor {
	for _, descriptor := range workspaceResourceRegistry {
		if descriptor.id == id {
			return descriptor
		}
	}
	return workspaceResourceRegistry[0]
}

func (m *Model) ensureWorkspace() {
	if m.workspace.initialized {
		return
	}
	m.workspace = newWorkspaceState(m.state.configPath)
	if m.mode == viewLogin {
		m.workspace.resource = resourceProfiles
		m.workspace.history = []workspaceResource{resourceProfiles}
	}
}
