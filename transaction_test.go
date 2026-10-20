package dp100

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// Notifies the test when a request actually enters I/O; no sleeps are needed
// to assume the gate has been acquired.
type notifyingTransport struct {
	*fakeTransport
	entered chan struct{}
	once    sync.Once
}

func (f *notifyingTransport) WriteReport(ctx context.Context, b []byte) error {
	err := f.fakeTransport.WriteReport(ctx, b)
	f.once.Do(func() { close(f.entered) })
	return err
}
func TestQueuedCancellationDoesNotTransmit(t *testing.T) {
	f := &notifyingTransport{fakeTransport: newFake(), entered: make(chan struct{})}
	c := New(f)
	defer c.Close()
	done := make(chan error, 1)
	go func() { _, err := c.Info(context.Background()); done <- err }()
	<-f.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Info(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	f.mu.Lock()
	count := len(f.writes)
	f.mu.Unlock()
	if count != 1 {
		t.Fatalf("queued request transmitted; writes=%d", count)
	}
	c.Close()
	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}
func TestLateReplyAfterTimeout(t *testing.T) {
	f := newFake()
	c := New(f)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := c.Info(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	// Old response and telemetry arrive before the matching new reply.
	f.replies <- reply(commandInfo, 1, make([]byte, 40))
	f.respond = func(b []byte) [][]byte {
		return [][]byte{reply(commandMeasurements, 0, make([]byte, 16)), reply(b[2], b[3], make([]byte, 40))}
	}
	if _, err := c.Info(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 2 {
		t.Fatal("unexpected retry")
	}
}
func TestOutputHelperReadFailureDoesNotWrite(t *testing.T) {
	for _, data := range [][]byte{nil, settingsPayload(15, false, Limits{})} {
		f := newFake()
		f.respond = func(b []byte) [][]byte { return [][]byte{reply(b[2], b[3], data)} }
		c := New(f)
		err := c.SetEnabled(context.Background(), true)
		c.Close()
		if !errors.Is(err, ErrProtocol) {
			t.Fatal(err)
		}
		if len(f.writes) != 1 || f.writes[0][4] != 1 {
			t.Fatal("read failure caused a write")
		}
	}
}
func TestMalformedACKIsUncertain(t *testing.T) {
	f := newFake()
	f.respond = func(b []byte) [][]byte { return [][]byte{reply(b[2], b[3], nil)} }
	c := New(f)
	defer c.Close()
	err := c.SetOutput(context.Background(), OutputSettings{})
	var op *OpError
	if !errors.Is(err, ErrProtocol) || !errors.As(err, &op) || !op.Transmitted {
		t.Fatal(err)
	}
}
