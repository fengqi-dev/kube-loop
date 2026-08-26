//go:build !darwin

package install

import "context"

func installCertificateWithoutServiceChange(context.Context, []byte) error { return nil }
