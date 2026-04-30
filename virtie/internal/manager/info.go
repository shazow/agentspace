package manager

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Info struct {
	ProcessList string
}

func (m *manager) collectGuestInfo(ctx context.Context, socketPath string, watchers ...*managedProcess) (Info, error) {
	if socketPath == "" {
		return Info{}, fmt.Errorf("guest agent socket is not configured")
	}

	timeout := 2 * m.effectiveQMPCommandTimeout()
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	infoCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client, err := m.connectGuestAgent(infoCtx, socketPath, watchers...)
	if err != nil {
		return Info{}, err
	}
	defer client.Disconnect()

	status, err := m.runGuestCommandStatus(infoCtx, client, "ps", guestPSPath, []string{"-eo", "pid,ppid,stat,comm,args"}, "process list")
	if err != nil {
		return Info{}, err
	}
	if status.ExitCode != 0 {
		return Info{}, fmt.Errorf("ps %q exited with status %d%s", "process list", status.ExitCode, guestExecOutputSuffix(status))
	}

	return Info{ProcessList: decodeGuestExecData(status.OutData)}, nil
}

func (m *manager) printGuestInfo(ctx context.Context, socketPath string, watchers ...*managedProcess) {
	info, err := m.collectGuestInfo(ctx, socketPath, watchers...)
	if err != nil {
		m.logger.Printf("guest info failed: %v", err)
		return
	}

	m.logger.Printf("guest info")
	processList := strings.TrimRight(info.ProcessList, "\n")
	if processList != "" {
		fmt.Fprintln(m.logger.Writer(), processList)
	}
}
