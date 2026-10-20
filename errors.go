package dp100

import (
	"errors"
	"fmt"
)

var (
	// ErrClosed means the Client has been closed.
	ErrClosed = errors.New("dp100: client closed")
	// ErrProtocol means a response failed framing, CRC, or payload validation.
	ErrProtocol = errors.New("dp100: invalid protocol response")
	// ErrOutputEnabled means a configuration operation requires output OFF.
	ErrOutputEnabled = errors.New("dp100: operation requires output off")
	// ErrUnsupported means the platform has no built-in HID backend.
	ErrUnsupported = errors.New("dp100: native HID backend requires Linux")
)

// OpError describes a failed HID exchange. Unwrap preserves errors.Is/As for
// context cancellation, device removal, and ErrProtocol.
//
// Transmitted means a write was attempted, not necessarily completed. For a
// settings command, this means the device MAY have applied it. Never retry
// automatically based on this flag; verify the device's state first.
type OpError struct {
	Command     byte
	Transmitted bool
	Err         error
}

func (e *OpError) Error() string {
	return fmt.Sprintf("dp100: command %#02x: %v", e.Command, e.Err)
}
func (e *OpError) Unwrap() error { return e.Err }
