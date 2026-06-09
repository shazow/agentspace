package manager

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/shazow/agentspace/virtie/internal/manager/launch"
)

var suspendStatePath = launch.SuspendStatePath
var vmStatePath = launch.VMStatePath
var launchPIDPath = launch.LaunchPIDPath
var writeSuspendStateData = launch.WriteSuspendStateData
var readSuspendState = launch.ReadSuspendState
var hasSavedSuspendState = launch.HasSavedSuspendState
var removeSuspendState = launch.RemoveSuspendState
var writeLaunchPID = launch.WriteLaunchPID
var readLaunchPID = launch.ReadLaunchPID
var removeLaunchPID = launch.RemoveLaunchPID
var validateLaunchLock = launch.ValidateLaunchLock

type pidSignaler = launch.PIDSignaler

var _ launch.PIDSignaler = syscallPIDSignaler{}

type syscallPIDSignaler struct{}

func (syscallPIDSignaler) Exists(pid int) error {
	err := syscall.Kill(pid, 0)
	if errors.Is(err, syscall.EPERM) {
		return nil
	}
	return err
}

func (syscallPIDSignaler) Signal(pid int, sig os.Signal) error {
	number, ok := sig.(syscall.Signal)
	if !ok {
		return fmt.Errorf("unsupported signal %v", sig)
	}
	return syscall.Kill(pid, number)
}

func errorsIsNoProcess(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}
