package virtie

import manifestpkg "github.com/shazow/agentspace/virtie/manifest"

const (
	DefaultVSockCIDStart = manifestpkg.DefaultVSockCIDStart
	DefaultVSockCIDEnd   = manifestpkg.DefaultVSockCIDEnd
	DefaultVolumeFSType  = manifestpkg.DefaultVolumeFSType
)

func LoadManifest(path string) (*Manifest, error) {
	return manifestpkg.LoadManifest(path)
}

type Manifest = manifestpkg.Manifest
type ManifestCommand = manifestpkg.ManifestCommand
type ManifestIdentity = manifestpkg.ManifestIdentity
type ManifestPaths = manifestpkg.ManifestPaths
type ManifestPersistence = manifestpkg.ManifestPersistence
type ManifestQEMU = manifestpkg.ManifestQEMU
type ManifestQEMUBlockDevice = manifestpkg.ManifestQEMUBlockDevice
type ManifestQEMUCPU = manifestpkg.ManifestQEMUCPU
type ManifestQEMUConsole = manifestpkg.ManifestQEMUConsole
type ManifestQEMUDevices = manifestpkg.ManifestQEMUDevices
type ManifestQEMUKernel = manifestpkg.ManifestQEMUKernel
type ManifestQEMUKnobs = manifestpkg.ManifestQEMUKnobs
type ManifestQEMUMachine = manifestpkg.ManifestQEMUMachine
type ManifestQEMUMemory = manifestpkg.ManifestQEMUMemory
type ManifestQEMUNetDevice = manifestpkg.ManifestQEMUNetDevice
type ManifestQEMUQMP = manifestpkg.ManifestQEMUQMP
type ManifestQEMURNGDevice = manifestpkg.ManifestQEMURNGDevice
type ManifestQEMUSMP = manifestpkg.ManifestQEMUSMP
type ManifestQEMUVirtioFSShare = manifestpkg.ManifestQEMUVirtioFSShare
type ManifestQEMUVSOCKDevice = manifestpkg.ManifestQEMUVSOCKDevice
type ManifestSSH = manifestpkg.ManifestSSH
type ManifestVirtioFS = manifestpkg.ManifestVirtioFS
type ManifestVirtioFSDaemon = manifestpkg.ManifestVirtioFSDaemon
type ManifestVolume = manifestpkg.ManifestVolume
type ManifestVSock = manifestpkg.ManifestVSock
type ManifestVSockCIDRange = manifestpkg.ManifestVSockCIDRange
