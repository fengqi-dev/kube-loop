package httpapi

import (
	"errors"

	adminbootstrap "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/bootstrap"
	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

type Option func(*handlerOptions) error

type handlerOptions struct {
	readAPI            *readAPI
	tokenAuthenticator TokenAuthenticator
	localUsers         *adminlocaluser.Service
	bootstrapService   *adminbootstrap.Service
	oauthRepositories  storage.Repositories
	oauthTransactions  storage.TransactionManager
}

func WithOAuthClients(
	repositories storage.Repositories,
	transactions storage.TransactionManager,
) Option {
	return func(options *handlerOptions) error {
		if repositories == nil || transactions == nil {
			return errors.New("oauth client API repositories are required")
		}
		options.oauthRepositories, options.oauthTransactions = repositories, transactions
		return nil
	}
}

func WithLocalUsers(service *adminlocaluser.Service) Option {
	return func(options *handlerOptions) error {
		if service == nil {
			return errors.New("management local user service is required")
		}
		if options.localUsers != nil {
			return errors.New(
				"management local user service is already configured",
			)
		}
		options.localUsers = service
		return nil
	}
}

func WithReadAPI(repositories storage.Repositories) Option {
	return func(options *handlerOptions) error {
		if repositories == nil {
			return errors.New("management API repositories are required")
		}
		if options.readAPI != nil {
			return errors.New("management read API is already configured")
		}
		options.readAPI = &readAPI{repositories: repositories}
		return nil
	}
}

func WithBootstrap(service *adminbootstrap.Service) Option {
	return func(options *handlerOptions) error {
		if service == nil {
			return errors.New("iam bootstrap service is required")
		}
		if options.bootstrapService != nil {
			return errors.New("iam bootstrap service is already configured")
		}
		options.bootstrapService = service
		return nil
	}
}
