package dp100

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
)

// Info reads device identity without changing configuration.
func (c *Client) Info(ctx context.Context) (DeviceInfo, error) {
	b, err := c.exchange(ctx, commandInfo, nil)
	if err != nil {
		return DeviceInfo{}, err
	}
	return parseInfo(b)
}

func parseInfo(b []byte) (DeviceInfo, error) {
	if err := requireLength(b, 40); err != nil {
		return DeviceInfo{}, err
	}
	info := DeviceInfo{
		Model:    strings.TrimRight(string(b[:16]), "\x00\xff "),
		Hardware: Version(uint16At(b, 16)), Firmware: Version(uint16At(b, 18)), Bootloader: Version(uint16At(b, 20)),
		RunArea: uint16At(b, 22), Year: uint16At(b, 36), Month: b[38], Day: b[39],
	}
	copy(info.Serial[:], b[24:36])
	return info, nil
}

// Measurements requests one telemetry snapshot. No background polling is started.
func (c *Client) Measurements(ctx context.Context) (Measurements, error) {
	b, err := c.exchange(ctx, commandMeasurements, nil)
	if err != nil {
		return Measurements{}, err
	}
	if err := requireLength(b, 16); err != nil {
		return Measurements{}, err
	}
	mode := OutputMode(b[14])
	if b[14]&0x80 != 0 {
		mode = UnderVoltage
	}
	return Measurements{
		InputVoltageMV: uint16At(b, 0), OutputVoltageMV: uint16At(b, 2), OutputCurrentMA: uint16At(b, 4),
		MaximumOutputVoltageMV: uint16At(b, 6), Temperature1DeciC: uint16At(b, 8), Temperature2DeciC: uint16At(b, 10),
		InternalSupplyMV: uint16At(b, 12), Mode: mode, State: WorkState(b[15]),
	}, nil
}

// Settings reads the currently active output settings, independent of group.
func (c *Client) Settings(ctx context.Context) (OutputSettings, error) {
	return c.readSettings(ctx, 0x80)
}

// Preset reads group 0 through 9. Group 0 is the working group.
func (c *Client) Preset(ctx context.Context, group byte) (OutputSettings, error) {
	if group > 9 {
		return OutputSettings{}, fmt.Errorf("dp100: preset group must be 0 through 9")
	}
	return c.readSettings(ctx, group)
}

func (c *Client) readSettings(ctx context.Context, index byte) (OutputSettings, error) {
	b, err := c.exchange(ctx, commandSettings, []byte{index})
	if err != nil {
		return OutputSettings{}, err
	}
	return parseSettings(b)
}

func parseSettings(b []byte) (OutputSettings, error) {
	if err := requireLength(b, 10); err != nil {
		return OutputSettings{}, err
	}
	return OutputSettings{Group: b[0] & 0x0f, Enabled: b[1] != 0, Limits: Limits{
		VoltageMV: uint16At(b, 2), CurrentMA: uint16At(b, 4), OverVoltageMV: uint16At(b, 6), OverCurrentMA: uint16At(b, 8),
	}}, nil
}

// SetOutput sends the enable state and all four limit fields to a group (0
// through 9). It can energize the output. On verified firmware V1.4, this command
// applies voltage/current but IGNORES OVP/OCP; do not rely on it to set protection
// thresholds. Preserve the values from Settings and verify readback. Voltage
// may be clamped asynchronously to Measurements.MaximumOutputVoltageMV after ACK.
// Read/modify/write is not atomic against other users.
func (c *Client) SetOutput(ctx context.Context, settings OutputSettings) error {
	if settings.Group > 9 {
		return fmt.Errorf("dp100: output group must be 0 through 9")
	}
	return c.writeSettings(ctx, settings.Group|0x20, settings.Enabled, settings.Limits)
}

// StorePreset writes all four limits to a stored group (1 through 9) without
// selecting it. On V1.4 this includes OVP/OCP. If the group is already selected,
// its active limits also change; this is not a staging-only operation.
// Persistence across power cycles has not been tested.
func (c *Client) StorePreset(ctx context.Context, group byte, limits Limits) error {
	if group < 1 || group > 9 {
		return fmt.Errorf("dp100: stored preset group must be 1 through 9")
	}
	return c.writeSettings(ctx, group|0x60, false, limits)
}

// ApplyPreset sends the vendor UseGroup packet. On V1.4, the supplied limits
// are IGNORED: the device recalls stored values instead. This method is kept for
// callers using the original API; new callers should use RecallPreset.
// Deprecated: use RecallPreset. Output behavior when already enabled is unverified.
func (c *Client) ApplyPreset(ctx context.Context, group byte, limits Limits) error {
	if group < 1 || group > 9 {
		return fmt.Errorf("dp100: stored preset group must be 1 through 9")
	}
	return c.writeSettings(ctx, group|0xa0, false, limits)
}

// RecallPreset selects group 0 through 9 using its stored limits. It requires
// output OFF and does not enable output. Group 0 returns to the working group.
// The check and command are serialized against this Client, not the front panel
// or other applications. The command's limit fields are zero and ignored on V1.4.
func (c *Client) RecallPreset(ctx context.Context, group byte) error {
	if group > 9 {
		return fmt.Errorf("dp100: preset group must be 0 through 9")
	}
	return c.configureOff(ctx, func(s OutputSettings) (byte, Limits) { return group | 0xa0, Limits{} })
}

// SetProtection changes the active group's OVP/OCP thresholds, preserving its
// voltage/current setpoints. Output must be OFF. This uses the group-storage
// operation, unlike SetOutput, and modifies that group's stored limits as well.
// Verified on V1.4; protection trip behavior and power-cycle persistence are not
// established. Front-panel changes cannot be excluded by the Client lock.
func (c *Client) SetProtection(ctx context.Context, overVoltageMV, overCurrentMA uint16) error {
	if err := (Limits{OverVoltageMV: overVoltageMV, OverCurrentMA: overCurrentMA}).validate(); err != nil {
		return err
	}
	return c.configureOff(ctx, func(s OutputSettings) (byte, Limits) {
		s.OverVoltageMV = overVoltageMV
		s.OverCurrentMA = overCurrentMA
		return s.Group | 0x60, s.Limits
	})
}

func (c *Client) configureOff(ctx context.Context, configure func(OutputSettings) (byte, Limits)) error {
	return c.transaction(ctx, func(ctx context.Context) error {
		b, err := c.exchangeLocked(ctx, commandSettings, []byte{0x80})
		if err != nil {
			return err
		}
		s, err := parseSettings(b)
		if err != nil {
			return err
		}
		if s.Group > 9 {
			return fmt.Errorf("%w: output group %d", ErrProtocol, s.Group)
		}
		if s.Enabled {
			return ErrOutputEnabled
		}
		index, limits := configure(s)
		if err := limits.validate(); err != nil {
			return err
		}
		b, err = c.exchangeLocked(ctx, commandSettings, settingsPayload(index, false, limits))
		if err != nil {
			return err
		}
		return checkACK(commandSettings, b)
	})
}

func (c *Client) writeSettings(ctx context.Context, index byte, enabled bool, l Limits) error {
	if err := l.validate(); err != nil {
		return err
	}
	return c.write(ctx, commandSettings, settingsPayload(index, enabled, l))
}

func settingsPayload(index byte, enabled bool, l Limits) []byte {
	b := make([]byte, 10)
	b[0], b[1] = index, boolByte(enabled)
	for i, v := range []uint16{l.VoltageMV, l.CurrentMA, l.OverVoltageMV, l.OverCurrentMA} {
		binary.LittleEndian.PutUint16(b[2+2*i:], v)
	}
	return b
}

// SetEnabled reads the active settings and changes only the output enable state.
// Enabling can energize the connected load at its existing setpoints. Disabling
// does not guarantee immediate discharge of the output capacitors.
// Other operations on this Client cannot interleave with the read/write pair;
// front-panel changes and other applications are outside this guarantee.
func (c *Client) SetEnabled(ctx context.Context, enabled bool) error {
	return c.updateOutput(ctx, func(s *OutputSettings) { s.Enabled = enabled })
}

// SetSetpoints changes voltage/current while preserving the current enable
// state and protection fields. If output is enabled, the change takes effect
// live. Voltage may be clamped to available input headroom after ACK.
// The read/write pair is serialized against other operations on this Client.
func (c *Client) SetSetpoints(ctx context.Context, voltageMV, currentMA uint16) error {
	if err := (Limits{VoltageMV: voltageMV, CurrentMA: currentMA}).validate(); err != nil {
		return err
	}
	return c.updateOutput(ctx, func(s *OutputSettings) { s.VoltageMV = voltageMV; s.CurrentMA = currentMA })
}

func (c *Client) updateOutput(ctx context.Context, update func(*OutputSettings)) error {
	return c.transaction(ctx, func(ctx context.Context) error {
		b, err := c.exchangeLocked(ctx, commandSettings, []byte{0x80})
		if err != nil {
			return err
		}
		s, err := parseSettings(b)
		if err != nil {
			return err
		}
		if s.Group > 9 {
			return fmt.Errorf("%w: output group %d", ErrProtocol, s.Group)
		}
		update(&s)
		if err := s.Limits.validate(); err != nil {
			return err
		}
		b, err = c.exchangeLocked(ctx, commandSettings, settingsPayload(s.Group|0x20, s.Enabled, s.Limits))
		if err != nil {
			return err
		}
		return checkACK(commandSettings, b)
	})
}

// System reads global settings. V1.2 and older may return only six bytes.
func (c *Client) System(ctx context.Context) (SystemSettings, error) {
	b, err := c.exchange(ctx, commandSystem, nil)
	if err != nil {
		return SystemSettings{}, err
	}
	if len(b) != 6 && len(b) != 8 {
		return SystemSettings{}, fmt.Errorf("%w: system payload size %d, want 6 or 8", ErrProtocol, len(b))
	}
	s := SystemSettings{OverTemperatureRaw: uint16At(b, 0), OverPowerDeciW: uint16At(b, 2), Backlight: b[4], Volume: b[5]}
	if len(b) == 8 {
		s.ReverseProtection = b[6] != 0
		s.AutoOutput = b[7] != 0
	}
	return s, nil
}

// SetSystem writes all global settings, including protection and automatic output
// on boot. It checks firmware to select the appropriate payload length. The OTP
// value is raw due to conflicting vendor documentation; see SystemSettings.
func (c *Client) SetSystem(ctx context.Context, s SystemSettings) error {
	if s.Backlight > 4 || s.Volume > 4 || s.OverPowerDeciW > 1050 {
		return fmt.Errorf("dp100: invalid system settings (levels 0..4, OPP 0..1050 deciwatts)")
	}
	return c.transaction(ctx, func(ctx context.Context) error {
		response, err := c.exchangeLocked(ctx, commandInfo, nil)
		if err != nil {
			return err
		}
		info, err := parseInfo(response)
		if err != nil {
			return err
		}
		b := make([]byte, 8)
		binary.LittleEndian.PutUint16(b, s.OverTemperatureRaw)
		binary.LittleEndian.PutUint16(b[2:], s.OverPowerDeciW)
		b[4], b[5], b[6], b[7] = s.Backlight, s.Volume, boolByte(s.ReverseProtection), boolByte(s.AutoOutput)
		if info.Firmware < 13 {
			if s.ReverseProtection || s.AutoOutput {
				return fmt.Errorf("dp100: reverse protection and auto output require firmware V1.3 or later")
			}
			b = b[:6]
		}
		response, err = c.exchangeLocked(ctx, commandSystem, b)
		if err != nil {
			return err
		}
		return checkACK(commandSystem, response)
	})
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}
