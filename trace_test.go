package dp100

import (
	"context"
	"errors"
	"testing"
)

func TestTraceDisableAndReadback(t *testing.T) {
	f := configurationFake(OutputSettings{Group: 9, Enabled: true, Limits: Limits{19110, 2663, 30500, 5050}})
	c := New(f)
	defer c.Close()
	var events []TraceEvent
	c.SetTrace(func(e TraceEvent) { events = append(events, e) })
	if err := c.SetEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Settings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(events) != 9 {
		t.Fatalf("events: %+v", events)
	}
	for i, e := range events {
		if e.Exchange != uint64(i/3+1) || e.Sequence != byte(i/3+1) {
			t.Fatalf("event: %+v", e)
		}
		if i > 0 && e.Elapsed < events[i-1].Elapsed {
			t.Fatal("non-monotonic elapsed")
		}
	}
	write, ack := events[3], events[5]
	if write.Stage != "write" || write.Report[5] != 0x29 || write.Report[6] != 0 || ack.Stage != "read" || !ack.Valid || !ack.Matched || len(ack.Payload) != 1 || ack.Payload[0] != 1 {
		t.Fatalf("write=%+v ack=%+v", write, ack)
	}
	// Trace owns its raw buffer; application cannot corrupt the transport write.
	write.Report[6] = 1
	if f.writes[1][6] != 0 {
		t.Fatal("trace buffer aliases transport")
	}
	c.SetTrace(nil)
	if _, err := c.Settings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(events) != 9 {
		t.Fatal("trace not disabled")
	}
}
func TestTraceMismatchAndMalformed(t *testing.T) {
	f := newFake()
	c := New(f)
	defer c.Close()
	var events []TraceEvent
	c.SetTrace(func(e TraceEvent) { events = append(events, e) })
	f.respond = func(b []byte) [][]byte {
		bad := reply(b[2], b[3], nil)
		bad[0] = 0
		return [][]byte{reply(commandMeasurements, b[3], make([]byte, 16)), bad}
	}
	_, err := c.Settings(context.Background())
	if !errors.Is(err, ErrProtocol) {
		t.Fatal(err)
	}
	if len(events) != 5 || events[2].Matched || !events[2].Valid || events[3].Valid || events[3].Err == nil || events[4].Stage != "error" {
		t.Fatalf("events=%+v", events)
	}
}
