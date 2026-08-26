package main

import (
	"context"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
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
			return helper.RunService(ctx, helper.NewServer(auth))
		},
		elevated: func(options elevatedOptions) error {
			return helperinstall.RunElevatedRequest(options.operation, options.request, options.result)
		},
		trustCertificate: func(ctx context.Context, options trustCertificateOptions) error {
			return helperinstall.ManageDarwinTrustFromCLI(ctx, options.operation, options.certificate)
		},
	}
}
