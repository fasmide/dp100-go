package dp100

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitEnabledDelayedReadback(t *testing.T) {
	f := newFake()
	c := New(f)
	defer c.Close()
	reads := 0
	f.respond = func(b []byte) [][]byte {
		if b[4] == 10 {
			return [][]byte{reply(b[2], b[3], []byte{1})}
		}
		reads++
		return [][]byte{reply(b[2], b[3], settingsPayload(3, reads <= 3, Limits{5000, 100, 5500, 200}))}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.SetEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	s, err := c.WaitEnabled(ctx, false, time.Microsecond)
	if err != nil || s.Enabled || s.Group != 3 || reads != 4 {
		t.Fatalf("%+v %v reads=%d", s, err, reads)
	}
	writes := 0
	for _, b := range f.writes {
		if b[4] == 10 {
			writes++
		}
	}
	if writes != 1 {
		t.Fatalf("writes=%d", writes)
	}
}
func TestWaitEnabledNeverChanges(t *testing.T) {
	f := configurationFake(OutputSettings{Enabled: true, Limits: Limits{19110, 2663, 30500, 5050}})
	c := New(f)
	defer c.Close()
	if err := c.SetEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	s, err := c.WaitEnabled(ctx, false, time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || !s.Enabled {
		t.Fatalf("%+v %v", s, err)
	}
	writes := 0
	for _, b := range f.writes {
		if b[4] == 10 {
			writes++
		}
	}
	if writes != 1 {
		t.Fatal("wait retried a write")
	}
}
func TestWaitEnabledErrors(t *testing.T) {
	f := newFake()
	c := New(f)
	defer c.Close()
	if _, err := c.WaitEnabled(context.Background(), false, 0); err == nil || len(f.writes) != 0 {
		t.Fatal("invalid interval")
	}
	failure := errors.New("removed")
	f.writeErr = failure
	if _, err := c.WaitEnabled(context.Background(), false, time.Millisecond); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if len(f.writes) != 1 {
		t.Fatal("communication error retried")
	}
}
