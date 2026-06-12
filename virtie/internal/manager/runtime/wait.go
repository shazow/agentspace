package runtime

import "errors"

var ErrForegroundWaitNotConfigured = errors.New("runtime foreground wait is not configured")
