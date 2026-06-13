package runtime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
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

func TestRuntimeDependenciesDoNotNameHotplug(t *testing.T) {
	depsType := reflect.TypeOf(Dependencies{})
	for i := 0; i < depsType.NumField(); i++ {
		field := depsType.Field(i)
		if strings.Contains(strings.ToLower(field.Name), "hotplug") {
			t.Fatalf("runtime dependency %s embeds hotplug feature policy in runtime core", field.Name)
		}
	}
}

func TestRuntimeProductionFilesDoNotImportHotplug(t *testing.T) {
	_, files := runtimeProductionFiles(t)
	for path, file := range files {
		for _, imported := range file.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("unquote import in %s: %v", path, err)
			}
			if strings.Contains(strings.ToLower(importPath), "hotplug") {
				t.Fatalf("runtime production file %s imports hotplug feature package %q", filepath.Base(path), importPath)
			}
		}
	}
}

func TestRuntimeProductionDeclarationsDoNotNameHotplug(t *testing.T) {
	fileset, files := runtimeProductionFiles(t)
	for _, file := range files {
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				failIfHotplugDeclaration(t, fileset, decl.Name)
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						failIfHotplugDeclaration(t, fileset, spec.Name)
					case *ast.ValueSpec:
						for _, name := range spec.Names {
							failIfHotplugDeclaration(t, fileset, name)
						}
					}
				}
			}
		}
	}
}

func failIfHotplugDeclaration(t *testing.T, fileset *token.FileSet, name *ast.Ident) {
	t.Helper()
	if strings.Contains(strings.ToLower(name.Name), "hotplug") {
		t.Fatalf("runtime production declaration %s at %s names hotplug feature policy", name.Name, fileset.Position(name.Pos()))
	}
}

func exportedDecls(t *testing.T) map[string]struct{} {
	t.Helper()
	_, files := runtimeProductionFiles(t)
	names := map[string]struct{}{}
	for _, file := range files {
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

func runtimeProductionFiles(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	dir := filepath.Dir(file)
	fileset := token.NewFileSet()
	files, err := parser.ParseDir(fileset, dir, func(info os.FileInfo) bool {
		return strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	pkg := files["runtime"]
	if pkg == nil {
		t.Fatal("package runtime not found")
	}
	return fileset, pkg.Files
}
