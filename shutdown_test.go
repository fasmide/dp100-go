package dp100

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestDisableExactPacketAllGroups(t *testing.T) {
	for group := byte(0); group <= 9; group++ {
		s := OutputSettings{Group: group, Enabled: true, Limits: Limits{19110, 2663, 30500, 5050}}
		f := configurationFake(s)
		c := New(f)
		if err := c.SetEnabled(context.Background(), false); err != nil {
			t.Fatal(err)
		}
		wantRead, _ := encode(frame{commandSettings, 1, []byte{0x80}})
		// Explicit wire fields, independent of settingsPayload and boolByte.
		wantWrite, _ := encode(frame{commandSettings, 2, []byte{group | 0x20, 0, 0xa6, 0x4a, 0x67, 0x0a, 0x24, 0x77, 0xba, 0x13}})
		if len(f.writes) != 2 || !bytes.Equal(f.writes[0], wantRead) || !bytes.Equal(f.writes[1], wantWrite) {
			t.Fatalf("group %d: writes %x", group, f.writes)
		}
		c.Close()
	}
}

func TestDisableACKFilteringAtWrap(t *testing.T) {
	f := newFake()
	c := New(f)
	defer c.Close()
	c.sequence = 254 // live query 255, disable write 0
	f.respond = func(b []byte) [][]byte {
		if b[4] == 1 {
			return [][]byte{reply(b[2], b[3], settingsPayload(9, true, Limits{5000, 100, 30500, 5050}))}
		}
		return [][]byte{
			reply(commandMeasurements, b[3], make([]byte, 16)), // unsolicited, same sequence
			reply(commandSettings, 255, []byte{0}),             // late reply across wrap
			reply(commandSettings, 1, []byte{0}),               // wrong sequence
			reply(commandSettings, 0, []byte{1}),
		}
	}
	if err := c.SetEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if f.writes[1][3] != 0 {
		t.Fatal("did not test wrap")
	}
}

func TestDisableInvalidACK(t *testing.T) {
	for _, ack := range [][]byte{nil, {1, 1}, {0}, {2}} {
		f := newFake()
		c := New(f)
		f.respond = func(b []byte) [][]byte {
			if b[4] == 1 {
				return [][]byte{reply(b[2], b[3], settingsPayload(0, true, Limits{}))}
			}
			return [][]byte{reply(b[2], b[3], ack)}
		}
		err := c.SetEnabled(context.Background(), false)
		if len(ack) != 1 {
			if !errors.Is(err, ErrProtocol) {
				t.Fatal(err)
			}
		} else {
			var dev *DeviceError
			if !errors.As(err, &dev) || dev.Status != ack[0] {
				t.Fatal(err)
			}
		}
		c.Close()
	}
}

// The wire has no generation identifier. A duplicate delayed by a FULL sequence
// cycle is indistinguishable from a current ACK. This test records a limitation,
// not a claim that this happened on hardware.
func TestFullCycleStaleACKIsIndistinguishable(t *testing.T) {
	f := newFake()
	c := New(f)
	defer c.Close()
	f.respond = func(b []byte) [][]byte { return [][]byte{reply(b[2], b[3], []byte{1})} }
	if err := c.SetOutput(context.Background(), OutputSettings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	oldACK := reply(commandSettings, 1, []byte{1})
	for i := 0; i < 255; i++ {
		if err := c.SetOutput(context.Background(), OutputSettings{}); err != nil {
			t.Fatal(err)
		}
	}
	f.respond = nil
	f.replies <- oldACK
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.SetOutput(ctx, OutputSettings{}); err != nil {
		t.Fatal(err)
	}
	if f.writes[256][3] != 1 {
		t.Fatal("sequence not reused")
	}
}

func TestACKDoesNotVerifyState(t *testing.T) {
	f := configurationFake(OutputSettings{Group: 0, Enabled: true, Limits: Limits{19110, 2663, 30500, 5050}})
	c := New(f)
	defer c.Close()
	if err := c.SetEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		s, err := c.Settings(context.Background())
		if err != nil || !s.Enabled {
			t.Fatalf("state: %+v %v", s, err)
		}
	}
	if len(f.writes) != 7 {
		t.Fatal("unexpected write retry")
	}
}
