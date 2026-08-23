package distribution

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

const (
	downloadBaseURL  = ProjectURL + "/releases/download/" + Version
	singBoxBinary    = "sing-box"
	singBoxBinaryWin = "sing-box.exe"
	windowsGOOS      = "windows"
	cronetDLL        = "libcronet.dll"
	wintunDLL        = "wintun.dll"
)

type releaseAsset struct {
	Name   string
	SHA256 string
}

var releaseAssets = map[string]releaseAsset{
	"darwin/amd64": {
		Name:   "sing-box-1.13.19-darwin-amd64.tar.gz",
		SHA256: "31ee722237d95774e101fbffeae6be6776249c5f7db229ad8ff00b45b22e6a00",
	},
	"darwin/arm64": {
		Name:   "sing-box-1.13.19-darwin-arm64.tar.gz",
		SHA256: "23bf191906f2dfc9f00e9f0092f274f3426ba9377327e903ff94e636b64d0997",
	},
	"linux/amd64": {
		Name:   "sing-box-1.13.19-linux-amd64.tar.gz",
		SHA256: "ef88a9e577d474210867bd708933d042e9b70106529df2656182c9db90106aa1",
	},
	"linux/arm64": {
		Name:   "sing-box-1.13.19-linux-arm64.tar.gz",
		SHA256: "7fe3597a95a3c5ad67477b1d7653b9ce097e0be7c676758eba1fcf558f353d57",
	},
	"windows/amd64": {
		Name:   "sing-box-1.13.19-windows-amd64.zip",
		SHA256: "e011a4def2f5e2b143ed54adb2b1a20a6be407806ab4442f3667f1dd817a2c8d",
	},
	"windows/arm64": {
		Name:   "sing-box-1.13.19-windows-arm64.zip",
		SHA256: "dbb6c4803f94a997fcc4a1cce313eff65a901abc197731b55109ea4fbd412c88",
	},
}

type Installer struct {
	HTTPClient      *http.Client
	BaseDir         string
	BundledPath     string
	GOOS            string
	GOARCH          string
	Asset           *releaseAsset
	DownloadURL     func(releaseAsset) string
	DisableOverride bool
	// DisableDownload skips downloading/copying into BaseDir/cores and only
	// returns an already-present bundled or override binary.
	DisableDownload bool
}

func (i *Installer) Ensure(ctx context.Context) (string, error) {
	if !i.DisableOverride {
		environment := loadInstallerEnvironment()
		if environment.SingBoxPath != "" {
			return validateBinary(environment.SingBoxPath)
		}
	}
	var missing []string
	for _, candidate := range i.bundledCandidates() {
		path, err := validateBinary(candidate)
		if err == nil {
			return path, nil
		}
		missing = append(missing, candidate)
	}
	if i.DisableDownload {
		if len(missing) == 0 {
			return "", fmt.Errorf(
				"bundled sing-box %s not found; reinstall the KubeLoop package",
				Version,
			)
		}
		return "", fmt.Errorf(
			"bundled sing-box %s not found (tried %s); reinstall the KubeLoop package",
			Version,
			strings.Join(missing, ", "),
		)
	}
	return i.downloadToCores(ctx)
}
