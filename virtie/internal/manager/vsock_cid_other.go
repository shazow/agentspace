//go:build !linux

package manager

type hostVSockCIDChecker struct{}

func newHostVSockCIDChecker() vsockCIDChecker {
	return &hostVSockCIDChecker{}
}

func (c *hostVSockCIDChecker) Available(cid int) (bool, error) {
	return true, nil
}
