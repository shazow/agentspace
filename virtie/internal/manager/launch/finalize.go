package launch

import (
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
