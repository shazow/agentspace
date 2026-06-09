package control

import (
	"errors"
	"os"
	"syscall"
)

func FailedPrecondition(err error) error {
	return &RPCError{Code: ErrFailedPrecondition, Message: err.Error()}
}

func IsSocketUnavailable(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}

func IsUnsupported(err error) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == ErrUnsupported
}
