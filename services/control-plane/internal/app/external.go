package app

import "errors"

var (
	ErrExternalAdapterNotImplemented = errors.New("external adapter boundary exists but is not implemented in the stdlib scaffold")
	ErrNotFound                      = errors.New("not found")
	ErrClosed                        = errors.New("closed")
)
