package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

type suspendState struct {
	HostName      string    `json:"hostName"`
	QMPSocketPath string    `json:"qmpSocketPath"`
	Timestamp     time.Time `json:"timestamp"`
	Status        string    `json:"status"`
}

func suspendStatePath(manifest *manifest.Manifest) string {
	return filepath.Join(manifest.Paths.WorkingDir, ".virtie", manifest.Identity.HostName+".suspend.json")
}

func writeSuspendState(manifest *manifest.Manifest, qmpSocketPath string, status string) error {
	state := suspendState{
		HostName:      manifest.Identity.HostName,
		QMPSocketPath: qmpSocketPath,
		Timestamp:     time.Now().UTC(),
		Status:        status,
	}

	path := suspendStatePath(manifest)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create suspend state directory: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode suspend state: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write suspend state %q: %w", path, err)
	}
	return nil
}

func removeSuspendState(manifest *manifest.Manifest) error {
	path := suspendStatePath(manifest)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove suspend state %q: %w", path, err)
	}
	return nil
}
