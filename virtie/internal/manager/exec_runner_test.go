package manager

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shazow/agentspace/virtie/internal/loghandler"
)

func TestExecRunnerDebugLogsBufferedStreams(t *testing.T) {
	var logs bytes.Buffer
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	proc := startExecRunnerOutputHelper(t, processSpec{
		Name:        "helper",
		DebugOutput: true,
		Logger:      loghandler.NewLogger(&logs, &loghandler.Options{Level: slog.LevelDebug, NoColor: true}),
		Stdout:      &stdout,
		Stderr:      &stderr,
	})
	if err := proc.Wait(); err != nil {
		t.Fatalf("wait helper: %v", err)
	}

	if got, want := stdout.String(), "first stdout\npartial stdout"; got != want {
		t.Fatalf("unexpected stdout: got %q want %q", got, want)
	}
	if got, want := stderr.String(), "first stderr\npartial stderr"; got != want {
		t.Fatalf("unexpected stderr: got %q want %q", got, want)
	}

	output := logs.String()
	for _, want := range []string{
		`DBG first stdout exec=helper stream=stdout`,
		`DBG partial stdout exec=helper stream=stdout`,
		`INF first stderr exec=helper stream=stderr`,
		`INF partial stderr exec=helper stream=stderr`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("debug log missing %q:\n%s", want, output)
		}
	}
}

func TestExecRunnerPreservesFileBackedStreams(t *testing.T) {
	var logs bytes.Buffer
	stdoutFile, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatalf("create stdout file: %v", err)
	}
	defer stdoutFile.Close()
	stderrFile, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatalf("create stderr file: %v", err)
	}
	defer stderrFile.Close()

	proc := startExecRunnerOutputHelper(t, processSpec{
		Name:        "helper",
		DebugOutput: true,
		Logger:      loghandler.NewLogger(&logs, &loghandler.Options{Level: slog.LevelDebug, NoColor: true}),
		Stdout:      stdoutFile,
		Stderr:      stderrFile,
	})
	if err := proc.Wait(); err != nil {
		t.Fatalf("wait helper: %v", err)
	}

	if err := stdoutFile.Close(); err != nil {
		t.Fatalf("close stdout file: %v", err)
	}
	if err := stderrFile.Close(); err != nil {
		t.Fatalf("close stderr file: %v", err)
	}
	stdoutBytes, err := os.ReadFile(stdoutFile.Name())
	if err != nil {
		t.Fatalf("read stdout file: %v", err)
	}
	stderrBytes, err := os.ReadFile(stderrFile.Name())
	if err != nil {
		t.Fatalf("read stderr file: %v", err)
	}
	if got, want := string(stdoutBytes), "first stdout\npartial stdout"; got != want {
		t.Fatalf("unexpected stdout file: got %q want %q", got, want)
	}
	if got, want := string(stderrBytes), "first stderr\npartial stderr"; got != want {
		t.Fatalf("unexpected stderr file: got %q want %q", got, want)
	}
	output := logs.String()
	if strings.Contains(output, "stream=stdout") || strings.Contains(output, "stream=stderr") {
		t.Fatalf("unexpected stream capture for file-backed output:\n%s", output)
	}
}

func TestExecRunnerDebugLogsOptInFileBackedStreams(t *testing.T) {
	var logs bytes.Buffer
	stdoutFile, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatalf("create stdout file: %v", err)
	}
	defer stdoutFile.Close()
	stderrFile, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatalf("create stderr file: %v", err)
	}
	defer stderrFile.Close()

	proc := startExecRunnerOutputHelper(t, processSpec{
		Name:              "helper",
		DebugOutput:       true,
		CaptureFileOutput: true,
		Logger:            loghandler.NewLogger(&logs, &loghandler.Options{Level: slog.LevelDebug, NoColor: true}),
		Stdout:            stdoutFile,
		Stderr:            stderrFile,
	})
	if err := proc.Wait(); err != nil {
		t.Fatalf("wait helper: %v", err)
	}

	if err := stdoutFile.Close(); err != nil {
		t.Fatalf("close stdout file: %v", err)
	}
	if err := stderrFile.Close(); err != nil {
		t.Fatalf("close stderr file: %v", err)
	}
	stdoutBytes, err := os.ReadFile(stdoutFile.Name())
	if err != nil {
		t.Fatalf("read stdout file: %v", err)
	}
	stderrBytes, err := os.ReadFile(stderrFile.Name())
	if err != nil {
		t.Fatalf("read stderr file: %v", err)
	}
	if got, want := string(stdoutBytes), "first stdout\npartial stdout"; got != want {
		t.Fatalf("unexpected stdout file: got %q want %q", got, want)
	}
	if got, want := string(stderrBytes), "first stderr\npartial stderr"; got != want {
		t.Fatalf("unexpected stderr file: got %q want %q", got, want)
	}

	output := logs.String()
	for _, want := range []string{
		`DBG first stdout exec=helper stream=stdout`,
		`DBG partial stdout exec=helper stream=stdout`,
		`INF first stderr exec=helper stream=stderr`,
		`INF partial stderr exec=helper stream=stderr`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("debug log missing %q:\n%s", want, output)
		}
	}
}

func TestExecRunnerLogsStderrWithInfoLogger(t *testing.T) {
	var logs bytes.Buffer
	stdoutFile, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatalf("create stdout file: %v", err)
	}
	defer stdoutFile.Close()
	stderrFile, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatalf("create stderr file: %v", err)
	}
	defer stderrFile.Close()

	proc := startExecRunnerOutputHelper(t, processSpec{
		Name:              "helper",
		DebugOutput:       true,
		CaptureFileOutput: true,
		Logger:            loghandler.NewLogger(&logs, &loghandler.Options{Level: slog.LevelInfo, NoColor: true}),
		Stdout:            stdoutFile,
		Stderr:            stderrFile,
	})
	if err := proc.Wait(); err != nil {
		t.Fatalf("wait helper: %v", err)
	}

	if err := stdoutFile.Close(); err != nil {
		t.Fatalf("close stdout file: %v", err)
	}
	if err := stderrFile.Close(); err != nil {
		t.Fatalf("close stderr file: %v", err)
	}
	stdoutBytes, err := os.ReadFile(stdoutFile.Name())
	if err != nil {
		t.Fatalf("read stdout file: %v", err)
	}
	stderrBytes, err := os.ReadFile(stderrFile.Name())
	if err != nil {
		t.Fatalf("read stderr file: %v", err)
	}
	if got, want := string(stdoutBytes), "first stdout\npartial stdout"; got != want {
		t.Fatalf("unexpected stdout file: got %q want %q", got, want)
	}
	if got, want := string(stderrBytes), "first stderr\npartial stderr"; got != want {
		t.Fatalf("unexpected stderr file: got %q want %q", got, want)
	}

	output := logs.String()
	for _, want := range []string{
		`INF first stderr exec=helper stream=stderr`,
		`INF partial stderr exec=helper stream=stderr`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("info log missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "stream=stdout") {
		t.Fatalf("unexpected stdout debug log with info logger:\n%s", output)
	}
}

func TestExecRunnerStandardStreamsLogEnabledOutputWithoutRawDuplicate(t *testing.T) {
	var logs bytes.Buffer
	stdoutVisible, stderrVisible := replaceStandardStreams(t)

	proc := startExecRunnerOutputHelper(t, processSpec{
		Name:              "helper",
		DebugOutput:       true,
		CaptureFileOutput: true,
		Logger:            loghandler.NewLogger(&logs, &loghandler.Options{Level: slog.LevelInfo, NoColor: true}),
		Stdout:            os.Stdout,
		Stderr:            os.Stderr,
	})
	if err := proc.Wait(); err != nil {
		t.Fatalf("wait helper: %v", err)
	}

	if got, want := readFileString(t, stdoutVisible), "first stdout\npartial stdout"; got != want {
		t.Fatalf("unexpected visible stdout: got %q want %q", got, want)
	}
	if got := readFileString(t, stderrVisible); got != "" {
		t.Fatalf("unexpected visible stderr duplicate: got %q", got)
	}

	output := logs.String()
	for _, want := range []string{
		`INF first stderr exec=helper stream=stderr`,
		`INF partial stderr exec=helper stream=stderr`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("info log missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "stream=stdout") {
		t.Fatalf("unexpected stdout debug log with info logger:\n%s", output)
	}
}

func TestExecRunnerStandardStreamsDebugLogsWithoutRawDuplicate(t *testing.T) {
	var logs bytes.Buffer
	stdoutVisible, stderrVisible := replaceStandardStreams(t)

	proc := startExecRunnerOutputHelper(t, processSpec{
		Name:              "helper",
		DebugOutput:       true,
		CaptureFileOutput: true,
		Logger:            loghandler.NewLogger(&logs, &loghandler.Options{Level: slog.LevelDebug, NoColor: true}),
		Stdout:            os.Stdout,
		Stderr:            os.Stderr,
	})
	if err := proc.Wait(); err != nil {
		t.Fatalf("wait helper: %v", err)
	}

	if got := readFileString(t, stdoutVisible); got != "" {
		t.Fatalf("unexpected visible stdout duplicate: got %q", got)
	}
	if got := readFileString(t, stderrVisible); got != "" {
		t.Fatalf("unexpected visible stderr duplicate: got %q", got)
	}

	output := logs.String()
	for _, want := range []string{
		`DBG first stdout exec=helper stream=stdout`,
		`DBG partial stdout exec=helper stream=stdout`,
		`INF first stderr exec=helper stream=stderr`,
		`INF partial stderr exec=helper stream=stderr`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("debug log missing %q:\n%s", want, output)
		}
	}
}

func TestExecRunnerWaitDoesNotBlockOnDescendantInheritedOutput(t *testing.T) {
	var logs bytes.Buffer
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	pidFile := t.TempDir() + "/descendant.pid"
	defer killExecRunnerDescendant(pidFile)

	proc := startExecRunnerOutputHelper(t, processSpec{
		Name:              "helper",
		DebugOutput:       true,
		CaptureFileOutput: true,
		Logger:            loghandler.NewLogger(&logs, &loghandler.Options{Level: slog.LevelDebug, NoColor: true}),
		Stdout:            &stdout,
		Stderr:            &stderr,
		Env: []string{
			"GO_WANT_EXEC_RUNNER_BACKGROUND_HELPER=1",
			"GO_EXEC_RUNNER_BACKGROUND_PID_FILE=" + pidFile,
		},
	})

	done := make(chan error, 1)
	go func() {
		done <- proc.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait helper: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wait helper timed out while descendant held stdout/stderr open")
	}

	if got, want := stdout.String(), "first stdout\npartial stdout"; got != want {
		t.Fatalf("unexpected stdout: got %q want %q", got, want)
	}
	if got, want := stderr.String(), "first stderr\npartial stderr"; got != want {
		t.Fatalf("unexpected stderr: got %q want %q", got, want)
	}

	output := logs.String()
	for _, want := range []string{
		`DBG first stdout exec=helper stream=stdout`,
		`DBG partial stdout exec=helper stream=stdout`,
		`INF first stderr exec=helper stream=stderr`,
		`INF partial stderr exec=helper stream=stderr`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("debug log missing %q:\n%s", want, output)
		}
	}
}

func TestExecRunnerDebugLogsNilStreams(t *testing.T) {
	var logs bytes.Buffer

	proc := startExecRunnerOutputHelper(t, processSpec{
		Name:        "helper",
		DebugOutput: true,
		Logger:      loghandler.NewLogger(&logs, &loghandler.Options{Level: slog.LevelDebug, NoColor: true}),
	})
	if err := proc.Wait(); err != nil {
		t.Fatalf("wait helper: %v", err)
	}

	output := logs.String()
	for _, want := range []string{
		`DBG first stdout exec=helper stream=stdout`,
		`DBG partial stdout exec=helper stream=stdout`,
		`INF first stderr exec=helper stream=stderr`,
		`INF partial stderr exec=helper stream=stderr`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("debug log missing %q:\n%s", want, output)
		}
	}
}

func startExecRunnerOutputHelper(t *testing.T, spec processSpec) process {
	t.Helper()

	spec.Path = os.Args[0]
	spec.Args = []string{"-test.run=TestExecRunnerOutputHelper", "--"}
	spec.Env = append(spec.Env, "GO_WANT_EXEC_RUNNER_OUTPUT_HELPER=1")

	proc, err := (&execRunner{}).Start(spec)
	if err != nil {
		t.Fatalf("start helper: %v", err)
	}
	return proc
}

func replaceStandardStreams(t *testing.T) (*os.File, *os.File) {
	t.Helper()

	originalStdout := os.Stdout
	originalStderr := os.Stderr

	stdoutFile, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatalf("create stdout file: %v", err)
	}
	stderrFile, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatalf("create stderr file: %v", err)
	}

	os.Stdout = stdoutFile
	os.Stderr = stderrFile
	t.Cleanup(func() {
		os.Stdout = originalStdout
		os.Stderr = originalStderr
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
	})

	return stdoutFile, stderrFile
}

func readFileString(t *testing.T, file *os.File) string {
	t.Helper()

	if err := file.Sync(); err != nil {
		t.Fatalf("sync %s: %v", file.Name(), err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatalf("read %s: %v", file.Name(), err)
	}
	return string(data)
}

func TestExecRunnerOutputHelper(t *testing.T) {
	if os.Getenv("GO_WANT_EXEC_RUNNER_OUTPUT_HELPER") != "1" {
		return
	}
	if os.Getenv("GO_WANT_EXEC_RUNNER_HOLD_FDS") == "1" {
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	if os.Getenv("GO_WANT_EXEC_RUNNER_BACKGROUND_HELPER") == "1" {
		cmd := exec.Command(os.Args[0], "-test.run=TestExecRunnerOutputHelper", "--")
		cmd.Env = append(os.Environ(), "GO_WANT_EXEC_RUNNER_HOLD_FDS=1")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "start descendant: %v\n", err)
			os.Exit(2)
		}
		if pidFile := os.Getenv("GO_EXEC_RUNNER_BACKGROUND_PID_FILE"); pidFile != "" {
			_ = os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
		}
	}
	_, _ = fmt.Fprint(os.Stdout, "first stdout\npartial stdout")
	_, _ = fmt.Fprint(os.Stderr, "first stderr\npartial stderr")
	os.Exit(0)
}

func killExecRunnerDescendant(pidFile string) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = process.Kill()
}
