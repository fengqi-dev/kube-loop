//go:build windows

package helper

import (
	"context"

	"golang.org/x/sys/windows/svc"
)

type windowsService struct {
	server *Server
}

func (s *windowsService) Execute(
	_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status,
) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- s.server.Serve(ctx) }()
	changes <- svc.Status{
		State:   svc.Running,
		Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPreShutdown,
	}
	for {
		select {
		case req, ok := <-requests:
			if !ok {
				cancel()
				<-errCh
				return false, 0
			}
			switch req.Cmd {
			case svc.Interrogate:
				changes <- req.CurrentStatus
			case svc.Stop, svc.Shutdown, svc.PreShutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				<-errCh
				changes <- svc.Status{State: svc.Stopped}
				return false, 0
			case svc.Pause, svc.Continue, svc.ParamChange,
				svc.NetBindAdd, svc.NetBindRemove, svc.NetBindEnable, svc.NetBindDisable,
				svc.DeviceEvent, svc.HardwareProfileChange, svc.PowerEvent, svc.SessionChange:
				// The helper supports only lifecycle and status commands.
			}
		case err := <-errCh:
			if err != nil {
				return true, 1
			}
			return false, 0
		}
	}
}

// RunService starts the helper, using the Windows service manager when appropriate.
func RunService(ctx context.Context, server *Server) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !isService {
		return server.Serve(ctx)
	}
	return svc.Run(ServiceNameWin(), &windowsService{server: server})
}
