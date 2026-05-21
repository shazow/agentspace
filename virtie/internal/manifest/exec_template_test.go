package manifest

import (
	"reflect"
	"strings"
	"testing"
)

func TestRenderExecTemplatesArgvAndEnv(t *testing.T) {
	t.Setenv("USER", "template-user")

	command, err := RenderExec([]string{
		"socat",
		"-",
		"TCP:{{.Host}}:{{.Port}}",
		"--user={{.Env.USER}}",
	}, ExecTemplateContext{
		"Host": "127.0.0.1",
		"Port": "22",
	})
	if err != nil {
		t.Fatalf("render exec: %v", err)
	}

	if got, want := command.Path, "socat"; got != want {
		t.Fatalf("unexpected path: got %q want %q", got, want)
	}
	if got, want := command.Args, []string{"-", "TCP:127.0.0.1:22", "--user=template-user"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected args: got %#v want %#v", got, want)
	}
	if got, want := command.Env, []string{"HOST=127.0.0.1", "PORT=22"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected env: got %#v want %#v", got, want)
	}
}

func TestRenderCommandTemplatesAndMergesEnv(t *testing.T) {
	command, err := RenderCommand(Command{
		Path: "notify-{{.State}}",
		Args: []string{"{{.Message}}"},
		Env:  []string{"EXISTING=1"},
	}, ExecTemplateContext{
		"State":   "runtime:resume",
		"Message": "Restored",
	})
	if err != nil {
		t.Fatalf("render command: %v", err)
	}

	if got, want := command.Path, "notify-runtime:resume"; got != want {
		t.Fatalf("unexpected path: got %q want %q", got, want)
	}
	if got, want := command.Args, []string{"Restored"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected args: got %#v want %#v", got, want)
	}
	if got, want := command.Env, []string{"EXISTING=1", "MESSAGE=Restored", "STATE=runtime:resume"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected env: got %#v want %#v", got, want)
	}
}

func TestRenderExecRejectsMissingTemplateKey(t *testing.T) {
	_, err := RenderExecArgv([]string{"echo", "{{.Missing}}"}, ExecTemplateContext{})
	if err == nil ||
		!strings.Contains(err.Error(), `exec[1] "{{.Missing}}"`) ||
		!strings.Contains(err.Error(), `map has no entry for key "Missing"`) {
		t.Fatalf("expected missing key error, got %v", err)
	}
}

func TestRenderExecRejectsInvalidTemplate(t *testing.T) {
	_, err := RenderExecArgv([]string{"echo", "{{"}, ExecTemplateContext{})
	if err == nil ||
		!strings.Contains(err.Error(), `exec[1] "{{"`) ||
		!strings.Contains(err.Error(), "unclosed action") {
		t.Fatalf("expected template parse error, got %v", err)
	}
}
