//go:build linux

package dp100

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Explicit opt-in: modifies preset 9, applies it with output off, then restores
// the original preset and working settings. Never run alongside other clients.
func TestHardwarePresets(t *testing.T) {
	if os.Getenv("DP100_TEST_PRESETS") != "disconnected" || os.Getenv("DP100_HIDRAW") == "" {
		t.Skip("requires DP100_HIDRAW and DP100_TEST_PRESETS=disconnected")
	}
	c, err := Open(os.Getenv("DP100_HIDRAW"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	original, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if original.Enabled || original.Group != 0 {
		t.Fatal("requires output OFF and working group 0")
	}
	system, err := c.System(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var groups [10]OutputSettings
	for i := range groups {
		groups[i], err = c.Preset(ctx, byte(i))
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot := struct {
		Output OutputSettings
		System SystemSettings
		Groups [10]OutputSettings
	}{original, system, groups}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.CreateTemp("", "dp100-before-presets-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Write(data); err != nil {
		backup.Close()
		t.Fatal(err)
	}
	if err := backup.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("recovery snapshot: %s", backup.Name())
	defer func() {
		if err := c.SetEnabled(ctx, false); err != nil {
			t.Errorf("disable before restoration: %v", err)
			return
		}
		if err := c.StorePreset(ctx, 9, groups[9].Limits); err != nil {
			t.Errorf("restore preset: %v", err)
		}
		if err := c.writeSettings(ctx, 0x60, false, groups[0].Limits); err != nil {
			t.Errorf("restore working group storage: %v", err)
		}
		if err := c.RecallPreset(ctx, 0); err != nil {
			t.Errorf("restore working group selection: %v", err)
		}
		if err := c.SetOutput(ctx, original); err != nil {
			t.Errorf("restore output: %v", err)
		}
		time.Sleep(150 * time.Millisecond)
		got, err := c.Settings(ctx)
		if err != nil {
			t.Error(err)
		} else if got != original {
			t.Errorf("RESTORE MISMATCH: %+v want %+v; snapshot %s", got, original, backup.Name())
		}
		for i := range groups {
			got, err := c.Preset(ctx, byte(i))
			if err != nil {
				t.Error(err)
			} else if got != groups[i] {
				t.Errorf("group %d restore mismatch: %+v want %+v", i, got, groups[i])
			}
		}
		gotSystem, err := c.System(ctx)
		if err != nil {
			t.Error(err)
		} else if gotSystem != system {
			t.Errorf("system changed: %+v", gotSystem)
		}
		t.Logf("final: %+v", got)
	}()
	logState := func(label string) {
		t.Helper()
		time.Sleep(150 * time.Millisecond)
		active, err := c.Settings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := c.Preset(ctx, 9)
		if err != nil {
			t.Fatal(err)
		}
		zero, err := c.Preset(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: active=%+v preset9=%+v group0=%+v", label, active, stored, zero)
		if active.Enabled {
			t.Fatal("output unexpectedly enabled")
		}
	}
	limits := Limits{VoltageMV: 5000, CurrentMA: 100, OverVoltageMV: 5500, OverCurrentMA: 200}
	if err := c.StorePreset(ctx, 9, limits); err != nil {
		t.Fatal(err)
	}
	logState("store9")
	stored9, err := c.Preset(ctx, 9)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stored9.Limits != limits || unchanged != original {
		t.Fatalf("store changed wrong state: stored=%+v active=%+v", stored9, unchanged)
	}
	// Deliberately distinct fields establish recall vs supplied-field semantics.
	supplied := Limits{VoltageMV: 3300, CurrentMA: 150, OverVoltageMV: 6000, OverCurrentMA: 300}
	if err := c.ApplyPreset(ctx, 9, supplied); err != nil {
		t.Fatal(err)
	}
	logState("apply9 distinct payload")
	recalled, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if recalled.Group != 9 || recalled.Limits != limits {
		t.Fatalf("apply did not recall stored limits: %+v", recalled)
	}
	if err := c.writeSettings(ctx, 0xa0, false, original.Limits); err != nil {
		t.Fatal(err)
	}
	logState("recall0")
	if err := c.writeSettings(ctx, 0x60, false, limits); err != nil {
		t.Fatal(err)
	}
	logState("store0")
	if err := c.writeSettings(ctx, 0xa0, false, supplied); err != nil {
		t.Fatal(err)
	}
	logState("recall0 after store0")
	if err := c.SetProtection(ctx, 6500, 350); err != nil {
		t.Fatal(err)
	}
	logState("SetProtection working group")
	active, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if active.Limits != (Limits{5000, 100, 6500, 350}) {
		t.Fatalf("SetProtection mismatch: %+v", active)
	}
	if err := c.RecallPreset(ctx, 9); err != nil {
		t.Fatal(err)
	}
	logState("RecallPreset9 zero fields")
	active, err = c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if active.Group != 9 || active.Limits != limits {
		t.Fatalf("RecallPreset mismatch: %+v", active)
	}
	if err := c.SetProtection(ctx, 7000, 400); err != nil {
		t.Fatal(err)
	}
	logState("SetProtection selected preset")
	stored, err := c.Preset(ctx, 9)
	if err != nil {
		t.Fatal(err)
	}
	active, err = c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expected := Limits{5000, 100, 7000, 400}
	if stored.Limits != expected || active.Limits != expected {
		t.Fatalf("selected protection mismatch: stored=%+v active=%+v", stored, active)
	}
	if err := c.RecallPreset(ctx, 0); err != nil {
		t.Fatal(err)
	}
	logState("RecallPreset0 zero fields")
}
