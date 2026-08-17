package helper

import (
	"strings"
	"testing"
)

func TestInstallRootFromWindowsExe(t *testing.T) {
	t.Parallel()
	cases := []struct {
		exe  string
		want string
	}{
		{`D:\Apps\KubeLoop\KubeLoop.exe`, `D:\Apps\KubeLoop`},
		{`D:\Apps\KubeLoop\resources\kubeloop-helper.exe`, `D:\Apps\KubeLoop`},
		{`C:\Program Files\KubeLoop\KubeLoop.exe`, `C:\Program Files\KubeLoop`},
		{`C:\Program Files\KubeLoop\resources\kubeloop-helper.exe`, `C:\Program Files\KubeLoop`},
		{`D:/Apps/KubeLoop/resources/kubeloop-helper.exe`, `D:\Apps\KubeLoop`},
	}
	for _, tc := range cases {
		got := installRootFromWindowsExe(tc.exe)
		if !strings.EqualFold(got, tc.want) {
			t.Fatalf("exe=%q got %q want %q", tc.exe, got, tc.want)
		}
	}
}
