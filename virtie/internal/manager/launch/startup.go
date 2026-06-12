package launch

import (
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

type RuntimeStartupFinalize struct {
	QEMU         *executor.Process
	QMP          qmpclient.Client
	MarkQMPReady func(time.Time)
	QuitTimeout  time.Duration
	Now          func() time.Time
}

func FinalizeRuntimeStartup(finalize RuntimeStartupFinalize) {
	now := finalize.Now
	if now == nil {
		now = time.Now
	}
	if finalize.MarkQMPReady != nil {
		finalize.MarkQMPReady(now())
	}
	if finalize.QEMU == nil || finalize.QMP == nil {
		return
	}
	finalize.QEMU.SetShutdown(func() error {
		return finalize.QMP.Quit(finalize.QuitTimeout)
	})
}
