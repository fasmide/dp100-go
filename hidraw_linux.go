//go:build linux

package dp100

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// Open opens a Linux hidraw device and verifies its USB vendor/product IDs.
// No protocol commands are sent. The caller must call Close.
func Open(path string) (*Client, error) {
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("dp100: open: %w", err)
	}
	// Linux HIDIOCGRAWINFO: struct hidraw_devinfo { uint32 bustype; int16 vendor, product; }.
	var info struct {
		Bus             uint32
		Vendor, Product uint16
	}
	var ioctlErr syscall.Errno
	raw, err := f.SyscallConn()
	if err == nil {
		err = raw.Control(func(fd uintptr) {
			_, _, ioctlErr = syscall.Syscall(syscall.SYS_IOCTL, fd, 0x80084803, uintptr(unsafe.Pointer(&info)))
		})
	}
	if err == nil && ioctlErr != 0 {
		err = ioctlErr
	}
	if err == nil && (info.Bus != 3 || info.Vendor != VendorID || info.Product != ProductID) {
		err = fmt.Errorf("not a DP100 USB device (bus %d, VID:PID %04x:%04x)", info.Bus, info.Vendor, info.Product)
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("dp100: identify %s: %w", path, err)
	}
	return New(&hidraw{f}), nil
}

type hidraw struct{ file *os.File }

func (h *hidraw) Close() error { return h.file.Close() }

// Short poll deadlines allow cancellation without a reader goroutine or a
// cancellation callback racing with the next transaction's deadlines.
func pollDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(10 * time.Millisecond)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		return d
	}
	return deadline
}

func (h *hidraw) ReadReport(ctx context.Context) ([]byte, error) {
	b := make([]byte, reportSize)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := h.file.SetReadDeadline(pollDeadline(ctx)); err != nil {
			return nil, err
		}
		n, err := h.file.Read(b)
		if n > 0 {
			return b[:n], err
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			continue
		}
		if err == nil {
			err = io.ErrNoProgress
		}
		return nil, err
	}
}
func (h *hidraw) WriteReport(ctx context.Context, b []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Never retry a write with an uncertain outcome.
	deadline, _ := ctx.Deadline()
	if err := h.file.SetWriteDeadline(deadline); err != nil {
		return err
	}
	cancelled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = h.file.SetWriteDeadline(time.Now())
		close(cancelled)
	})
	defer func() {
		if !stop() {
			<-cancelled
		}
	}()
	n, err := h.file.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
