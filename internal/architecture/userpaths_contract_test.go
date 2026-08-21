package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevelopmentHelperScriptsUseIsolatedSecretPath(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	const (
		current = ".kubeloop-dev/secrets/helper.token"
		legacy  = ".kubeloop/helper.token"
	)
	for _, name := range []string{
		filepath.Join(".github", "workflows", "ci.yml"),
		filepath.Join("e2e", "run.sh"),
	} {
		content, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), legacy) {
			t.Fatalf("%s still uses legacy helper token path", name)
		}
		if !strings.Contains(string(content), current) {
			t.Fatalf("%s does not use isolated development helper token path", name)
		}
	}
}
