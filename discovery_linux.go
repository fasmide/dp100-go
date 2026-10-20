//go:build linux

package dp100

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Discover lists DP100 interfaces via sysfs, sorted by device path. It requires
// no hidraw permissions, sends no requests, and returns an empty slice if none
// are present. A device can disappear between discovery and Open.
func Discover() ([]Device, error) { return discover(os.DirFS("/sys/class/hidraw"), "/dev") }

func discover(sysfs fs.FS, devRoot string) ([]Device, error) {
	entries, err := fs.ReadDir(sysfs, ".")
	if errors.Is(err, fs.ErrNotExist) {
		return []Device{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dp100: enumerate HID devices: %w", err)
	}
	devices := make([]Device, 0)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "hidraw") {
			continue
		}
		data, err := fs.ReadFile(sysfs, entry.Name()+"/device/uevent")
		if errors.Is(err, fs.ErrNotExist) {
			continue
		} // Unplugged during enumeration.
		if err != nil {
			return nil, fmt.Errorf("dp100: inspect %s: %w", entry.Name(), err)
		}
		fields := make(map[string]string)
		for _, line := range strings.Split(string(data), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if ok {
				fields[key] = value
			}
		}
		ids := strings.Split(fields["HID_ID"], ":")
		if len(ids) != 3 {
			continue
		}
		bus, e1 := strconv.ParseUint(ids[0], 16, 32)
		vendor, e2 := strconv.ParseUint(ids[1], 16, 32)
		product, e3 := strconv.ParseUint(ids[2], 16, 32)
		if e1 != nil || e2 != nil || e3 != nil || bus != 3 || vendor != VendorID || product != ProductID {
			continue
		}
		devices = append(devices, Device{Path: filepath.Join(devRoot, entry.Name()), Name: fields["HID_NAME"], Serial: fields["HID_UNIQ"]})
	}
	return devices, nil
}
