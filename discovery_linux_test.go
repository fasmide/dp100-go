//go:build linux

package dp100

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestDiscover(t *testing.T) {
	fixture := fstest.MapFS{
		"hidraw14/device/uevent": {Data: []byte("HID_ID=0003:00002E3C:0000AF01\nHID_NAME=ALIENTEK ATK-MDP100\nHID_UNIQ=ABC123\n")},
		"hidraw2/device/uevent":  {Data: []byte("HID_ID=0003:00001234:0000AF01\n")},
		"hidraw3/device/uevent":  {Data: []byte("HID_ID=0005:00002E3C:0000AF01\n")},
		"hidraw4/device/uevent":  {Data: []byte("HID_ID=oops\n")},
		"hidraw5/device/uevent":  {Data: []byte("HID_ID=0003:xxxx:0000AF01\n")},
		"hidraw6/device/uevent":  {Data: []byte("HID_ID=0003:00002e3c:0000af01\n")},
		"hidraw7":                {Mode: fs.ModeDir}, // Disappeared during enumeration.
		"unrelated":              {Data: []byte("ignored")},
	}
	devices, err := discover(fixture, "/dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("devices: %+v", devices)
	}
	if devices[0] != (Device{Path: "/dev/hidraw14", Name: "ALIENTEK ATK-MDP100", Serial: "ABC123"}) || devices[1].Path != "/dev/hidraw6" {
		t.Fatalf("devices: %+v", devices)
	}
	empty, err := discover(fstest.MapFS{}, "/dev")
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	_, err = discover(failingFS{fs.ErrPermission}, "/dev")
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatal(err)
	}
}

type failingFS struct{ err error }

func (f failingFS) Open(string) (fs.File, error) { return nil, f.err }
