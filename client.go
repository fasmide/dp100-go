// Package dp100 controls the Alientek DP100 programmable power supply over HID.
//
// Values use explicit integer wire units (mV, mA, and tenths of watts). Opening
// and closing a Client do not change the output. Writes are never retried:
// after a timeout the device may have applied the command even without an ACK.
package dp100

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Transport exchanges whole HID reports. Implementations must support concurrent
// Close, which must unblock pending I/O. ReadReport returns 64 bytes without a
// report ID; WriteReport receives 65 bytes including a leading zero report ID.
// I/O must honor context cancellation. A Client serializes all other access.
// Implement this interface to use a different HID backend or operating system.
type Transport interface {
	ReadReport(context.Context) ([]byte, error)
	WriteReport(context.Context, []byte) error
	Close() error
}

// DeviceError means the device rejected a write. Only status 1 means success.
type DeviceError struct{ Command, Status byte }

func (e *DeviceError) Error() string {
	return fmt.Sprintf("dp100: command %#02x rejected (status %#02x)", e.Command, e.Status)
}

// Client is safe for concurrent use and must not be copied. Commands are
// serialized because the device supports only one request at a time.
// Use one Client per physical device. The zero value is not usable; use New/Open.
type Client struct {
	transport Transport
	gate      chan struct{}
	done      chan struct{}
	once      sync.Once
	closeErr  error
	sequence  byte
}

// New takes ownership of t. Each operation has a one-second maximum duration
// (including waiting for another operation), or the context's earlier deadline.
// New panics if t is nil. No commands or background goroutines are started.
func New(t Transport) *Client {
	if t == nil {
		panic("dp100: nil transport")
	}
	return &Client{transport: t, gate: make(chan struct{}, 1), done: make(chan struct{})}
}

// Close releases the transport without switching off the output or issuing a
// protocol disconnect. It is safe to call multiple times, including during I/O.
func (c *Client) Close() error {
	c.once.Do(func() { close(c.done); c.closeErr = c.transport.Close() })
	return c.closeErr
}

// transaction holds the request gate across compound read/modify/write helpers.
func (c *Client) transaction(ctx context.Context, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	}
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(ctx)
}

func (c *Client) exchange(ctx context.Context, command byte, data []byte) ([]byte, error) {
	var payload []byte
	err := c.transaction(ctx, func(ctx context.Context) error {
		var err error
		payload, err = c.exchangeLocked(ctx, command, data)
		return err
	})
	return payload, err
}

// exchangeLocked requires the transaction gate and a bounded context.
func (c *Client) exchangeLocked(ctx context.Context, command byte, data []byte) (payload []byte, err error) {
	transmitted := false
	defer func() {
		if err != nil {
			err = &OpError{Command: command, Transmitted: transmitted, Err: err}
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-c.done:
		return nil, ErrClosed
	default:
	}
	c.sequence++
	request := frame{command, c.sequence, data}
	report, err := encode(request)
	if err != nil {
		return nil, err
	}
	transmitted = true
	if err = c.transport.WriteReport(ctx, report); err != nil {
		return nil, c.ioError(ctx, "write", err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		report, err := c.transport.ReadReport(ctx)
		if err != nil {
			return nil, c.ioError(ctx, "read", err)
		}
		response, err := decode(report)
		if err != nil {
			return nil, err
		}
		// Ignore unsolicited telemetry and late replies from earlier transactions.
		if response.command != command || response.sequence != request.sequence {
			continue
		}
		return response.data, nil
	}
}

func (c *Client) ioError(ctx context.Context, operation string, err error) error {
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%s report: %w", operation, err)
}

func (c *Client) write(ctx context.Context, command byte, data []byte) error {
	response, err := c.exchange(ctx, command, data)
	if err != nil {
		return err
	}
	return checkACK(command, response)
}

func checkACK(command byte, response []byte) error {
	if err := requireLength(response, 1); err != nil {
		return &OpError{Command: command, Transmitted: true, Err: err}
	}
	if response[0] != 1 {
		return &DeviceError{command, response[0]}
	}
	return nil
}
