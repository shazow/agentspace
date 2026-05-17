package manager

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/shazow/agentspace/virtie/internal/manifest"
	"golang.org/x/crypto/ssh"
)

const (
	sshAutoprovisionIdentityName = "id_ed25519"
	guestShellPath               = "/run/current-system/sw/bin/sh"
)

type sshAutoprovisionKey struct {
	IdentityFile  string
	PublicKeyFile string
	AuthorizedKey string
}

func (m *manager) ensureSSHAutoprovisionKey(launchManifest *manifest.Manifest) (sshAutoprovisionKey, error) {
	stateDir := launchManifest.ResolvedPersistenceStateDir()
	if err := ensureDirectories([]string{stateDir}); err != nil {
		return sshAutoprovisionKey{}, err
	}

	identityFile := filepath.Join(stateDir, sshAutoprovisionIdentityName)
	publicKeyFile := identityFile + ".pub"
	if _, err := os.Stat(identityFile); err == nil {
		if chmodErr := os.Chmod(identityFile, 0o600); chmodErr != nil {
			return sshAutoprovisionKey{}, fmt.Errorf("chmod ssh identity %q: %w", identityFile, chmodErr)
		}
		return m.ensurePublicKeyForExistingIdentity(identityFile, publicKeyFile)
	} else if !errors.Is(err, os.ErrNotExist) {
		return sshAutoprovisionKey{}, fmt.Errorf("stat ssh identity %q: %w", identityFile, err)
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return sshAutoprovisionKey{}, fmt.Errorf("generate ssh identity: %w", err)
	}

	block, err := ssh.MarshalPrivateKey(privateKey, "virtie-autoprovision-"+launchManifest.Identity.HostName)
	if err != nil {
		return sshAutoprovisionKey{}, fmt.Errorf("encode ssh identity: %w", err)
	}
	if err := writeNewFile(identityFile, pem.EncodeToMemory(block), 0o600); err != nil {
		return sshAutoprovisionKey{}, err
	}

	return writeAutoprovisionPublicKey(identityFile, publicKeyFile, privateKey.Public())
}

func (m *manager) ensurePublicKeyForExistingIdentity(identityFile string, publicKeyFile string) (sshAutoprovisionKey, error) {
	if data, err := os.ReadFile(publicKeyFile); err == nil {
		if chmodErr := os.Chmod(publicKeyFile, 0o644); chmodErr != nil {
			return sshAutoprovisionKey{}, fmt.Errorf("chmod ssh public key %q: %w", publicKeyFile, chmodErr)
		}
		if authorizedKey := strings.TrimSpace(string(data)); authorizedKey != "" {
			return sshAutoprovisionKey{
				IdentityFile:  identityFile,
				PublicKeyFile: publicKeyFile,
				AuthorizedKey: authorizedKey,
			}, nil
		}
		// Empty public key files are treated as missing so they can be repaired.
		if err := os.Remove(publicKeyFile); err != nil {
			return sshAutoprovisionKey{}, fmt.Errorf("remove empty ssh public key %q: %w", publicKeyFile, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return sshAutoprovisionKey{}, fmt.Errorf("read ssh public key %q: %w", publicKeyFile, err)
	}

	data, err := os.ReadFile(identityFile)
	if err != nil {
		return sshAutoprovisionKey{}, fmt.Errorf("read ssh identity %q: %w", identityFile, err)
	}
	privateKey, err := ssh.ParseRawPrivateKey(data)
	if err != nil {
		return sshAutoprovisionKey{}, fmt.Errorf("parse ssh identity %q: %w", identityFile, err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return sshAutoprovisionKey{}, fmt.Errorf("derive ssh public key from %q: %w", identityFile, err)
	}
	return writeAutoprovisionPublicKey(identityFile, publicKeyFile, signer.PublicKey())
}

func writeAutoprovisionPublicKey(identityFile string, publicKeyFile string, key any) (sshAutoprovisionKey, error) {
	var publicKey ssh.PublicKey
	switch typed := key.(type) {
	case ssh.PublicKey:
		publicKey = typed
	default:
		var err error
		publicKey, err = ssh.NewPublicKey(typed)
		if err != nil {
			return sshAutoprovisionKey{}, fmt.Errorf("encode ssh public key: %w", err)
		}
	}

	publicKeyBytes := ssh.MarshalAuthorizedKey(publicKey)
	if err := os.WriteFile(publicKeyFile, publicKeyBytes, 0o644); err != nil {
		return sshAutoprovisionKey{}, fmt.Errorf("write ssh public key %q: %w", publicKeyFile, err)
	}
	if err := os.Chmod(publicKeyFile, 0o644); err != nil {
		return sshAutoprovisionKey{}, fmt.Errorf("chmod ssh public key %q: %w", publicKeyFile, err)
	}
	return sshAutoprovisionKey{
		IdentityFile:  identityFile,
		PublicKeyFile: publicKeyFile,
		AuthorizedKey: strings.TrimSpace(string(publicKeyBytes)),
	}, nil
}

func writeNewFile(filePath string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create %q: %w", filePath, err)
	}
	writeErr := func() error {
		if _, err := file.Write(data); err != nil {
			return fmt.Errorf("write %q: %w", filePath, err)
		}
		return nil
	}()
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return fmt.Errorf("close %q: %w", filePath, closeErr)
	}
	if err := os.Chmod(filePath, mode); err != nil {
		return fmt.Errorf("chmod %q: %w", filePath, err)
	}
	return nil
}

func (m *manager) installSSHAutoprovisionKey(ctx context.Context, launchManifest *manifest.Manifest, key sshAutoprovisionKey, watchers ...*managedProcess) error {
	socketPath, err := launchManifest.ResolvedGuestAgentSocketPath()
	if err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}
	client, err := m.waitForGuestAgent(ctx, socketPath, watchers...)
	if err != nil {
		return err
	}
	defer client.Disconnect()

	owner := launchManifest.SSH.User + ":users"
	authorizedKeysPath := guestAuthorizedKeysPath(launchManifest.SSH.User)
	sshDir := path.Dir(authorizedKeysPath)
	if err := m.installGuestFileDirectory(ctx, client, authorizedKeysPath, &owner); err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}
	if err := m.chownGuestFile(ctx, client, sshDir, owner); err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}
	if err := m.chmodGuestFile(ctx, client, sshDir, "0700"); err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}

	tempKeyPath := "/run/virtie-autoprovision-authorized-key.pub"
	tempText := key.AuthorizedKey + "\n"
	tempMode := "0600"
	tempFile := manifest.ResolvedWriteFile{
		GuestPath: tempKeyPath,
		Text:      &tempText,
		Mode:      &tempMode,
		Overwrite: true,
	}
	payloadBase64, err := guestFilePayloadBase64(tempFile)
	if err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}
	if err := m.writeGuestFile(client, tempKeyPath, payloadBase64); err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}
	if err := m.chmodGuestFile(ctx, client, tempKeyPath, "0600"); err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}

	script := `set -eu
PATH=/run/current-system/sw/bin:/bin
auth=$1
keyfile=$2
touch "$auth"
if ! grep -qxF -f "$keyfile" "$auth"; then
  cat "$keyfile" >> "$auth"
fi
rm -f "$keyfile"`
	if err := m.runGuestFileCommand(ctx, client, "append authorized_keys", guestShellPath, []string{"-c", script, "virtie-ssh-autoprovision", authorizedKeysPath, tempKeyPath}, authorizedKeysPath); err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}
	if err := m.chownGuestFile(ctx, client, authorizedKeysPath, owner); err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}
	if err := m.chmodGuestFile(ctx, client, authorizedKeysPath, "0600"); err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}
	return nil
}

func guestAuthorizedKeysPath(user string) string {
	if user == "root" {
		return "/root/.ssh/authorized_keys"
	}
	return "/home/" + user + "/.ssh/authorized_keys"
}
