package install

import (
	"errors"
	"fmt"
)

type installRollback struct {
	binary fileRollback
	core   fileRollback
	auth   fileRollback
	token  fileRollback
}

func beginInstallRollback(binaryPath, corePath, authPath, tokenPath string) (*installRollback, error) {
	binary, err := snapshotFileForRollback(binaryPath)
	if err != nil {
		return nil, err
	}
	core, err := snapshotFileForRollback(corePath)
	if err != nil {
		binary.discard()
		return nil, err
	}
	auth, err := snapshotFileForRollback(authPath)
	if err != nil {
		binary.discard()
		core.discard()
		return nil, err
	}
	token, err := snapshotFileForRollback(tokenPath)
	if err != nil {
		binary.discard()
		core.discard()
		auth.discard()
		return nil, err
	}
	return &installRollback{binary: binary, core: core, auth: auth, token: token}, nil
}

func (r *installRollback) commit() {
	r.binary.discard()
	r.core.discard()
	r.auth.discard()
	r.token.discard()
}

func (r *installRollback) restore() error {
	var rollbackErrs []error
	if err := prepareBinaryInstall(); err != nil {
		rollbackErrs = append(rollbackErrs, fmt.Errorf("stop failed service: %w", err))
	}
	if err := r.binary.restore(); err != nil {
		rollbackErrs = append(rollbackErrs, fmt.Errorf("restore helper binary: %w", err))
	}
	if err := r.core.restore(); err != nil {
		rollbackErrs = append(rollbackErrs, fmt.Errorf("restore sing-box core: %w", err))
	}
	if err := r.auth.restore(); err != nil {
		rollbackErrs = append(rollbackErrs, fmt.Errorf("restore helper auth: %w", err))
	}
	if err := r.token.restore(); err != nil {
		rollbackErrs = append(rollbackErrs, fmt.Errorf("restore helper token: %w", err))
	}
	if r.binary.existed {
		if err := enableService(r.binary.path); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("restart previous helper: %w", err))
		}
	} else if err := disableService(); err != nil {
		rollbackErrs = append(rollbackErrs, fmt.Errorf("remove failed helper service: %w", err))
	}
	rollbackErr := errors.Join(rollbackErrs...)
	if rollbackErr == nil {
		r.commit()
	}
	return rollbackErr
}
