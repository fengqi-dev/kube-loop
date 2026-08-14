package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	adminhttpserver "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/httpserver"
	adminoperations "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/operations"
	adminprovider "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/provider"
	adminconfig "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/managementconfig"
	controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/maintenance"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionregistry"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
)

type serverRuntimeOptions struct {
	Context                context.Context
	Stop                   context.CancelFunc
	Config                 loadedControlPlaneConfig
	Logger                 *slog.Logger
	Store                  *controlplanestorage.Store
	Server                 *controlplane.Server
	ManagementServer       *adminhttpserver.Server
	RelayRegistry          *relayRegistryRuntime
	KubernetesConfig       controlplanekubernetes.Config
	ManagedProviderRuntime *adminprovider.Runtime
	ManagementPolicyLoader *adminconfig.PolicyLoader
	SessionRecovery        *sessionregistry.Reconciler
	ManagementOperations   *adminoperations.Service
	MaintenanceWorker      *maintenance.Worker
	BindingRecovery        *trafficbindingclient.Reconciler
	SessionRuntime         *sessionregistry.Registry
}

func serveControlPlane(options serverRuntimeOptions) {
	listener, err := net.Listen("tcp", options.Server.ListenAddress())
	if err != nil {
		_ = options.Store.Close()
		options.Logger.Error("listen failed", "address", options.Server.ListenAddress(), "error", err)
		os.Exit(1)
	}
	managementListener, err := net.Listen("tcp", options.ManagementServer.ListenAddress())
	if err != nil {
		_ = listener.Close()
		_ = options.Store.Close()
		options.Logger.Error("Management Plane listen failed", "address", options.ManagementServer.ListenAddress(), "error", err)
		os.Exit(1)
	}
	var relayServer *http.Server
	var relayListener net.Listener
	if options.RelayRegistry != nil {
		rawRelayListener, listenErr := net.Listen("tcp", options.RelayRegistry.listenAddress)
		if listenErr != nil {
			_ = listener.Close()
			_ = managementListener.Close()
			_ = options.Store.Close()
			options.Logger.Error("Relay Registry listen failed", "address", options.RelayRegistry.listenAddress, "error", listenErr)
			os.Exit(1)
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
	options.Logger.Info("Management Plane started", "listen_address", managementListener.Addr().String(), "public_url", options.Config.Document.Management.PublicURL)
	if relayListener != nil {
		options.Logger.Info("Relay Registry started", "listen_address", relayListener.Addr().String(), "transport", "mTLS")
	}
	go options.ManagedProviderRuntime.Run(options.Context)
	serveCount := 2
	if relayServer != nil {
		serveCount++
	}
	errCh := make(chan error, serveCount)
	var backgroundWorkers sync.WaitGroup
	backgroundWorkers.Add(5)
	go func() {
		defer backgroundWorkers.Done()
		options.ManagementPolicyLoader.Run(options.Context)
	}()
	go func() {
		defer backgroundWorkers.Done()
		options.SessionRecovery.Run(options.Context)
	}()
	go func() {
		defer backgroundWorkers.Done()
		options.ManagementOperations.Run(options.Context)
	}()
	go func() {
		defer backgroundWorkers.Done()
		options.MaintenanceWorker.Run(options.Context)
	}()
	go func() {
		defer backgroundWorkers.Done()
		options.BindingRecovery.Run(options.Context)
	}()
	go func() { errCh <- options.Server.Serve(listener) }()
	go func() { errCh <- options.ManagementServer.Serve(managementListener) }()
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
			firstServeError = errors.New("Control Plane listener stopped unexpectedly")
		}
		options.Stop()
	case <-options.Context.Done():
	}
	options.Logger.Info("kubeloop control plane shutting down")
	shutdownContext, cancel := context.WithTimeout(context.Background(), options.Config.ShutdownTimeout)
	defer cancel()
	shutdownError := options.Server.Shutdown(shutdownContext)
	shutdownError = errors.Join(shutdownError, options.ManagementServer.Shutdown(shutdownContext))
	shutdownError = errors.Join(shutdownError, options.SessionRuntime.Shutdown(shutdownContext))
	if relayServer != nil {
		shutdownError = errors.Join(shutdownError, relayServer.Shutdown(shutdownContext))
	}
	for serveResults < serveCount {
		select {
		case serveError := <-errCh:
			serveResults++
			if serveError != nil && firstServeError == nil {
				firstServeError = serveError
			}
		case <-time.After(time.Second):
			options.Logger.Warn("Control Plane serve loop did not report shutdown", "remaining", serveCount-serveResults)
			serveResults = serveCount
		}
	}
	backgroundWorkers.Wait()
	closeError := options.Store.Close()
	if finalError := errors.Join(firstServeError, shutdownError, closeError); finalError != nil {
		options.Logger.Error("Control Plane stopped with an error", "error", finalError)
		os.Exit(1)
	}
}
