package dp100

import (
	"encoding/hex"
	"testing"
)

func unhex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Captured on /dev/hidraw14, firmware V1.4. The bytes after the CRC contain
// stale data from earlier replies: they must NOT participate in CRC validation.
const infoCapture = "fa10072841544b2d445031303000ffffffffffff0e000e000b00aa001f2c9c0000c026220a272005e8070403c99d000000000000000000000000000000000000"
const measurementsCapture = "fa300710164ea24a3301dc4b72017801d6130100b1560e000b00aa001f2c9c0000c026220a272005e8070403c99d000000000000000000000000000000000000"
const settingsCapture = "fa35070a0001a64a670a2477ba13bc4ad6130100b1560e000b00aa001f2c9c0000c026220a272005e8070403c99d000000000000000000000000000000000000"
const systemCapture = "fa40070850001a04020101001beebc4ad6130100b1560e000b00aa001f2c9c0000c026220a272005e8070403c99d000000000000000000000000000000000000"

func TestCRC(t *testing.T) {
	if got := crc16([]byte("123456789")); got != 0x4b37 {
		t.Fatalf("CRC = %04x", got)
	}
}
func TestEncode(t *testing.T) {
	b, err := encode(frame{commandInfo, 7, nil})
	if err != nil {
		t.Fatal(err)
	}
	want := append(unhex(t, "00fb10070032f5"), make([]byte, 58)...)
	if string(b) != string(want) {
		t.Fatalf("report = %x, want %x", b, want)
	}
	if _, err := encode(frame{data: make([]byte, maxPayload+1)}); err == nil {
		t.Fatal("oversize payload accepted")
	}
}
func TestDecodeCaptures(t *testing.T) {
	for _, s := range []string{infoCapture, measurementsCapture, settingsCapture, systemCapture} {
		if _, err := decode(unhex(t, s)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestDecodeInvalid(t *testing.T) {
	base := unhex(t, infoCapture)
	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"short", func(b []byte) []byte { return b[:63] }},
		{"long", func(b []byte) []byte { return append(b, 0) }},
		{"address", func(b []byte) []byte { b[0] = 0xfb; return b }},
		{"length", func(b []byte) []byte { b[3] = 59; return b }},
		{"CRC", func(b []byte) []byte { b[4] ^= 1; return b }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decode(tc.mutate(append([]byte(nil), base...))); err == nil {
				t.Fatal("invalid report accepted")
			}
		})
	}
}
func FuzzDecode(f *testing.F) {
	f.Add(unhex(f, infoCapture))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = decode(b) })
}
