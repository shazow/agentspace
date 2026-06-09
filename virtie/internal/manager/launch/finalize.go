package launch

import (
	"log/slog"
	"os/exec"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

type QEMUCommandBuilder func(manifest *manifest.Manifest, cid int, incoming bool) (*exec.Cmd, error)

func FinalizeLockedPlan(plan *Plan, checker VSockCIDChecker, buildQEMU QEMUCommandBuilder) error {
	cid, err := AcquireCID(plan.Manifest, plan.ResumeState, checker)
	if err != nil {
		return err
	}
	qemuCmd, err := buildQEMU(plan.Manifest, cid, plan.ResumeState != nil)
	if err != nil {
		return err
	}
	plan.CID = cid
	plan.QEMUCommand = qemuCmd
	return nil
}

type LockedPlanSetup struct {
	Plan      *Plan
	Checker   VSockCIDChecker
	BuildQEMU QEMUCommandBuilder
	Logger    *slog.Logger
	Cleanup   func() error
}

func SetupLockedPlan(setup LockedPlanSetup) error {
	if err := FinalizeLockedPlan(setup.Plan, setup.Checker, setup.BuildQEMU); err != nil {
		runSetupCleanup(setup.Cleanup)
		return err
	}
	if setup.Logger != nil {
		if setup.Plan.ResumeState != nil {
			setup.Logger.Info("restoring saved vsock cid", "cid", setup.Plan.CID)
		} else {
			setup.Logger.Info("allocated vsock cid", "cid", setup.Plan.CID)
		}
	}
	if err := PrepareFilesystem(setup.Plan, setup.Logger); err != nil {
		runSetupCleanup(setup.Cleanup)
		return err
	}
	return nil
}

func runSetupCleanup(cleanup func() error) {
	if cleanup != nil {
		_ = cleanup()
	}
}
