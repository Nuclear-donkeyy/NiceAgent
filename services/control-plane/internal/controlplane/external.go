package controlplane

import "errors"

var ErrExternalAdapterNotImplemented = errors.New("external adapter boundary exists but is not implemented in the stdlib scaffold")
