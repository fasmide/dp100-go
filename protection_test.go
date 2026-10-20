package dp100

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func configurationFake(s OutputSettings) *fakeTransport {
	f := newFake()
	f.respond = func(b []byte) [][]byte {
		if b[4] == 1 {
			return [][]byte{reply(b[2], b[3], settingsPayload(s.Group, s.Enabled, s.Limits))}
		}
		return [][]byte{reply(b[2], b[3], []byte{1})}
	}
	return f
}
func TestProtectionAndRecallPackets(t *testing.T) {
	for _, group := range []byte{0, 9} {
		f := configurationFake(OutputSettings{Group: group, Limits: Limits{5000, 100, 30500, 5050}})
		c := New(f)
		if err := c.SetProtection(context.Background(), 5500, 200); err != nil {
			t.Fatal(err)
		}
		b := f.writes[1]
		if b[5] != group|0x60 || b[6] != 0 || uint16At(b, 7) != 5000 || uint16At(b, 9) != 100 || uint16At(b, 11) != 5500 || uint16At(b, 13) != 200 {
			t.Fatalf("protection write: %x", b)
		}
		if err := c.RecallPreset(context.Background(), group); err != nil {
			t.Fatal(err)
		}
		b = f.writes[3]
		if b[5] != group|0xa0 || b[6] != 0 {
			t.Fatalf("recall: %x", b)
		}
		for _, v := range b[7:15] {
			if v != 0 {
				t.Fatal("recall has limit payload")
			}
		}
		c.Close()
	}
}
func TestConfigurationRequiresOff(t *testing.T) {
	f := configurationFake(OutputSettings{Group: 0, Enabled: true, Limits: Limits{5000, 100, 30500, 5050}})
	c := New(f)
	defer c.Close()
	for _, err := range []error{c.SetProtection(context.Background(), 5500, 200), c.RecallPreset(context.Background(), 9)} {
		if !errors.Is(err, ErrOutputEnabled) {
			t.Fatal(err)
		}
	}
	for _, b := range f.writes {
		if b[4] != 1 {
			t.Fatal("configuration sent with output enabled")
		}
	}
}
func TestConfigurationValidation(t *testing.T) {
	f := newFake()
	c := New(f)
	defer c.Close()
	for _, err := range []error{c.SetProtection(context.Background(), 30501, 100), c.SetProtection(context.Background(), 5000, 5051), c.RecallPreset(context.Background(), 10)} {
		if err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	if len(f.writes) != 0 {
		t.Fatal("validation caused I/O")
	}
}
func TestConfigurationReadFailure(t *testing.T) {
	for _, payload := range [][]byte{nil, settingsPayload(15, false, Limits{})} {
		f := newFake()
		f.respond = func(b []byte) [][]byte { return [][]byte{reply(b[2], b[3], payload)} }
		c := New(f)
		if err := c.SetProtection(context.Background(), 5500, 200); !errors.Is(err, ErrProtocol) {
			t.Fatal(err)
		}
		if len(f.writes) != 1 {
			t.Fatal("malformed read caused write")
		}
		c.Close()
	}
}
func TestConfigurationPairsDoNotInterleave(t *testing.T) {
	f := configurationFake(OutputSettings{Limits: Limits{5000, 100, 30500, 5050}})
	c := New(f)
	defer c.Close()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.SetProtection(context.Background(), 5500, 200); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for i, b := range f.writes {
		if (i%2 == 0) != (b[4] == 1) {
			t.Fatalf("interleaving at %d", i)
		}
	}
}
