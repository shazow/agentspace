package manager

import (
	"fmt"
	"os"
	"strings"

	govmmQemu "github.com/kata-containers/govmm/qemu"
	"github.com/shazow/agentspace/virtie/internal/manifest"
)

func buildQEMUSpec(manifest *manifest.Manifest, cid int) (processSpec, error) {
	return buildQEMUSpecWithIncoming(manifest, cid, false)
}

func buildIncomingQEMUSpec(manifest *manifest.Manifest, cid int) (processSpec, error) {
	return buildQEMUSpecWithIncoming(manifest, cid, true)
}

func buildQEMUSpecWithIncoming(manifest *manifest.Manifest, cid int, incoming bool) (processSpec, error) {
	qemu, err := manifest.ResolvedQEMU()
	if err != nil {
		return processSpec{}, err
	}

	args, err := buildQEMUArgs(qemu, cid, incoming)
	if err != nil {
		return processSpec{}, err
	}

	return processSpec{
		Name:         "qemu",
		Path:         qemu.BinaryPath,
		Args:         args,
		Dir:          manifest.Paths.WorkingDir,
		ProcessGroup: true,
		Stdout:       os.Stderr,
		Stderr:       os.Stderr,
	}, nil
}

func buildQEMUArgs(qemu manifest.QEMU, cid int, incoming bool) ([]string, error) {
	config := &govmmQemu.Config{
		Machine: govmmQemu.Machine{
			Type: qemu.Machine.Type,
		},
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

	rngTransport, err := resolveQEMUTransport(qemu.Devices.RNG.Transport)
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

	args, err = appendOptionalFeatureQEMUArgs(qemu, config, args)
	if err != nil {
		return nil, err
	}

	for _, share := range qemu.Devices.VirtioFS {
		shareTransport, err := resolveQEMUTransport(share.Transport)
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
		blockTransport, err := resolveQEMUTransport(block.Transport)
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
		netTransport, err := resolveQEMUTransport(netdev.Transport)
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

	vsockTransport, err := resolveQEMUTransport(qemu.Devices.VSOCK.Transport)
	if err != nil {
		return nil, err
	}
	args = append(args, govmmQemu.VSOCKDevice{
		ID:        qemu.Devices.VSOCK.ID,
		ContextID: uint64(cid),
		Transport: vsockTransport,
	}.QemuParams(config)...)

	if incoming {
		args = append(args, "-incoming", "defer")
	}

	args = append(args, qemu.PassthroughArgs...)

	return args, nil
}

func resolveQEMUTransport(value string) (govmmQemu.VirtioTransport, error) {
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

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
