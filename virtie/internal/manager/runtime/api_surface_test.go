package runtime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRuntimePackageDoesNotExportConstructionAndTeardownInternals(t *testing.T) {
	banned := map[string]struct{}{
		"Balloon":                        {},
		"BalloonQMP":                     {},
		"CleanupStartError":              {},
		"CloseActions":                   {},
		"Closer":                         {},
		"ControlStats":                   {},
		"Disconnecter":                   {},
		"ErrBalloonNotConfigured":        {},
		"ErrForegroundWaitNotConfigured": {},
		"ErrSuspendNotReady":             {},
		"MarkReady":                      {},
		"NewCloser":                      {},
		"NewState":                       {},
		"QueueSuspend":                   {},
		"ShutdownResources":              {},
		"StartTask":                      {},
		"StartedRuntime":                 {},
		"StartupFailureActions":          {},
		"State":                          {},
		"Status":                         {},
		"SuspendRequester":               {},
		"Task":                           {},
		"TaskGroup":                      {},
		"UnsupportedHotplug":             {},
	}

	for name := range exportedDecls(t) {
		if _, ok := banned[name]; ok {
			t.Fatalf("runtime should not export construction/teardown helper %s", name)
		}
	}
}

func exportedDecls(t *testing.T) map[string]struct{} {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	dir := filepath.Dir(file)
	files, err := parser.ParseDir(token.NewFileSet(), dir, func(info os.FileInfo) bool {
		return strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	pkg := files["runtime"]
	if pkg == nil {
		t.Fatal("package runtime not found")
	}
	names := map[string]struct{}{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if decl.Recv == nil && ast.IsExported(decl.Name.Name) {
					names[decl.Name.Name] = struct{}{}
				}
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						if ast.IsExported(spec.Name.Name) {
							names[spec.Name.Name] = struct{}{}
						}
					case *ast.ValueSpec:
						for _, name := range spec.Names {
							if ast.IsExported(name.Name) {
								names[name.Name] = struct{}{}
							}
						}
					}
				}
			}
		}
	}
	return names
}
