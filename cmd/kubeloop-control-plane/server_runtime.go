package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/maintenance"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionregistry"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
)

type serverRuntimeOptions struct {
	Context           context.Context
	Stop              context.CancelFunc
	Config            loadedControlPlaneConfig
	Logger            *slog.Logger
	Server            controlPlaneRuntimeServer
	RelayRegistry     *relayRegistryRuntime
	KubernetesConfig  controlplanekubernetes.Config
	SessionRecovery   controlPlaneWorker
	MaintenanceWorker controlPlaneWorker
	BindingRecovery   controlPlaneWorker
	SessionRuntime    controlPlaneSessionRuntime
}

type controlPlaneRuntimeServer interface {
	ListenAddress() string
	Serve(net.Listener) error
	Shutdown(context.Context) error
}

type controlPlaneWorker interface {
	Run(context.Context)
}

type controlPlaneSessionRuntime interface {
	Shutdown(context.Context) error
}

var (
	_ controlPlaneRuntimeServer  = (*controlplane.Server)(nil)
	_ controlPlaneWorker         = (*sessionregistry.Reconciler)(nil)
	_ controlPlaneWorker         = (*maintenance.Worker)(nil)
	_ controlPlaneWorker         = (*trafficbindingclient.Reconciler)(nil)
	_ controlPlaneSessionRuntime = (*sessionregistry.Registry)(nil)
)

func serveControlPlane(options serverRuntimeOptions) error {
	listener, err := (&net.ListenConfig{}).Listen(options.Context, "tcp", options.Server.ListenAddress())
	if err != nil {
		return fmt.Errorf("listen on %s: %w", options.Server.ListenAddress(), err)
	}
	var relayServer *http.Server
	var relayListener net.Listener
	if options.RelayRegistry != nil {
		rawRelayListener, listenErr := (&net.ListenConfig{}).Listen(
			options.Context,
			"tcp",
			options.RelayRegistry.listenAddress,
		)
		if listenErr != nil {
			_ = listener.Close()
			return fmt.Errorf("relay registry listen on %s: %w", options.RelayRegistry.listenAddress, listenErr)
		}
		relayListener = tls.NewListener(rawRelayListener, options.RelayRegistry.tlsConfig)
		relayServer = &http.Server{
			Handler: options.RelayRegistry.handler, ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second,
			MaxHeaderBytes: controlplane.DefaultMaxHeaderBytes,
		}
	}
	options.Logger.Info("kubeloop control plane started",
		"version", version, "commit", commit,
		"protocol_min", protocolMin, "protocol_max", protocolMax,
		"listen_address", listener.Addr().String(), "public_url", options.Config.Document.API.PublicURL,
		"kubernetes_impersonation", options.KubernetesConfig.Impersonation.Enabled,
	)
	if relayListener != nil {
		options.Logger.Info(
			"Relay Registry started",
			"listen_address",
			relayListener.Addr().String(),
			"transport",
			"TLS",
		)
	}
	serveCount := 1
	if relayServer != nil {
		serveCount++
	}
	errCh := make(chan error, serveCount)
	var backgroundWorkers sync.WaitGroup
	backgroundWorkers.Add(3)
	go func() {
		defer backgroundWorkers.Done()
		options.SessionRecovery.Run(options.Context)
	}()
	go func() {
		defer backgroundWorkers.Done()
		options.MaintenanceWorker.Run(options.Context)
	}()
	go func() {
		defer backgroundWorkers.Done()
		options.BindingRecovery.Run(options.Context)
	}()
	backgroundDone := make(chan struct{})
	go func() {
		backgroundWorkers.Wait()
		close(backgroundDone)
	}()
	go func() { errCh <- options.Server.Serve(listener) }()
	if relayServer != nil {
		go func() {
			err := relayServer.Serve(relayListener)
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			errCh <- err
		}()
	}

	var firstServeError error
	serveResults := 0
	select {
	case err := <-errCh:
		serveResults++
		firstServeError = err
		if firstServeError == nil {
			firstServeError = errors.New("control plane listener stopped unexpectedly")
		}
		options.Stop()
	case <-options.Context.Done():
	}
	options.Logger.Info("kubeloop control plane shutting down")
	shutdownContext, cancel := context.WithTimeout(
		context.WithoutCancel(options.Context),
		options.Config.ShutdownTimeout,
	)
	defer cancel()
	shutdownError := options.Server.Shutdown(shutdownContext)
	shutdownError = errors.Join(shutdownError, options.SessionRuntime.Shutdown(shutdownContext))
	if relayServer != nil {
		shutdownError = errors.Join(shutdownError, relayServer.Shutdown(shutdownContext))
	}
	serveWait := time.NewTimer(time.Second)
	defer serveWait.Stop()
	for serveResults < serveCount {
		select {
		case serveError := <-errCh:
			serveResults++
			if serveError != nil && firstServeError == nil {
				firstServeError = serveError
			}
		case <-shutdownContext.Done():
			options.Logger.Warn(
				"Control Plane serve loop exceeded shutdown deadline",
				"remaining",
				serveCount-serveResults,
			)
			shutdownError = errors.Join(shutdownError, shutdownContext.Err())
			serveResults = serveCount
		case <-serveWait.C:
			options.Logger.Warn(
				"Control Plane serve loop did not report shutdown",
				"remaining",
				serveCount-serveResults,
			)
			serveResults = serveCount
		}
	}
	select {
	case <-backgroundDone:
	case <-shutdownContext.Done():
		options.Logger.Warn("Control Plane background workers did not stop before shutdown deadline")
		shutdownError = errors.Join(shutdownError, shutdownContext.Err())
	}
	return errors.Join(firstServeError, shutdownError)
}
