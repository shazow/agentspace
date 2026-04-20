package virtie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	doQMP "github.com/digitalocean/go-qemu/qmp"
	govmmQemu "github.com/kata-containers/govmm/qemu"
)

const (
	DefaultQMPRetryDelay     = 200 * time.Millisecond
	DefaultQMPConnectTimeout = 500 * time.Millisecond
	DefaultQMPQuitTimeout    = 500 * time.Millisecond
)

var errQMPTimeout = errors.New("qmp operation timed out")

type QMPClient interface {
	Quit(timeout time.Duration) error
	Disconnect() error
}

type QMPDialer interface {
	Dial(ctx context.Context, socketPath string, timeout time.Duration) (QMPClient, error)
}

type socketMonitorDialer struct{}

type socketMonitorClient struct {
	monitor *doQMP.SocketMonitor
}

func (d *socketMonitorDialer) Dial(ctx context.Context, socketPath string, timeout time.Duration) (QMPClient, error) {
	monitor, err := doQMP.NewSocketMonitor("unix", socketPath, timeout)
	if err != nil {
		return nil, err
	}
	if err := runQMPMonitorOp(ctx, timeout, monitor.Disconnect, monitor.Connect); err != nil {
		_ = monitor.Disconnect()
		if errors.Is(err, errQMPTimeout) {
			return nil, fmt.Errorf("qmp connect timed out after %s", timeout)
		}
		return nil, err
	}
	return &socketMonitorClient{monitor: monitor}, nil
}

func (c *socketMonitorClient) Quit(timeout time.Duration) error {
	payload, err := json.Marshal(doQMP.Command{Execute: "quit"})
	if err != nil {
		return fmt.Errorf("encode qmp quit: %w", err)
	}
	err = runQMPMonitorOp(context.Background(), timeout, c.Disconnect, func() error {
		if _, err := c.monitor.Run(payload); err != nil {
			return fmt.Errorf("qmp quit: %w", err)
		}
		return nil
	})
	if errors.Is(err, errQMPTimeout) {
		return fmt.Errorf("qmp quit timed out after %s", timeout)
	}
	return err
}

func (c *socketMonitorClient) Disconnect() error {
	if c == nil || c.monitor == nil {
		return nil
	}
	_ = c.monitor.Disconnect()
	return nil
}

func runQMPMonitorOp(ctx context.Context, timeout time.Duration, abort func() error, fn func() error) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- fn()
	}()

	var timer *time.Timer
	var timerCh <-chan time.Time
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		timerCh = timer.C
		defer timer.Stop()
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		if abort != nil {
			_ = abort()
		}
		return ctx.Err()
	case <-timerCh:
		if abort != nil {
			_ = abort()
		}
		return fmt.Errorf("%w after %s", errQMPTimeout, timeout)
	}
}

func buildQEMUSpec(manifest *Manifest, cid int) (ProcessSpec, error) {
	qemu := manifest.ResolvedQEMU()
	args, err := buildQEMUArgs(qemu, cid)
	if err != nil {
		return ProcessSpec{}, err
	}

	return ProcessSpec{
		Name:   "qemu",
		Path:   qemu.BinaryPath,
		Args:   args,
		Dir:    manifest.Paths.WorkingDir,
		Stdout: os.Stderr,
		Stderr: os.Stderr,
	}, nil
}

func buildQEMUArgs(qemu ManifestQEMU, cid int) ([]string, error) {
	config := &govmmQemu.Config{
		Machine: govmmQemu.Machine{
			Type: qemu.Machine.Type,
		},
	}

	transport := func(value string) (govmmQemu.VirtioTransport, error) {
		switch value {
		case "pci":
			return govmmQemu.TransportPCI, nil
		case "mmio":
			return govmmQemu.TransportMMIO, nil
		case "ccw":
			return govmmQemu.TransportCCW, nil
		default:
			return "", fmt.Errorf("unsupported qemu transport %q", value)
		}
	}

	args := make([]string, 0, 64)

	args = append(args, "-name", qemu.Name)

	machineArg := qemu.Machine.Type
	if len(qemu.Machine.Options) > 0 {
		machineArg = machineArg + "," + strings.Join(qemu.Machine.Options, ",")
	}
	args = append(args, "-M", machineArg)

	args = append(args, "-m", fmt.Sprintf("%d", qemu.Memory.SizeMiB))
	args = append(args, "-smp", fmt.Sprintf("%d", qemu.SMP.CPUs))

	if qemu.Knobs.NoDefaults {
		args = append(args, "-nodefaults")
	}
	if qemu.Knobs.NoUserConfig {
		args = append(args, "-no-user-config")
	}
	if qemu.Knobs.NoReboot {
		args = append(args, "-no-reboot")
	}

	args = append(args, "-kernel", qemu.Kernel.Path)
	args = append(args, "-initrd", qemu.Kernel.InitrdPath)

	if qemu.Console.StdioChardev {
		args = append(args, "-chardev", "stdio,id=stdio,signal=off")
	}

	rngTransport, err := transport(qemu.Devices.RNG.Transport)
	if err != nil {
		return nil, err
	}
	args = append(args, govmmQemu.RngDevice{
		ID:        qemu.Devices.RNG.ID,
		Transport: rngTransport,
	}.QemuParams(config)...)

	if qemu.MachineID != nil && *qemu.MachineID != "" {
		args = append(args, "-smbios", fmt.Sprintf("type=1,uuid=%s", *qemu.MachineID))
	}

	if qemu.Console.SerialConsole {
		args = append(args, "-serial", "chardev:stdio")
	}
	if qemu.CPU.EnableKVM {
		args = append(args, "-enable-kvm")
	}

	args = append(args, "-cpu", qemu.CPU.Model)

	if qemu.Kernel.Params != "" {
		args = append(args, "-append", qemu.Kernel.Params)
	}
	if qemu.Devices.I8042 {
		args = append(args, "-device", "i8042")
	}
	if qemu.Knobs.NoGraphic {
		args = append(args, "-nographic")
	}
	if qemu.Knobs.SeccompSandbox {
		args = append(args, "-sandbox", "on")
	}

	args = append(args, "-qmp", fmt.Sprintf("unix:%s,server,nowait", qemu.QMP.SocketPath))

	switch qemu.Memory.Backend {
	case "", "default":
		// No extra memory object required.
	case "memfd":
		args = append(args, "-numa", "node,memdev=mem")
		args = append(args, "-object", fmt.Sprintf("memory-backend-memfd,id=mem,size=%dM,share=%s", qemu.Memory.SizeMiB, onOff(qemu.Memory.Shared)))
	default:
		return nil, fmt.Errorf("unsupported qemu memory backend %q", qemu.Memory.Backend)
	}

	if qemu.Devices.Balloon != nil {
		balloonTransport, err := transport(qemu.Devices.Balloon.Transport)
		if err != nil {
			return nil, err
		}
		driver := govmmQemu.BalloonDeviceTransport[balloonTransport]
		deviceParams := []string{
			driver,
			fmt.Sprintf("id=%s", qemu.Devices.Balloon.ID),
			fmt.Sprintf("deflate-on-oom=%s", onOff(qemu.Devices.Balloon.DeflateOnOOM)),
			fmt.Sprintf("free-page-reporting=%s", onOff(qemu.Devices.Balloon.FreePageReporting)),
		}
		args = append(args, "-device", strings.Join(deviceParams, ","))
	}

	for _, share := range qemu.Devices.VirtioFS {
		shareTransport, err := transport(share.Transport)
		if err != nil {
			return nil, err
		}
		args = append(args, govmmQemu.VhostUserDevice{
			SocketPath:    share.SocketPath,
			CharDevID:     "char-" + share.ID,
			Tag:           share.Tag,
			VhostUserType: govmmQemu.VhostUserFS,
			Transport:     shareTransport,
		}.QemuParams(config)...)
	}

	for _, block := range qemu.Devices.Block {
		blockTransport, err := transport(block.Transport)
		if err != nil {
			return nil, err
		}
		driver := govmmQemu.VirtioBlockTransport[blockTransport]

		driveParams := []string{
			fmt.Sprintf("id=%s", block.ID),
			"format=raw",
			fmt.Sprintf("file=%s", block.ImagePath),
			"if=none",
		}
		if block.AIO != "" {
			driveParams = append(driveParams, fmt.Sprintf("aio=%s", block.AIO))
		}
		driveParams = append(driveParams, "discard=unmap")
		if block.Cache != nil {
			driveParams = append(driveParams, fmt.Sprintf("cache=%s", *block.Cache))
		}
		driveParams = append(driveParams, fmt.Sprintf("read-only=%s", onOff(block.ReadOnly)))

		deviceParams := []string{
			driver,
			fmt.Sprintf("drive=%s", block.ID),
		}
		if block.Serial != nil {
			deviceParams = append(deviceParams, fmt.Sprintf("serial=%s", *block.Serial))
		}

		args = append(args, "-drive", strings.Join(driveParams, ","))
		args = append(args, "-device", strings.Join(deviceParams, ","))
	}

	for _, netdev := range qemu.Devices.Network {
		netTransport, err := transport(netdev.Transport)
		if err != nil {
			return nil, err
		}
		driver := govmmQemu.VirtioNetTransport[netTransport]

		netdevParams := []string{
			netdev.Backend,
			fmt.Sprintf("id=%s", netdev.ID),
		}
		netdevParams = append(netdevParams, netdev.NetdevOptions...)

		deviceParams := []string{
			driver,
			fmt.Sprintf("netdev=%s", netdev.ID),
			fmt.Sprintf("mac=%s", netdev.MacAddress),
		}
		if netdev.RomFile != nil {
			deviceParams = append(deviceParams, fmt.Sprintf("romfile=%s", *netdev.RomFile))
		}
		if netdev.MQVectors > 0 {
			deviceParams = append(deviceParams, "mq=on", fmt.Sprintf("vectors=%d", netdev.MQVectors))
		}

		args = append(args, "-netdev", strings.Join(netdevParams, ","))
		args = append(args, "-device", strings.Join(deviceParams, ","))
	}

	vsockTransport, err := transport(qemu.Devices.VSOCK.Transport)
	if err != nil {
		return nil, err
	}
	args = append(args, govmmQemu.VSOCKDevice{
		ID:        qemu.Devices.VSOCK.ID,
		ContextID: uint64(cid),
		Transport: vsockTransport,
	}.QemuParams(config)...)

	args = append(args, qemu.PassthroughArgs...)

	return args, nil
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
