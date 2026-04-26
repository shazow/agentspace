package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

func (m *manager) resumeSaved(ctx context.Context, manifest *manifest.Manifest, state suspendState) (err error) {
	if err := manifest.Validate(); err != nil {
		return err
	}
	if state.CID <= 0 {
		return &stageError{Stage: "restore", Err: fmt.Errorf("saved suspend state %q does not include a valid vsock CID", suspendStatePath(manifest))}
	}
	if state.VMStatePath == "" {
		state.VMStatePath = vmStatePath(manifest)
	}
	if _, err := os.Stat(state.VMStatePath); err != nil {
		return &stageError{Stage: "restore", Err: fmt.Errorf("saved vm state %q is not available: %w", state.VMStatePath, err)}
	}

	launchCtx, cancelLaunch := context.WithCancel(ctx)
	defer cancelLaunch()

	signalCh, stopSignals := m.launchSignalChannel()
	signalDone := make(chan struct{})
	sessionSignals := make(chan os.Signal, 8)
	go func() {
		for {
			select {
			case <-signalDone:
				return
			case sig, ok := <-signalCh:
				if !ok {
					return
				}
				switch sig {
				case os.Interrupt, syscall.SIGTERM:
					cancelLaunch()
				case syscall.SIGTSTP, syscall.SIGCONT:
					select {
					case sessionSignals <- sig:
					default:
					}
				}
			}
		}
	}()
	defer close(signalDone)
	defer stopSignals()

	managedSocketPaths, err := manifest.ResolvedSocketPaths()
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	virtioFSSocketPaths, err := manifest.ResolvedVirtioFSSocketPaths()
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	qmpSocketPath, err := manifest.ResolvedQMPSocketPath()
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	volumes := manifest.ResolvedVolumes()

	lock, err := m.locker.Acquire(manifest.ResolvedLockPath())
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	defer joinDeferredError(&err, lock.Release)

	if err := removeSuspendRequest(manifest); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := writeLaunchPID(manifest, os.Getpid()); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	defer joinDeferredError(&err, func() error {
		return removeLaunchPID(manifest, os.Getpid())
	})

	cidLock, err := m.acquireCID(manifest, state.CID)
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	defer joinDeferredError(&err, cidLock.Release)
	m.logger.Printf("restoring saved vsock cid %d", state.CID)

	if err := ensureDirectories(manifest.ResolvedPersistenceDirectories()); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := ensureParentDirectories(managedSocketPaths); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := ensureParentDirectories([]string{qmpSocketPath}); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := ensureParentDirectories(volumeImagePaths(volumes)); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := removeSocketPaths(managedSocketPaths); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := removeSocketPaths([]string{qmpSocketPath}); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := ensureVolumeImages(volumes); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}

	var started []*managedProcess
	var qmpClient qmpClient
	var featureTasks managedTaskGroup
	defer func() {
		featureErr := featureTasks.Stop()
		stopErr := m.stopAll(started)
		var disconnectErr error
		if qmpClient != nil {
			disconnectErr = qmpClient.Disconnect()
		}
		cleanupErr := removeSocketPaths(append([]string{qmpSocketPath}, managedSocketPaths...))
		if err == nil {
			err = errors.Join(featureErr, stopErr, disconnectErr, cleanupErr)
		} else if featureErr != nil || stopErr != nil || disconnectErr != nil || cleanupErr != nil {
			err = errors.Join(err, featureErr, stopErr, disconnectErr, cleanupErr)
		}
	}()

	virtiofsd, err := m.startVirtioFSDaemons(manifest)
	if err != nil {
		return &stageError{Stage: "virtiofs startup", Err: err}
	}
	started = append(started, virtiofsd...)

	m.logger.Printf("waiting for virtiofs sockets")
	if err := m.waitForSockets(launchCtx, virtioFSSocketPaths, started...); err != nil {
		return err
	}

	m.logger.Printf("starting qemu for restore")
	qemuSpec, err := buildIncomingQEMUSpec(manifest, state.CID)
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	qemu, err := m.startManagedProcess(qemuSpec)
	if err != nil {
		return &stageError{Stage: "vm startup", Err: err}
	}
	started = append(started, qemu)

	m.logger.Printf("waiting for qmp readiness")
	qmpClient, err = m.waitForQMP(launchCtx, qmpSocketPath, qemu)
	if err != nil {
		return err
	}
	qemu.shutdown = func() error {
		return qmpClient.Quit(m.effectiveQMPQuitTimeout())
	}

	m.logger.Printf("restoring vm state")
	if err := qmpClient.MigrateIncoming(m.effectiveQMPMigrationTimeout(), state.VMStatePath); err != nil {
		return &stageError{Stage: "restore", Err: err}
	}
	if err := m.waitForMigration(launchCtx, qmpClient); err != nil {
		return &stageError{Stage: "restore", Err: err}
	}
	if err := qmpClient.Cont(m.effectiveQMPCommandTimeout()); err != nil {
		return &stageError{Stage: "restore", Err: err}
	}

	m.logger.Printf("waiting for ssh readiness")
	if err := m.waitForSSH(launchCtx, manifest, state.CID, started...); err != nil {
		return err
	}

	featureTasks = startOptionalFeatureTasks(launchCtx, optionalFeatureRuntime{
		logger:     m.logger,
		qmpTimeout: m.effectiveQMPCommandTimeout(),
	}, manifest, qmpClient)

	m.logger.Printf("starting ssh session")
	session, err := m.startManagedProcess(buildSSHSpec(manifest, state.CID, nil, true))
	if err != nil {
		return &stageError{Stage: "active session", Err: err}
	}
	started = append(started, session)

	if err := os.Remove(state.VMStatePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return &stageError{Stage: "restore", Err: fmt.Errorf("remove saved vm state %q: %w", state.VMStatePath, err)}
	}
	if err := removeSuspendState(manifest); err != nil {
		return &stageError{Stage: "restore", Err: err}
	}

	return m.waitForSession(launchCtx, session, manifest, qmpSocketPath, qmpClient, state.CID, sessionSignals, started[:len(started)-1]...)
}
