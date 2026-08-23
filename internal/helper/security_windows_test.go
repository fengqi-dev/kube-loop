//go:build windows

package helper

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func daclAllowsSID(descriptor *windows.SECURITY_DESCRIPTOR, sidText string) (bool, error) {
	want, err := windows.StringToSid(sidText)
	if err != nil {
		return false, fmt.Errorf("parse SID: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return false, fmt.Errorf("read DACL: %w", err)
	}
	if dacl == nil {
		return false, nil
	}
	for index := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return false, fmt.Errorf("read ACE %d: %w", index, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		got := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if windows.EqualSid(got, want) {
			return true, nil
		}
	}
	return false, nil
}
