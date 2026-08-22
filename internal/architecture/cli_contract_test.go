package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandPackagesKeepProcessExitInMain(t *testing.T) {
	root := repositoryRoot(t)
	commandRoot := filepath.Join(root, "cmd")
	entries, err := os.ReadDir(commandRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "kubeloop-") {
			continue
		}
		files, err := filepath.Glob(filepath.Join(commandRoot, entry.Name(), "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			assertCommandFileExitContract(t, path)
		}
	}
}

func assertCommandFileExitContract(t *testing.T, path string) {
	t.Helper()
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			packageName, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			position := set.Position(call.Pos())
			if packageName.Name == "os" && selector.Sel.Name == "Exit" && function.Name.Name != "main" {
				t.Errorf("%s:%d calls os.Exit outside main", path, position.Line)
			}
			if strings.HasPrefix(selector.Sel.Name, "Fatal") {
				t.Errorf(
					"%s:%d calls %s.%s instead of returning an error",
					path, position.Line, packageName.Name, selector.Sel.Name,
				)
			}
			return true
		})
	}
}
