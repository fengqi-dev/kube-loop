package app

import (
	"context"
	"io"
	"sync"
	"time"

	clientauth "github.com/fengqi-dev/kube-loop/internal/client/auth"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientdiscovery "github.com/fengqi-dev/kube-loop/internal/client/discovery"
	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
	clientfiletransfer "github.com/fengqi-dev/kube-loop/internal/client/filetransfer"
	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	clientpodssh "github.com/fengqi-dev/kube-loop/internal/client/podssh"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
	clientpreview "github.com/fengqi-dev/kube-loop/internal/client/preview"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	clientremotesession "github.com/fengqi-dev/kube-loop/internal/client/remotesession"
	"github.com/fengqi-dev/kube-loop/internal/mcp"
	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
	"github.com/fengqi-dev/kube-loop/internal/update"
)

type App struct {
	ctx                       context.Context
	profiles                  *clientprofile.Store
	discovery                 *clientdiscovery.Client
	auth                      *clientauth.Client
	remote                    *clientremote.Client
	remoteSessions            *clientremotesession.Manager
	dataPlanes                *clientdataplane.Manager
	remoteExecs               *clientexec.Manager
	remoteFiles               *clientfiletransfer.Manager
	remoteSSH                 *clientpodssh.Manager
	remoteForwards            *clientportforward.Manager
	remoteExchanges           *clientexchange.Manager
	remoteMirrors             *clientmirror.Manager
	remotePreviews            *clientpreview.Manager
	credentials               credentials.Store
	mcp                       *mcp.Controller
	updater                   *update.Checker
	once                      sync.Once
	updateMu                  sync.RWMutex
	updateCheck               sync.Mutex
	updateState               update.Info
	inventoryWatchMu          sync.Mutex
	inventoryWatchLifecycle   sync.Mutex
	inventoryWatchWG          sync.WaitGroup
	inventoryWatchProfile     string
	inventoryWatchCancel      context.CancelFunc
	backgroundCancel          context.CancelFunc
	backgroundWG              sync.WaitGroup
	shutdownTimeout           time.Duration
	serverLoginMu             sync.Mutex
	serverLogin               *serverLoginAttempt
	trafficInspectionOutput   io.Closer
	trafficInspectionEvents   *trafficinspect.RingBufferSink
	trafficInspectionSwitch   *trafficinspect.SwitchableSink
	trafficInspectionSettings *trafficinspect.SettingsStore
	trafficInspectionProtobuf *trafficinspect.ProtobufSchemaStore
	trafficInspectionMu       sync.Mutex
	trafficInspectionReady    func() bool
	trafficInspectionCAPath   string
	trafficInspectionTrust    trafficinspect.TrustStore
}

type BootstrapData struct {
	Update         update.Info         `json:"update"`
	Platform       string              `json:"platform"`
	CoreVersion    string              `json:"coreVersion"`
	ServerProfiles clientprofile.State `json:"serverProfiles"`
}
