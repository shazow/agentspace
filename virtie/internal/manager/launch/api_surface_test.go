package launch

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

func TestLaunchPackageDoesNotExportPackagePrivateHelpers(t *testing.T) {
	banned := map[string]struct{}{
		"BuildSSHCommand":           {},
		"BuildSSHCommandHint":       {},
		"BuildSSHCommandWithArgv":   {},
		"EnsureDirectories":         {},
		"EnsureExistingSocketPaths": {},
		"EnsureParentDirectories":   {},
		"EnsureVolumeImages":        {},
		"FinalizeRuntimeStartup":    {},
		"FirstUnexpectedExit":       {},
		"ForegroundWait":            {},
		"GuestDirectoryMode":        {},
		"GuestFilePayloadBase64":    {},
		"GuestInstallDirectoryArgs": {},
		"LockedPlanSetup":           {},
		"NotifyRuntimeResume":       {},
		"NotifyRuntimeSuspend":      {},
		"PrepareFilesystem":         {},
		"QEMUCommandBuilder":        {},
		"ReadHostFileForGuest":      {},
		"RestoreRuntime":            {},
		"RuntimeRestore":            {},
		"RuntimeStartupFinalize":    {},
		"RuntimeSuspendSave":        {},
		"SaveRuntimeSuspend":        {},
		"SetupLockedPlan":           {},
		"StartQEMU":                 {},
		"StartRuns":                 {},
		"ValidateLaunchLock":        {},
		"WaitForeground":            {},
		"WaitProcess":               {},
		"WorkspaceCWDSource":        {},
		"WrapCommandError":          {},
		"WrapStage":                 {},
		"WriteBackHostPath":         {},
	}

	for name := range exportedDecls(t) {
		if _, ok := banned[name]; ok {
			t.Fatalf("launch should not export package-private helper %s", name)
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
	pkg := files["launch"]
	if pkg == nil {
		t.Fatal("package launch not found")
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
