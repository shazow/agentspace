//go:build !linux

package manager

import "github.com/shazow/agentspace/virtie/internal/manager/launch"

type hostVSockCIDChecker struct{}

func newHostVSockCIDChecker() launch.VSockCIDChecker {
	return &hostVSockCIDChecker{}
}

func (c *hostVSockCIDChecker) Available(cid int) (bool, error) {
	return true, nil
}
