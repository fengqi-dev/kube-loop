//go:build windows

package helper

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBinaryInstallPathForExecutable(t *testing.T) {
	t.Parallel()
	name := HelperBinaryBaseName() + ".exe"
	cases := []struct {
		executable string
		want       string
	}{
		{
			executable: `D:\a\kube-loop\build\embedded\kubeloop-helper.exe`,
			want:       filepath.Join(`D:\a\kube-loop\build\embedded`, "resources", name),
		},
		{
			executable: `D:\Apps\KubeLoop\resources\kubeloop-helper.exe`,
			want:       filepath.Join(`D:\Apps\KubeLoop`, "resources", name),
		},
	}
	for _, tc := range cases {
		got := BinaryInstallPathForExecutable(tc.executable)
		if !strings.EqualFold(got, tc.want) {
			t.Errorf("executable=%q got %q want %q", tc.executable, got, tc.want)
		}
	}
}
