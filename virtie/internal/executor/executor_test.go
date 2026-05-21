package executor

import (
	"reflect"
	"strings"
	"testing"
)

func TestRenderArgvAndEnv(t *testing.T) {
	renderer, err := NewWithEnviron(Context{
		"Host": "127.0.0.1",
		"Port": "22",
	}, []string{"USER=template-user"})
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	argv, err := renderer.RenderArgv([]string{
		"socat",
		"-",
		"TCP:{{.Host}}:{{.Port}}",
		"--user={{.Env.USER}}",
	})
	if err != nil {
		t.Fatalf("render argv: %v", err)
	}

	if got, want := argv, []string{"socat", "-", "TCP:127.0.0.1:22", "--user=template-user"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected argv: got %#v want %#v", got, want)
	}
	if got, want := renderer.Env(), []string{"HOST=127.0.0.1", "PORT=22"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected env: got %#v want %#v", got, want)
	}
}

func TestRendererCopiesInputsAndOutputs(t *testing.T) {
	context := Context{"Host": "127.0.0.1"}
	environ := []string{"USER=template-user"}
	renderer, err := NewWithEnviron(context, environ)
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	context["Host"] = "192.0.2.1"
	environ[0] = "USER=changed"

	value, err := renderer.RenderString("{{.Host}} {{.Env.USER}}")
	if err != nil {
		t.Fatalf("render string: %v", err)
	}
	if got, want := value, "127.0.0.1 template-user"; got != want {
		t.Fatalf("unexpected rendered value: got %q want %q", got, want)
	}

	env := renderer.Env()
	env[0] = "HOST=changed"
	if got, want := renderer.Env(), []string{"HOST=127.0.0.1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected env after mutating copy: got %#v want %#v", got, want)
	}
}

func TestRendererEnvLookupUsesLastValue(t *testing.T) {
	renderer, err := NewWithEnviron(nil, []string{"USER=first", "USER=last"})
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	value, err := renderer.RenderString("{{.Env.USER}}")
	if err != nil {
		t.Fatalf("render string: %v", err)
	}
	if got, want := value, "last"; got != want {
		t.Fatalf("unexpected rendered value: got %q want %q", got, want)
	}
}

func TestNewRejectsReservedKeys(t *testing.T) {
	tests := []struct {
		name      string
		context   Context
		wantError string
	}{
		{
			name:      "empty",
			context:   Context{"": "value"},
			wantError: "must not be empty",
		},
		{
			name:      "env",
			context:   Context{"Env": "value"},
			wantError: `key "Env" is reserved`,
		},
		{
			name:      "contains equals",
			context:   Context{"BAD=KEY": "value"},
			wantError: `key "BAD=KEY" must not contain '='`,
		},
		{
			name:      "no env name",
			context:   Context{"---": "value"},
			wantError: `key "---" does not produce an environment name`,
		},
		{
			name:      "collision",
			context:   Context{"vmStatePath": "camel", "vm_state_path": "snake"},
			wantError: `both produce environment name "VM_STATE_PATH"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewWithEnviron(test.context, nil)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("expected error containing %q, got %v", test.wantError, err)
			}
		})
	}
}

func TestRenderRejectsMissingTemplateKey(t *testing.T) {
	renderer, err := New(nil)
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	_, err = renderer.RenderArgv([]string{"echo", "{{.Missing}}"})
	if err == nil ||
		!strings.Contains(err.Error(), `exec[1] "{{.Missing}}"`) ||
		!strings.Contains(err.Error(), `map has no entry for key "Missing"`) {
		t.Fatalf("expected missing key error, got %v", err)
	}
}

func TestRenderRejectsInvalidTemplate(t *testing.T) {
	renderer, err := New(nil)
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	_, err = renderer.RenderArgv([]string{"echo", "{{"})
	if err == nil ||
		!strings.Contains(err.Error(), `exec[1] "{{"`) ||
		!strings.Contains(err.Error(), "unclosed action") {
		t.Fatalf("expected template parse error, got %v", err)
	}
}
