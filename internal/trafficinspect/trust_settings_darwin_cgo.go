//go:build darwin && cgo

package trafficinspect

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

static SecCertificateRef kubeloopCertificate(const unsigned char *bytes, CFIndex length) {
	CFDataRef data = CFDataCreate(kCFAllocatorDefault, bytes, length);
	if (data == NULL) {
		return NULL;
	}
	SecCertificateRef certificate = SecCertificateCreateWithData(kCFAllocatorDefault, data);
	CFRelease(data);
	return certificate;
}

static OSStatus kubeloopTrustInstalled(
	const unsigned char *bytes,
	CFIndex length,
	Boolean *installed
) {
	*installed = false;
	SecCertificateRef certificate = kubeloopCertificate(bytes, length);
	if (certificate == NULL) {
		return errSecDecode;
	}
	CFArrayRef settings = NULL;
	OSStatus status = SecTrustSettingsCopyTrustSettings(
		certificate,
		kSecTrustSettingsDomainAdmin,
		&settings
	);
	CFRelease(certificate);
	if (status == errSecItemNotFound) {
		return errSecSuccess;
	}
	if (status != errSecSuccess) {
		return status;
	}
	if (settings != NULL && CFGetTypeID(settings) == CFArrayGetTypeID()) {
		CFIndex count = CFArrayGetCount(settings);
		if (count == 0) {
			*installed = true;
		}
		for (CFIndex index = 0; index < count && !*installed; index++) {
			CFTypeRef item = CFArrayGetValueAtIndex(settings, index);
			if (item == NULL || CFGetTypeID(item) != CFDictionaryGetTypeID()) {
				continue;
			}
			CFTypeRef value = CFDictionaryGetValue(
				(CFDictionaryRef)item,
				kSecTrustSettingsResult
			);
			if (value == NULL || CFGetTypeID(value) != CFNumberGetTypeID()) {
				continue;
			}
			SInt32 result = kSecTrustSettingsResultInvalid;
			if (CFNumberGetValue((CFNumberRef)value, kCFNumberSInt32Type, &result) &&
				(result == kSecTrustSettingsResultTrustRoot ||
				 result == kSecTrustSettingsResultTrustAsRoot)) {
				*installed = true;
			}
		}
	}
	if (settings != NULL) {
		CFRelease(settings);
	}
	return errSecSuccess;
}

static OSStatus kubeloopInstallTrust(const unsigned char *bytes, CFIndex length) {
	SecCertificateRef certificate = kubeloopCertificate(bytes, length);
	if (certificate == NULL) {
		return errSecDecode;
	}
	OSStatus status = SecTrustSettingsSetTrustSettings(
		certificate,
		kSecTrustSettingsDomainAdmin,
		NULL
	);
	CFRelease(certificate);
	return status;
}

static OSStatus kubeloopUninstallTrust(const unsigned char *bytes, CFIndex length) {
	SecCertificateRef certificate = kubeloopCertificate(bytes, length);
	if (certificate == NULL) {
		return errSecDecode;
	}
	OSStatus status = SecTrustSettingsRemoveTrustSettings(
		certificate,
		kSecTrustSettingsDomainAdmin
	);
	CFRelease(certificate);
	return status;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

type darwinNativeTrustSettings struct{}

func newDarwinTrustSettings() darwinTrustSettings { return darwinNativeTrustSettings{} }

func (darwinNativeTrustSettings) Installed(authority *Authority) (bool, error) {
	certificate, err := darwinAuthorityCertificate(authority)
	if err != nil {
		return false, err
	}
	var installed C.Boolean
	status := C.kubeloopTrustInstalled(
		(*C.uchar)(unsafe.Pointer(&certificate[0])),
		C.CFIndex(len(certificate)),
		&installed,
	)
	if status != C.errSecSuccess {
		return false, darwinTrustSettingsError("read", status)
	}
	return installed != 0, nil
}

func (darwinNativeTrustSettings) Install(authority *Authority) error {
	certificate, err := darwinAuthorityCertificate(authority)
	if err != nil {
		return err
	}
	status := C.kubeloopInstallTrust(
		(*C.uchar)(unsafe.Pointer(&certificate[0])),
		C.CFIndex(len(certificate)),
	)
	if status != C.errSecSuccess {
		return darwinTrustSettingsError("install", status)
	}
	return nil
}

func (darwinNativeTrustSettings) Uninstall(authority *Authority) error {
	certificate, err := darwinAuthorityCertificate(authority)
	if err != nil {
		return err
	}
	status := C.kubeloopUninstallTrust(
		(*C.uchar)(unsafe.Pointer(&certificate[0])),
		C.CFIndex(len(certificate)),
	)
	if status != C.errSecSuccess && status != C.errSecItemNotFound {
		return darwinTrustSettingsError("remove", status)
	}
	return nil
}

func darwinAuthorityCertificate(authority *Authority) ([]byte, error) {
	if authority == nil || authority.certificate.Leaf == nil || len(authority.certificate.Leaf.Raw) == 0 {
		return nil, errors.New("traffic inspection authority certificate is required")
	}
	return authority.certificate.Leaf.Raw, nil
}

func darwinTrustSettingsError(operation string, status C.OSStatus) error {
	return fmt.Errorf("%s admin trust settings: OSStatus %d", operation, int32(status))
}
