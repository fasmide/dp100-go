package dp100

import "fmt"

// Version is the vendor's version number in tenths (14 means V1.4).
type Version uint16

func (v Version) String() string { return fmt.Sprintf("V%d.%d", v/10, v%10) }

// DeviceInfo describes the controller and its factory identity.
type DeviceInfo struct {
	Model                          string
	Hardware, Firmware, Bootloader Version
	// RunArea is 0x00aa for the application; other values indicate the bootloader.
	RunArea uint16
	// Serial is the complete factory serial, not the DLL's truncated display value.
	Serial     [12]byte
	Year       uint16
	Month, Day byte
}

// OutputMode is the regulation mode. Bit 7 of the wire value indicates undervoltage.
type OutputMode byte

const (
	ConstantCurrent OutputMode = iota
	ConstantVoltage
	OutputOff
	UnderVoltage
)

func (m OutputMode) String() string {
	switch m {
	case ConstantCurrent:
		return "CC"
	case ConstantVoltage:
		return "CV"
	case OutputOff:
		return "off"
	case UnderVoltage:
		return "undervoltage"
	}
	return fmt.Sprintf("OutputMode(%d)", m)
}

// WorkState identifies the currently active protection, if any.
type WorkState byte

const (
	Normal WorkState = iota
	OverVoltage
	OverCurrent
	OverTemperature
	OverPower
	ReversePolarity
	InputUnderVoltage
)

func (s WorkState) String() string {
	switch s {
	case Normal:
		return "normal"
	case OverVoltage:
		return "OVP"
	case OverCurrent:
		return "OCP"
	case OverTemperature:
		return "OTP"
	case OverPower:
		return "OPP"
	case ReversePolarity:
		return "reverse polarity"
	case InputUnderVoltage:
		return "UVP"
	}
	return fmt.Sprintf("WorkState(%d)", s)
}

// Measurements is a snapshot of the PSU telemetry, not the programmed settings.
// Temperature scaling is inferred from live readings, not specified by the DLL.
type Measurements struct {
	InputVoltageMV, OutputVoltageMV, OutputCurrentMA uint16
	MaximumOutputVoltageMV                           uint16
	Temperature1DeciC, Temperature2DeciC             uint16
	InternalSupplyMV                                 uint16
	Mode                                             OutputMode
	State                                            WorkState
}

// Limits uses mV and mA, exactly as the wire protocol does. All fields, including
// zeros, are transmitted literally. Which fields are applied depends on the
// command/firmware: SetOutput ignores OVP/OCP on V1.4. Always verify readback.
// The device may quantize voltage or clamp it to the available input headroom.
type Limits struct {
	VoltageMV, CurrentMA         uint16
	OverVoltageMV, OverCurrentMA uint16
}

func (l Limits) validate() error {
	if l.VoltageMV > 30000 || l.CurrentMA > 5000 || l.OverVoltageMV > 30500 || l.OverCurrentMA > 5050 {
		return fmt.Errorf("dp100: limits exceed DP100 ranges (30000 mV, 5000 mA, OVP 30500 mV, OCP 5050 mA)")
	}
	return nil
}

// OutputSettings is the selected group, output enable state, and all four limits.
// Group 0 is the working group; stored presets are numbered 1 through 9.
type OutputSettings struct {
	Group   byte
	Enabled bool
	Limits
}

// SystemSettings contains global configuration. ReverseProtection and
// AutoOutput require firmware V1.3 or later. AutoOutput enables output at boot.
type SystemSettings struct {
	// OverTemperatureRaw is deliberately unscaled: the DLL manual says 0.1 C,
	// but the attached V1.4 device reports 80, consistent with whole degrees.
	// Preserve the read value unless the units for your firmware are verified.
	OverTemperatureRaw            uint16
	OverPowerDeciW                uint16
	Backlight, Volume             byte // 0 through 4
	ReverseProtection, AutoOutput bool
}
