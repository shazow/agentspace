package manager

import (
	"context"

	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/sshtools"
)

const guestShellPath = "/run/current-system/sw/bin/sh"

type sshAutoprovisionKey struct {
	IdentityFile  string
	PublicKeyFile string
	AuthorizedKey string
}

func (m *manager) ensureSSHAutoprovisionKey(launchManifest *manifest.Manifest) (sshAutoprovisionKey, error) {
	key, err := (sshtools.KeyStore{
		Dir:     launchManifest.ResolvedPersistenceStateDir(),
		Comment: "virtie-autoprovision-" + launchManifest.Identity.HostName,
	}).Ensure()
	if err != nil {
		return sshAutoprovisionKey{}, err
	}
	return sshAutoprovisionKey{
		IdentityFile:  key.IdentityFile,
		PublicKeyFile: key.PublicKeyFile,
		AuthorizedKey: key.AuthorizedKey,
	}, nil
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

	plan := sshtools.NewAuthorizedKeysInstallPlan(launchManifest.SSH.User, key.AuthorizedKey)
	if err := m.installGuestFileDirectory(ctx, client, plan.AuthorizedKeysPath, plan.Owner, "0700"); err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}
	if err := m.chownGuestFile(ctx, client, plan.SSHDir, plan.Owner); err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}
	if err := m.chmodGuestFile(ctx, client, plan.SSHDir, "0700"); err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}

	tempFile := manifest.ResolvedWriteFile{
		GuestPath: plan.TempKeyPath,
		Mode:      plan.TempKeyMode,
		Overwrite: true,
		Content: manifest.WriteFileContent{
			Kind: manifest.WriteFileContentText,
			Text: plan.TempKeyText,
		},
	}
	payloadBase64, err := guestFilePayloadBase64(tempFile)
	if err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}
	if err := m.writeGuestFile(client, plan.TempKeyPath, payloadBase64); err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}
	if err := m.chmodGuestFile(ctx, client, plan.TempKeyPath, plan.TempKeyMode); err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}

	command := plan.AppendCommand(guestShellPath)
	if err := m.runGuestFileCommand(ctx, client, command.Name, command.Path, command.Args, command.InputPath); err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}
	if err := m.chownGuestFile(ctx, client, plan.AuthorizedKeysPath, plan.Owner); err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}
	if err := m.chmodGuestFile(ctx, client, plan.AuthorizedKeysPath, "0600"); err != nil {
		return &stageError{Stage: "ssh autoprovision", Err: err}
	}
	return nil
}
