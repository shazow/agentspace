package manager

import (
	"errors"
	"net"
	"time"
)

const (
	defaultQMPRetryDelay       = 200 * time.Millisecond
	defaultQMPConnectTimeout   = 500 * time.Millisecond
	defaultQMPQuitTimeout      = 500 * time.Millisecond
	defaultQMPMigrationTimeout = 30 * time.Second
)

func appendQMPDelimiter(command []byte) []byte {
	if len(command) > 0 && command[len(command)-1] == '\n' {
		return command
	}
	return append(append([]byte(nil), command...), '\n')
}

func isTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
