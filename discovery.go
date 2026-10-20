package dp100

const (
	// VendorID and ProductID identify the DP100 USB HID interface.
	VendorID  = 0x2e3c
	ProductID = 0xaf01
)

// Device describes a USB HID interface found without opening it or sending
// protocol requests. Enumeration does not establish access permissions.
type Device struct {
	Path   string
	Name   string
	Serial string // USB serial, not the factory serial returned by Client.Info.
}
