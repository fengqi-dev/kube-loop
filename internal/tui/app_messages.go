package tui

import (
	"time"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	clientpodssh "github.com/fengqi-dev/kube-loop/internal/client/podssh"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
	clientpreview "github.com/fengqi-dev/kube-loop/internal/client/preview"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

type profilesLoadedMsg struct{ state clientprofile.State }
type authStatusMsg struct {
	session AuthSession
	err     error
}
type loginResultMsg struct {
	session   AuthSession
	err       error
	cancelled bool
}
type logoutResultMsg struct{ err error }
type namespacesLoadedMsg struct {
	namespaces []clientremote.Namespace
	err        error
}
type namespaceChangedMsg struct {
	namespace string
	resource  workspaceResource
}
type podsLoadedMsg struct {
	pods []clientremote.Pod
	err  error
}
type servicesLoadedMsg struct {
	services []clientremote.Service
	err      error
}
type dataPlaneStatusMsg struct{ status clientdataplane.Status }
type dataPlaneSessionConnectedMsg struct {
	profile clientprofile.Profile
	session clientremote.Session
	mode    clientdataplane.Mode
}
type dataPlaneSOCKSConnectedMsg struct{ profileID string }
type dataPlaneConnectedMsg struct {
	status clientdataplane.Status
	stage  string
	err    error
}
type dataPlaneDisconnectedMsg struct {
	status clientdataplane.Status
	err    error
}
type dataPlaneModeMsg struct {
	status   clientdataplane.Status
	previous clientdataplane.Mode
	err      error
}
type portForwardsLoadedMsg struct{ forwards []clientportforward.Info }
type podSSHLoadedMsg struct{ endpoints []clientpodssh.Info }
type trafficOperationsLoadedMsg struct {
	exchanges []clientexchange.Info
	mirrors   []clientmirror.Info
	previews  []clientpreview.Info
}
type profileSavedMsg struct {
	profile clientprofile.Profile
	err     error
}
type profileDeletedMsg struct {
	state clientprofile.State
	err   error
}
type portForwardStartedMsg struct {
	info clientportforward.Info
	err  error
}
type trafficOperationStartedMsg struct {
	kind, target string
	err          error
}
type podSSHStartedMsg struct {
	info clientpodssh.Info
	err  error
}
type execStartedMsg struct {
	task    clientremote.ExecTask
	command string
	err     error
}
type execEventMsg struct{ event clientexec.Event }
type taskStoppedMsg struct {
	kind, id string
	err      error
}
type tickMsg struct{ time time.Time }
