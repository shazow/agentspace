package manager

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Info struct {
	ProcessList string
}

type guestProcess struct {
	User    string
	Command string
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

	status, err := m.runGuestCommandStatus(infoCtx, client, "ps", guestPSPath, []string{"-eo", "user=,comm="}, "process list")
	if err != nil {
		return Info{}, err
	}
	if status.ExitCode != 0 {
		return Info{}, fmt.Errorf("ps %q exited with status %d%s", "process list", status.ExitCode, guestExecOutputSuffix(status))
	}

	return Info{ProcessList: formatGuestProcesses(parseGuestProcesses(decodeGuestExecData(status.OutData)))}, nil
}

func (m *manager) printGuestInfo(ctx context.Context, socketPath string, watchers ...*managedProcess) {
	info, err := m.collectGuestInfo(ctx, socketPath, watchers...)
	if err != nil {
		m.logger.Info("guest info failed", "err", err)
		return
	}

	m.logger.Info("guest info")
	processList := strings.TrimRight(info.ProcessList, "\n")
	if processList != "" {
		fmt.Fprintln(m.outputWriter(), processList)
	}
}

func parseGuestProcesses(output string) []guestProcess {
	var processes []guestProcess
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		processes = append(processes, guestProcess{
			User:    fields[0],
			Command: fields[1],
		})
	}
	return processes
}

func formatGuestProcesses(processes []guestProcess) string {
	if len(processes) == 0 {
		return ""
	}

	sort.Slice(processes, func(i, j int) bool {
		if processes[i].User != processes[j].User {
			return processes[i].User < processes[j].User
		}
		return processes[i].Command < processes[j].Command
	})

	var builder strings.Builder
	builder.WriteString("USER COMMAND\n")
	for _, process := range processes {
		builder.WriteString(process.User)
		builder.WriteByte(' ')
		builder.WriteString(process.Command)
		builder.WriteByte('\n')
	}
	return strings.TrimRight(builder.String(), "\n")
}
