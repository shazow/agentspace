package launch

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	backendfile "github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/ext4"
	"github.com/shazow/agentspace/virtie/internal/manifest"
)

func CreateVolumeImage(volume manifest.Volume) error {
	sizeBytes := volume.Size.Bytes()
	file, err := os.OpenFile(volume.ImagePath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create volume image %q: %w", volume.ImagePath, err)
	}

	created := false
	defer func() {
		if !created {
			_ = os.Remove(volume.ImagePath)
		}
	}()

	if err := file.Close(); err != nil {
		return fmt.Errorf("close volume image %q: %w", volume.ImagePath, err)
	}

	if chattrPath, lookErr := exec.LookPath("chattr"); lookErr == nil {
		cmd := exec.Command(chattrPath, "+C", volume.ImagePath)
		_ = cmd.Run()
	}

	if err := os.Truncate(volume.ImagePath, sizeBytes); err != nil {
		return fmt.Errorf("truncate volume image %q: %w", volume.ImagePath, err)
	}

	image, err := backendfile.OpenFromPath(volume.ImagePath, false)
	if err != nil {
		return fmt.Errorf("open volume image %q: %w", volume.ImagePath, err)
	}
	defer image.Close()

	params := &ext4.Params{}
	if volume.Label != "" {
		params.VolumeName = volume.Label
	}
	params.SectorsPerBlock = 8
	fs, err := ext4.Create(image, sizeBytes, 0, int64(ext4.SectorSize512), params)
	if err != nil {
		return fmt.Errorf("format ext4 volume image %q: %w", volume.ImagePath, err)
	}
	if volume.Label == "" {
		if err := fs.SetLabel(""); err != nil {
			return fmt.Errorf("clear default ext4 volume label for %q: %w", volume.ImagePath, err)
		}
	}

	created = true
	return nil
}

type SocketCleanupPrompt func(path string) (bool, error)

type SocketCleanupOptions struct {
	Paths        []string
	StateDir     string
	AlwaysDelete bool
	Prompt       SocketCleanupPrompt
}

func RemoveStaleSocketPaths(options SocketCleanupOptions) error {
	for _, path := range options.Paths {
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("stat socket %q: %w", path, err)
		}
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("remove socket %q: path is not a socket", path)
		}

		trustedStateSocket := false
		if options.StateDir != "" {
			absPath, pathErr := filepath.Abs(path)
			absStateDir, dirErr := filepath.Abs(options.StateDir)
			if pathErr == nil && dirErr == nil {
				rel, relErr := filepath.Rel(absStateDir, absPath)
				trustedStateSocket = relErr == nil && rel != "." && filepath.IsLocal(rel)
			}
		}

		if options.AlwaysDelete || trustedStateSocket {
			if err := removeExistingSocketPath(path); err != nil {
				return err
			}
			continue
		}
		if options.Prompt == nil {
			return fmt.Errorf("stale socket %q requires confirmation before deletion", path)
		}
		deleteSocket, err := options.Prompt(path)
		if err != nil {
			return fmt.Errorf("confirm stale socket %q deletion: %w", path, err)
		}
		if !deleteSocket {
			return fmt.Errorf("stale socket %q was not deleted", path)
		}
		if err := removeExistingSocketPath(path); err != nil {
			return err
		}
	}
	return nil
}

func RemoveSocketPaths(paths []string) error {
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("stat socket %q: %w", path, err)
		}
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("remove socket %q: path is not a socket", path)
		}
		if err := removeExistingSocketPath(path); err != nil {
			return err
		}
	}
	return nil
}

func removeExistingSocketPath(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove socket %q: %w", path, err)
	}
	return nil
}
