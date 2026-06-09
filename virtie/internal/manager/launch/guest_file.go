package launch

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

func GuestFilePayloadBase64(file manifest.ResolvedWriteFile) (string, error) {
	if file.Content.Kind == manifest.WriteFileContentText {
		return base64.StdEncoding.EncodeToString([]byte(file.Content.Text)), nil
	}
	if file.Content.Kind != manifest.WriteFileContentPath {
		return "", fmt.Errorf("guest file %q has no text or host path", file.GuestPath)
	}

	data, err := ReadHostFileForGuest(file)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func ReadHostFileForGuest(file manifest.ResolvedWriteFile) ([]byte, error) {
	if file.Content.Kind != manifest.WriteFileContentPath {
		return nil, fmt.Errorf("guest file %q has no host path", file.GuestPath)
	}
	if !file.FollowLinks {
		info, err := os.Lstat(file.Content.Path)
		if err != nil {
			return nil, fmt.Errorf("stat host file %q for guest path %q: %w", file.Content.Path, file.GuestPath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("host file %q for guest path %q is a symlink and followLinks is false", file.Content.Path, file.GuestPath)
		}
	}
	data, err := os.ReadFile(file.Content.Path)
	if err != nil {
		return nil, fmt.Errorf("read host file %q for guest path %q: %w", file.Content.Path, file.GuestPath, err)
	}
	return data, nil
}

func WriteBackHostPath(file manifest.ResolvedWriteFile) (string, error) {
	if file.Content.Kind != manifest.WriteFileContentPath {
		return "", fmt.Errorf("guest file %q has no host path", file.GuestPath)
	}
	info, err := os.Lstat(file.Content.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return file.Content.Path, nil
		}
		return "", fmt.Errorf("stat host file %q for guest path %q: %w", file.Content.Path, file.GuestPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return file.Content.Path, nil
	}
	if !file.FollowLinks {
		return "", fmt.Errorf("host file %q for guest path %q is a symlink and followLinks is false", file.Content.Path, file.GuestPath)
	}
	resolvedPath, err := filepath.EvalSymlinks(file.Content.Path)
	if err != nil {
		return "", fmt.Errorf("resolve host symlink %q for guest path %q: %w", file.Content.Path, file.GuestPath, err)
	}
	return resolvedPath, nil
}

func WriteHostFileAtomic(hostPath string, data []byte) error {
	dir := filepath.Dir(hostPath)
	mode := os.FileMode(0o644)
	if info, err := os.Stat(hostPath); err == nil {
		mode = info.Mode().Perm()
	}
	temp, err := os.CreateTemp(dir, ".virtie-writeback-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, hostPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func GuestInstallDirectoryArgs(guestDir string, owner string, mode string) []string {
	args := []string{"-d"}
	if owner != "" {
		user, group, _ := strings.Cut(owner, ":")
		if user != "" {
			args = append(args, "-o", user)
		}
		if group != "" {
			args = append(args, "-g", group)
		}
	}
	if mode != "" {
		args = append(args, "-m", GuestDirectoryMode(mode))
	}
	return append(args, guestDir)
}

func GuestDirectoryMode(mode string) string {
	prefix := ""
	digits := mode
	if strings.HasPrefix(mode, "0") {
		prefix = "0"
		digits = mode[1:]
	}
	if len(digits) != 3 {
		return mode
	}

	out := make([]byte, 3)
	for i := 0; i < 3; i++ {
		d := digits[i] - '0'
		if d&0b100 != 0 {
			d |= 0b001
		}
		out[i] = '0' + d
	}
	return prefix + string(out)
}
