package manifest

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/template"
	"unicode"
)

type ExecTemplateContext map[string]string

type RenderedCommand struct {
	Path string
	Args []string
	Env  []string
}

func RenderCommand(command Command, context ExecTemplateContext) (Command, error) {
	renderedArgv, err := RenderExecArgv(CommandArgv(command), context)
	if err != nil {
		return Command{}, err
	}
	rendered := Command{
		Env: append([]string(nil), command.Env...),
	}
	if len(renderedArgv) > 0 {
		rendered.Path = renderedArgv[0]
		rendered.Args = append([]string(nil), renderedArgv[1:]...)
	}
	rendered.Env = append(rendered.Env, ExecContextEnv(context)...)
	return rendered, nil
}

func RenderExec(argv []string, context ExecTemplateContext) (RenderedCommand, error) {
	renderedArgv, err := RenderExecArgv(argv, context)
	if err != nil {
		return RenderedCommand{}, err
	}
	if len(renderedArgv) == 0 {
		return RenderedCommand{}, nil
	}
	return RenderedCommand{
		Path: renderedArgv[0],
		Args: append([]string(nil), renderedArgv[1:]...),
		Env:  ExecContextEnv(context),
	}, nil
}

func RenderExecArgv(argv []string, context ExecTemplateContext) ([]string, error) {
	data := execTemplateData(context)
	rendered := make([]string, 0, len(argv))
	for i, arg := range argv {
		value, err := renderExecTemplateArg(arg, data)
		if err != nil {
			return nil, fmt.Errorf("exec[%d] %q: %w", i, arg, err)
		}
		rendered = append(rendered, value)
	}
	return rendered, nil
}

func ExecContextEnv(context ExecTemplateContext) []string {
	if len(context) == 0 {
		return nil
	}
	keys := make([]string, 0, len(context))
	for key := range context {
		// Env is reserved for host environment lookups in templates and is not
		// injected into child process environments as a context value.
		if key == "" || key == "Env" || strings.ContainsRune(key, '=') {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	values := make(map[string]string, len(keys))
	for _, key := range keys {
		envKey := ExecEnvKey(key)
		if envKey == "" {
			continue
		}
		values[envKey] = context[key]
	}

	keys = keys[:0]
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}

func execTemplateData(context ExecTemplateContext) map[string]any {
	data := make(map[string]any, len(context)+1)
	for key, value := range context {
		data[key] = value
	}
	// Env exposes the host environment to text/template lookups, for example
	// {{.Env.USER}} or {{index .Env "XDG_RUNTIME_DIR"}}.
	data["Env"] = environMap()
	return data
}

func renderExecTemplateArg(arg string, data map[string]any) (string, error) {
	tmpl, err := template.New("exec").Option("missingkey=error").Parse(arg)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}

func environMap() map[string]string {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		env[key] = value
	}
	return env
}

func CommandArgv(command Command) []string {
	if command.Path == "" {
		return append([]string(nil), command.Args...)
	}
	argv := []string{command.Path}
	return append(argv, command.Args...)
}

// ExecEnvKey converts template context keys into stable environment names.
// For example, vmStatePath becomes VM_STATE_PATH.
func ExecEnvKey(key string) string {
	var builder strings.Builder
	var previousUnderscore bool
	var previousLowerOrDigit bool
	for _, r := range key {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if unicode.IsUpper(r) && previousLowerOrDigit && !previousUnderscore {
				builder.WriteByte('_')
			}
			builder.WriteRune(unicode.ToUpper(r))
			previousUnderscore = false
			previousLowerOrDigit = unicode.IsLower(r) || unicode.IsDigit(r)
			continue
		}
		if builder.Len() > 0 && !previousUnderscore {
			builder.WriteByte('_')
		}
		previousUnderscore = true
		previousLowerOrDigit = false
	}
	return strings.Trim(builder.String(), "_")
}
