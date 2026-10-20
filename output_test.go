package dp100

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func outputFake() *fakeTransport {
	f := newFake()
	settings := OutputSettings{Group: 3, Enabled: true, Limits: Limits{5000, 500, 30500, 5050}}
	f.respond = func(b []byte) [][]byte {
		if b[4] == 1 {
			return [][]byte{reply(b[2], b[3], settingsPayload(settings.Group, settings.Enabled, settings.Limits))}
		}
		return [][]byte{reply(b[2], b[3], []byte{1})}
	}
	return f
}
func TestOutputHelpers(t *testing.T) {
	for _, tc := range []struct {
		name             string
		call             func(*Client) error
		enabled          bool
		voltage, current uint16
	}{
		{"disable", func(c *Client) error { return c.SetEnabled(context.Background(), false) }, false, 5000, 500},
		{"enable", func(c *Client) error { return c.SetEnabled(context.Background(), true) }, true, 5000, 500},
		{"setpoints", func(c *Client) error { return c.SetSetpoints(context.Background(), 12000, 1000) }, true, 12000, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := outputFake()
			c := New(f)
			defer c.Close()
			if err := tc.call(c); err != nil {
				t.Fatal(err)
			}
			if len(f.writes) != 2 {
				t.Fatal("expected read then write")
			}
			b := f.writes[1]
			if b[5] != 0x23 || b[6] != boolByte(tc.enabled) || uint16At(b, 7) != tc.voltage || uint16At(b, 9) != tc.current || uint16At(b, 11) != 30500 || uint16At(b, 13) != 5050 {
				t.Fatalf("write: %x", b)
			}
		})
	}
}
func TestSetpointsValidation(t *testing.T) {
	f := newFake()
	c := New(f)
	defer c.Close()
	if err := c.SetSetpoints(context.Background(), 30001, 0); err == nil {
		t.Fatal("invalid setpoint accepted")
	}
	if len(f.writes) != 0 {
		t.Fatal("invalid setpoint performed I/O")
	}
}
func TestOutputHelperSerializesPair(t *testing.T) {
	f := outputFake()
	c := New(f)
	defer c.Close()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.SetEnabled(context.Background(), false); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for i, b := range f.writes {
		if (i%2 == 0) != (b[4] == 1) {
			t.Fatalf("interleaved transaction at %d: %x", i, b)
		}
	}
}
func TestUncertainWriteError(t *testing.T) {
	f := newFake()
	c := New(f)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	err := c.SetOutput(ctx, OutputSettings{})
	var op *OpError
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &op) || !op.Transmitted || op.Command != commandSettings {
		t.Fatalf("error: %#v", err)
	}
	if len(f.writes) != 1 {
		t.Fatal("write retried")
	}
}
func TestProtocolError(t *testing.T) {
	f := newFake()
	f.respond = func(b []byte) [][]byte { r := reply(b[2], b[3], nil); r[4] ^= 1; return [][]byte{r} }
	c := New(f)
	defer c.Close()
	_, err := c.Info(context.Background())
	var op *OpError
	if !errors.Is(err, ErrProtocol) || !errors.As(err, &op) || !op.Transmitted {
		t.Fatal(err)
	}
}
func TestSequenceWrap(t *testing.T) {
	f := newFake()
	f.respond = func(b []byte) [][]byte { return [][]byte{reply(b[2], b[3], make([]byte, 40))} }
	c := New(f)
	defer c.Close()
	for i := 0; i < 260; i++ {
		if _, err := c.Info(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if f.writes[254][3] != 255 || f.writes[255][3] != 0 || f.writes[256][3] != 1 {
		t.Fatal("sequence did not wrap")
	}
}
