package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/gateway"
	"github.com/fengqi-dev/kube-loop/internal/gateway/relayagent"
	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
)

type gatewayRuntimeOptions struct {
	Context       context.Context
	Logger        *log.Logger
	ListenAddress string
	Path          string
	Listener      net.Listener
	Handler       http.Handler
	Gateway       gatewayDrainRuntime
	Admissions    gatewayAdmissionRuntime
	Control       gatewayControlRuntime
	DrainTimeout  time.Duration
	Serve         gatewayServeFunc
}

type gatewayDrainRuntime interface {
	BeginDrain()
	Drain(context.Context) error
}

type gatewayAdmissionRuntime interface {
	BeginDrain()
}

type gatewayControlRuntime interface {
	Drain(context.Context) error
}

type gatewayServeFunc func(context.Context, net.Listener, http.Handler) error

var (
	_ gatewayDrainRuntime     = (*gateway.Server)(nil)
	_ gatewayAdmissionRuntime = (*websocketmux.Handler)(nil)
	_ gatewayControlRuntime   = (*relayagent.Agent)(nil)
)

func serveGateway(options gatewayRuntimeOptions) error {
	errCh := make(chan error, 1)
	httpContext, cancelHTTP := context.WithCancel(context.WithoutCancel(options.Context))
	go func() {
		options.Logger.Printf("WebSocket Gateway listening on %s%s", options.ListenAddress, options.Path)
		errCh <- options.Serve(httpContext, options.Listener, options.Handler)
	}()

	serveFinished := false
	var serveError error
	select {
	case err := <-errCh:
		serveFinished = true
		serveError = err
	case <-options.Context.Done():
	}
	options.Logger.Printf("Gateway draining for up to %s", options.DrainTimeout)
	options.Gateway.BeginDrain()
	options.Admissions.BeginDrain()
	drainReportContext, cancelDrainReport := context.WithTimeout(
		context.WithoutCancel(options.Context),
		5*time.Second,
	)
	if err := options.Control.Drain(drainReportContext); err != nil {
		options.Logger.Printf("report Data Plane drain failed: %v", err)
	}
	cancelDrainReport()
	drainContext, cancelDrain := context.WithTimeout(
		context.WithoutCancel(options.Context),
		options.DrainTimeout,
	)
	drainErr := options.Gateway.Drain(drainContext)
	cancelDrain()
	if drainErr != nil {
		options.Logger.Printf("Gateway drain deadline reached: %v", drainErr)
	}
	cancelHTTP()
	if !serveFinished {
		if err := <-errCh; err != nil {
			serveError = err
		}
	}
	if serveError != nil {
		return fmt.Errorf("gateway listener stopped: %w", serveError)
	}
	options.Logger.Print("Gateway stopped")
	return nil
}
