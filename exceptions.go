package scout

import "errors"

// ErrNotSupported is returned when a driver is unknown or an operation is
// unsupported by the active engine. Mirrors scout's NotSupportedException.
var ErrNotSupported = errors.New("scout: driver or operation not supported")

// ErrScout is a generic Scout error (mirrors scout's ScoutException).
var ErrScout = errors.New("scout: error")

// IsNotSupported reports whether err is (or wraps) ErrNotSupported.
func IsNotSupported(err error) bool { return errors.Is(err, ErrNotSupported) }
