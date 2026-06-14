package manager

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func (m *manager) confirmDeleteStaleSocket(path string) (bool, error) {
	if output := m.outputWriter(); output != nil {
		fmt.Fprintf(output, "Stale socket detected, delete before launching? [Y|n] %q ", path)
	}
	reader := io.Reader(os.Stdin)
	if m != nil && m.inputReader != nil {
		reader = m.inputReader
	}
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil {
		if !errors.Is(err, io.EOF) || line == "" {
			return false, err
		}
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("expected y or n")
	}
}
