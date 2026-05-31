package app

import "errors"

var (
	ErrExternalAdapterNotImplemented = errors.New("external adapter boundary exists but is not implemented in the stdlib scaffold")
	ErrNotFound                      = errors.New("not found")
	ErrClosed                        = errors.New("closed")
	ErrAttemptMismatch               = errors.New("run attempt mismatch")
	ErrInvalidInput                  = errors.New("invalid input")
	ErrIdentityConflict              = errors.New("identity conflict")
)
