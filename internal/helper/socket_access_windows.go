//go:build windows

package helper

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func configureHelperSocketAccess(path, ownerSID string) error {
	if ownerSID == "" {
		return fmt.Errorf("Windows helper owner SID is required")
	}
	if _, err := windows.StringToSid(ownerSID); err != nil {
		return fmt.Errorf("parse Windows helper owner SID: %w", err)
	}
	return setWindowsFileDACL(
		path,
		"D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;"+ownerSID+")",
	)
}

func ConfigureElevatedExchangeAccess(path, ownerSID string) error {
	if _, err := windows.StringToSid(ownerSID); err != nil {
		return fmt.Errorf("parse Windows elevated request owner SID: %w", err)
	}
	return setWindowsFileDACL(
		path,
		"D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;"+ownerSID+")",
	)
}

func configureElevatedStagingAccess(path string) error {
	return setWindowsFileDACL(
		path,
		"D:P(A;OICI;GA;;;SY)(A;OICI;GA;;;BA)",
	)
}

func setWindowsFileDACL(path, sddl string) error {
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("build Windows security descriptor: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read Windows DACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return fmt.Errorf("set Windows DACL: %w", err)
	}
	return nil
}
