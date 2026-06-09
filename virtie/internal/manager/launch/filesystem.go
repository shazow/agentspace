package launch

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	backendfile "github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/ext4"
	"github.com/shazow/agentspace/virtie/internal/manifest"
)

func PrepareFilesystem(plan *Plan, logger *slog.Logger) error {
	if err := EnsureDirectories(plan.Manifest.ResolvedPersistenceDirectories()); err != nil {
		return err
	}
	if err := EnsureParentDirectories(plan.RuntimeSocketCleanupFiles()); err != nil {
		return err
	}
	if err := EnsureExistingSocketPaths(plan.ExternalVirtioFSSocketPaths); err != nil {
		return err
	}
	if err := EnsureParentDirectories(plan.VolumeImagePaths); err != nil {
		return err
	}
	if err := RemoveSocketPaths(plan.RuntimeSocketCleanupFiles()); err != nil {
		return err
	}
	if err := EnsureVolumeImages(plan.Volumes, logger); err != nil {
		return err
	}
	return nil
}

func EnsureDirectories(directories []string) error {
	for _, dir := range directories {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	return nil
}

func EnsureVolumeImages(volumes []manifest.Volume, logger *slog.Logger) error {
	for _, volume := range volumes {
		if !volume.AutoCreate {
			continue
		}

		info, err := os.Stat(volume.ImagePath)
		if err == nil {
			if info.IsDir() {
				return fmt.Errorf("volume image %q is a directory", volume.ImagePath)
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat volume image %q: %w", volume.ImagePath, err)
		}

		if logger != nil {
			logger.Info("creating volume image", "path", volume.ImagePath, "size_mib", volume.Size, "fs_type", volume.FSType)
		}
		if err := CreateVolumeImage(volume); err != nil {
			return err
		}
	}

	return nil
}

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

func EnsureParentDirectories(paths []string) error {
	for _, path := range paths {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	return nil
}

func RemoveSocketPaths(paths []string) error {
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove socket %q: %w", path, err)
		}
	}
	return nil
}

func EnsureExistingSocketPaths(paths []string) error {
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("external virtiofs socket %q does not exist", path)
			}
			return fmt.Errorf("stat external virtiofs socket %q: %w", path, err)
		}
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("external virtiofs socket %q is not a socket", path)
		}
	}
	return nil
}
