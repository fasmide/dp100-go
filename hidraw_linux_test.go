//go:build linux

package dp100

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Opt-in only. This test sends read requests, never settings or output commands.
func TestHardwareReadOnly(t *testing.T) {
	path := os.Getenv("DP100_HIDRAW")
	if path == "" {
		t.Skip("set DP100_HIDRAW to opt in to read-only hardware tests")
	}
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	info, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s hardware=%s firmware=%s bootloader=%s", info.Model, info.Hardware, info.Firmware, info.Bootloader)
	before, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	system, err := c.System(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("system: %+v", system)
	for group := byte(0); group <= 9; group++ {
		s, err := c.Preset(ctx, group)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("preset %d: %+v", group, s)
	}
	for i := 0; i < 3; i++ {
		m, err := c.Measurements(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("measurements: %+v", m)
	}
	after, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("settings changed: before=%+v after=%+v", before, after)
	}
}

// TestHardwareOutput requires a disconnected load and a separate explicit opt-in.
// It restores the original limits but deliberately leaves the output OFF.
func TestHardwareOutput(t *testing.T) {
	path := os.Getenv("DP100_HIDRAW")
	if path == "" || os.Getenv("DP100_TEST_OUTPUT") != "disconnected" {
		t.Skip("requires DP100_HIDRAW and DP100_TEST_OUTPUT=disconnected; disconnect the load first")
	}
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	original, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if original.Group != 0 {
		t.Fatal("select working group 0 before running this test; stored groups must not be modified")
	}
	if original.OverVoltageMV < 12500 || original.OverCurrentMA < 100 {
		t.Fatal("existing protection thresholds unsuitable for the 12 V / 100 mA test; not changing them automatically")
	}
	system, err := c.System(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var presets [10]OutputSettings
	for i := range presets {
		presets[i], err = c.Preset(ctx, byte(i))
		if err != nil {
			t.Fatal(err)
		}
	}
	// Persist a recovery record before the first write, even if the process dies.
	snapshot := struct {
		Output  OutputSettings
		System  SystemSettings
		Presets [10]OutputSettings
	}{original, system, presets}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.CreateTemp("", "dp100-before-output-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = backup.Write(data); err != nil {
		backup.Close()
		t.Fatal(err)
	}
	if err = backup.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("recovery snapshot: %s", filepath.Clean(backup.Name()))
	t.Logf("original: %+v", original)
	defer func() {
		restore := original
		restore.Enabled = false
		if err := c.SetOutput(context.Background(), restore); err != nil {
			t.Errorf("RESTORATION FAILED: %v; manually disable output and restore from %s", err, backup.Name())
			return
		}
		got, err := c.Settings(context.Background())
		if err != nil {
			t.Error(err)
			return
		}
		if got != restore {
			t.Errorf("restore readback: got %+v, want %+v", got, restore)
		}
		t.Logf("final settings: %+v", got)
	}()

	initialMeasurements, err := c.Measurements(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initialMeasurements.MaximumOutputVoltageMV < 12500 {
		t.Fatal("input supply has insufficient headroom for the 12 V test")
	}
	apply := func(want OutputSettings) {
		t.Helper()
		if err := c.SetOutput(ctx, want); err != nil {
			t.Fatalf("SetOutput(%+v): %v", want, err)
		}
		// ACK can precede the control loop's clamp of the voltage setpoint.
		time.Sleep(100 * time.Millisecond)
		got, err := c.Settings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("requested: %+v; readback: %+v", want, got)
		expected := want
		if expected.VoltageMV > initialMeasurements.MaximumOutputVoltageMV {
			expected.VoltageMV = initialMeasurements.MaximumOutputVoltageMV
			t.Logf("device clamps requested voltage to available maximum %d mV", expected.VoltageMV)
		}
		if got != expected {
			t.Fatalf("settings differ: got %+v, want %+v", got, expected)
		}
	}
	off := original
	off.Enabled = false
	apply(off)
	// Full nominal setpoint range is tested with output disabled. The input
	// supply may not have enough voltage headroom to produce 30 V under power.
	for _, limits := range []Limits{
		{VoltageMV: 0, CurrentMA: 0, OverVoltageMV: 30500, OverCurrentMA: 5050},
		{VoltageMV: 10, CurrentMA: 1, OverVoltageMV: 30500, OverCurrentMA: 5050},
		{VoltageMV: 5000, CurrentMA: 500, OverVoltageMV: 30500, OverCurrentMA: 5050},
		{VoltageMV: 12000, CurrentMA: 1000, OverVoltageMV: 30500, OverCurrentMA: 5050},
		{VoltageMV: 30000, CurrentMA: 5000, OverVoltageMV: 30500, OverCurrentMA: 5050},
	} {
		limits.OverVoltageMV = original.OverVoltageMV
		limits.OverCurrentMA = original.OverCurrentMA
		apply(OutputSettings{Group: original.Group, Limits: limits})
	}
	for _, voltage := range []uint16{1000, 3300, 5000, 12000} {
		s := OutputSettings{Group: original.Group, Limits: Limits{
			VoltageMV: voltage, CurrentMA: 100, OverVoltageMV: original.OverVoltageMV, OverCurrentMA: original.OverCurrentMA,
		}}
		apply(s)
		s.Enabled = true
		apply(s)
		time.Sleep(400 * time.Millisecond)
		m, err := c.Measurements(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("enabled telemetry: %+v", m)
		if m.State != Normal || m.Mode != ConstantVoltage {
			t.Fatalf("unexpected enabled mode/state: %+v", m)
		}
		delta := int(m.OutputVoltageMV) - int(voltage)
		if delta < -100 || delta > 100 {
			t.Fatalf("output voltage %d mV, want %d +/-100 mV", m.OutputVoltageMV, voltage)
		}
		if m.OutputCurrentMA > 20 {
			t.Fatalf("unexpected current with disconnected load: %d mA", m.OutputCurrentMA)
		}
		s.Enabled = false
		apply(s)
		time.Sleep(400 * time.Millisecond)
		m, err = c.Measurements(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("disabled telemetry: %+v", m)
		if m.Mode != OutputOff {
			t.Fatalf("unexpected disabled mode: %s", m.Mode)
		}
	}
	afterSystem, err := c.System(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if afterSystem != system {
		t.Errorf("system settings changed: %+v -> %+v", system, afterSystem)
	}
	// Groups 1..9 should not be changed by working-group output writes.
	if original.Group == 0 {
		for group := byte(1); group <= 9; group++ {
			got, err := c.Preset(ctx, group)
			if err != nil {
				t.Fatal(err)
			}
			if got != presets[group] {
				t.Errorf("preset %d changed: %+v -> %+v", group, presets[group], got)
			}
		}
	}
}

func TestOpenRejectsNonHID(t *testing.T) {
	if _, err := Open("/dev/null"); err == nil {
		t.Fatal("non-HID device accepted")
	}
	if _, err := Open("/nonexistent/dp100"); err == nil {
		t.Fatal("missing device accepted")
	}
}

func TestHidrawReports(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	writer := &hidraw{w}
	reader := &hidraw{r}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want := make([]byte, 64)
	want[0] = 0xfa
	if err := writer.WriteReport(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := reader.ReadReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("report=%x", got)
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if err := writer.WriteReport(cancelled, want); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestHidrawBlockedWriteCancellation(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	// Fill the pipe using its nonblocking descriptor; never block the test runner.
	raw, err := w.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var fillErr error
	if err := raw.Control(func(fd uintptr) {
		for {
			_, fillErr = syscall.Write(int(fd), make([]byte, 4096))
			if fillErr != nil {
				break
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(fillErr, syscall.EAGAIN) {
		t.Fatal(fillErr)
	}
	h := &hidraw{w}
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(5*time.Millisecond, cancel)
	defer timer.Stop()
	defer cancel()
	if err := h.WriteReport(ctx, make([]byte, 65)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestHidrawCancellation(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	h := &hidraw{r}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(5*time.Millisecond, cancel)
	start := time.Now()
	if _, err := h.ReadReport(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation took too long")
	}
}
