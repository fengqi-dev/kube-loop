package app

import (
	"context"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/helperd"
	"github.com/fengqi-dev/kube-loop/internal/helperinstall"
)

func productionDependencies() commandDependencies {
	return commandDependencies{
		install: func(options installOptions) error {
			return helperinstall.InstallFromCLI(
				options.source,
				options.token,
				options.uid,
				options.version,
				options.home,
				options.ownerSID,
				options.singBox,
			)
		},
		uninstall: helperinstall.UninstallFromCLI,
		run: func(ctx context.Context) error {
			auth, err := helper.ReadSystemAuth()
			if err != nil {
				return err
			}
			return helperd.RunService(ctx, helperd.NewServer(auth))
		},
		elevated: func(options elevatedOptions) error {
			return helperinstall.RunElevatedRequest(options.operation, options.request, options.result)
		},
	}
}
