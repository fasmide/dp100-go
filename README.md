# dp100-go

An idiomatic, dependency-free Go driver for the Alientek DP100 programmable power
supply. Uses Linux `hidraw` directly: no vendor DLL, .NET runtime, libusb, or CGO
is needed. The protocol/client layer is portable through the small `Transport`
interface. Go 1.22 or newer is required.

## Status

Read operations were verified against the attached DP100 at `/dev/hidraw14`
(USB `2e3c:af01`, hardware V1.4, firmware V1.4, bootloader V1.1):

- Device identity and full factory serial number
- Input/output voltage, output current, temperatures, mode and protection state
- Active output settings and groups 0–9
- Global system settings

Output on/off and voltage/current setpoints are hardware-tested on V1.4 with
the load disconnected. Enabled-output tests covered 1, 3.3, 5, and 12 V; current
setpoint readback covered 0–5000 mA with output off (not loaded CC regulation).
**V1.4 ignores OVP/OCP fields in `SetOutput`, despite acknowledging the write.**
A 30 V request was asynchronously clamped to the available 19.42 V maximum.

Preset 9 storage/recall, working-group recall, and OVP/OCP updates in working
and selected preset groups are hardware-verified with output off. Stored limits
are recalled; limit fields in an apply packet are ignored. Power-cycle persistence
and actual protection trips under load remain untested. Global system writes
remain unit-tested only.
Firmware V1.5 and older firmware compatibility are not hardware verified.
Scan/sequential output and firmware updates are deliberately not exposed.

## Read a device

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/fasmide/dp100-go"
)

func main() {
    psu, err := dp100.Open("/dev/hidraw14")
    if err != nil {
        log.Fatal(err)
    }
    defer psu.Close()

    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()

    m, err := psu.Measurements(ctx)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("%.3f V, %.3f A (%s, %s)\n",
        float64(m.OutputVoltageMV)/1000,
        float64(m.OutputCurrentMA)/1000, m.Mode, m.State)
}
```

The module path is `github.com/fasmide/dp100-go`; the Go package name is `dp100`.

Add the library to your module:

```sh
go get github.com/fasmide/dp100-go
```

Install the diagnostic CLI:

```sh
go install github.com/fasmide/dp100-go/cmd/dp100@latest
```

A read-only diagnostic command is included:

```sh
go run ./cmd/dp100 -list                    # no device opened
go run ./cmd/dp100                          # auto-select exactly one device
go run ./cmd/dp100 -device /dev/hidraw14     # explicit selection
```

Opening, reading, and closing do not change output state or configuration. The
CLI has no write options. A device's existing output may already be enabled.

## Explicit writes

The API uses integer **mV** and **mA**, not floating-point volts/amps. An output
packet always includes the complete state and both protection fields, but firmware
may ignore some fields. Preserve existing OVP/OCP rather than assuming a write
will change them:

```go
// WARNING: this energizes the output. Confirm existing protection thresholds
// are suitable BEFORE enabling; SetOutput cannot change them on firmware V1.4.
s, err := psu.Settings(ctx)
if err != nil {
    return err
}
s.VoltageMV = 5000
s.CurrentMA = 500
s.Enabled = true
err = psu.SetOutput(ctx, s)
```

For everyday changes, prefer the preserving helpers:

```go
err := psu.SetEnabled(ctx, false)       // preserve all active limits
// This preserves enable state; if already enabled, it changes the live output.
err = psu.SetSetpoints(ctx, 5000, 500)  // 5 V, 500 mA; preserve protection fields
```

Each helper holds the client's transaction lock across its read/write pair, so
other goroutines using that client cannot interleave. Front-panel changes and
other applications are outside this guarantee. Disabling is not a guarantee of
instant capacitor discharge. Closing the client does not disable output.

`Discover()` enumerates paths, names, and USB serials without opening devices;
it does not require hidraw access permissions. Pass a selected path to `Open`.
The built-in backend is Linux-only; `Open`/`Discover` return `ErrUnsupported` on
other platforms. `New(Transport)` remains portable.

- `Preset(ctx, group)` reads groups 0–9. The returned `Enabled` byte is the stored
  group's state, **not** the live output indicator; use `Settings` for that.
- `StorePreset(ctx, group, limits)` writes all four limits, including OVP/OCP,
  to groups 1–9 without selecting them. **If the group is already selected, its
  active limits change too.** Persistence across power cycles is not verified.
- `RecallPreset(ctx, group)` recalls stored limits for groups 0–9, with group 0
  returning to working settings. Requires output off; it does not enable output.
- `SetProtection(ctx, ovpMV, ocpMA)` sets active-group protection thresholds while
  preserving voltage/current. Requires output off and updates that group's stored
  values too. This is the verified route for V1.4, unlike `SetOutput`.
- `ApplyPreset(ctx, group, limits)` is deprecated: supplied limits are ignored
  on V1.4. Use `RecallPreset`, which has no misleading limit argument.
- `System`/`SetSystem` read/write global settings, including reverse protection
  and automatic output at boot. `SetSystem` writes all fields. Preserve values
  you do not intend to change. OTP is an explicitly **raw** threshold because
  vendor documentation conflicts with observed values (see `PROTOCOL.md`).

A `Client` is safe for concurrent use. Commands are serialized, have a maximum
one-second transaction budget including queue time, and honor earlier context
deadlines (including the entire read/write pair for preserving helpers).
`Close` interrupts pending I/O. Each physical PSU should have exactly one client
and no competing vendor application. Operations are not retried: **a timeout or
cancellation after transmission does not mean a write was not applied**. Read back
before deciding what to do next. Successful ACKs also do not prove exact applied
values; the device may ignore fields or quantize/clamp settings. On V1.4, the
30 V setpoint was briefly readable before it was clamped to 19.42 V by the
control loop. Allow settling time before checking the applied value.

## Linux permissions

`Open` verifies the device identity before issuing any protocol requests. hidraw
numbers can change on reconnect. For non-root access, a possible udev rule is:

```udev
SUBSYSTEM=="hidraw", ATTRS{idVendor}=="2e3c", ATTRS{idProduct}=="af01", GROUP="plugdev", MODE="0660"
```

Use a group that actually exists on your system and add your user to it. The
package does not install rules or alter permissions.

## Tests

```sh
go test -race -cover ./...
go vet ./...
go test -run '^$' -fuzz FuzzDecode -fuzztime 5s .

# Explicit opt-in: sends ONLY read queries; checks active settings before/after.
DP100_HIDRAW=/dev/hidraw14 go test -v -run '^TestHardwareReadOnly$' .

# DANGER: disconnect the output load first. Enables output at up to 12 V.
# Saves a recovery snapshot in /tmp, restores original limits, leaves output OFF.
# Requires suitable pre-existing OVP/OCP and at least 12 V available headroom.
DP100_HIDRAW=/dev/hidraw14 DP100_TEST_OUTPUT=disconnected \
  go test -v -count=1 -run '^TestHardwareOutput$' .

# Modifies group 0/preset 9 and restores them. Requires output OFF and group 0.
DP100_HIDRAW=/dev/hidraw14 DP100_TEST_PRESETS=disconnected \
  go test -v -count=1 -run '^TestHardwarePresets$' .
```

Tests cover real captured reports (including nonzero trailing bytes), CRC and
length errors, field layouts, write flags, device rejection, cancellation,
concurrency, and late/unrelated replies. Normal tests do not access the PSU.

## Errors

Use `errors.Is` for `context.Canceled`, `context.DeadlineExceeded`, `ErrClosed`,
`ErrProtocol`, and `ErrOutputEnabled` (configuration requires output off).
`errors.As` extracts `*DeviceError` for rejected writes and
`*OpError` for failed exchanges. `OpError.Transmitted` means I/O was attempted;
for a settings command, the outcome may be uncertain even if the error is a
cancellation. A malformed write acknowledgement is also an uncertain result.
Validation errors and cancellation while waiting for the client lock send nothing.

## Parked firmware investigation

Offline parser/tests and investigation notes are isolated in
[`firmware/`](firmware/README.md). The original vendor update file and release
notes remain outside this project in `../1，Firmware/`. Firmware work is paused;
there is no firmware-update API in the control package.

See [CONTRIBUTING.md](CONTRIBUTING.md) for development and hardware-test rules.

See [PROTOCOL.md](PROTOCOL.md) for evidence, packet layouts, and open questions.

## License

Released into the public domain under [The Unlicense](LICENSE).
Original vendor software, firmware, and manuals are not covered by this license
and are not included in this project.
