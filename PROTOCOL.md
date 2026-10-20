# DP100 HID protocol notes

## Sources and method

The supplied Windows DLL is an obfuscated managed .NET assembly, not a native
library. The supplied .NET 8 Linux DLL contains readable implementations of
`FrameEntity`, `ManageProtocal`, `ManageHid`, `GetOrSetParameter`,
`ATKDP100API`, and packed field structs. Decompiled with ILSpy 9.1.0.7988.
No vendor binaries are modified or executed by this Go package.

The original vendor software/manuals remain in the parent directory, outside
this Go project. Source paths below refer to that original vendor bundle.

Source SHA-256 values:

- `2，PC Software/net8dll/linux/ATKDP100DLL.dll`:
  `7b642f374bcdff0f539616999ecf8642a93eb5297d1c552189a2c649e3613b9c`
- `2，PC Software/ATK-DP100DLL(x64).dll`:
  `43e6277af154ecf6254a671f14f0b9fba9bd7ab1e9e7cf8b2778441d1b125ffc`

The DLL user manual documents voltage/current units, group numbers, system
settings, and public API semantics. The V1.3 product manual documents limits
(30 V, 5 A, OVP 30.5 V, OCP 5.05 A, OPP 105 W).

Read-only live captures on the attached V1.4 unit validate layouts, CRC,
addresses and sequence echo. Packet fixtures are in `protocol_test.go`. Hardware
integration reads every group and compares live settings before and after.
Subsequently, with the user's output load disconnected, `35` output writes were
hardware-tested, followed by stored-group/protection writes and recalls with
output OFF (see below). No system writes, disconnect requests, scan commands,
or update requests were sent.

To reproduce decompilation (tools are development-only):

```sh
dotnet tool install ilspycmd --tool-path /tmp/dotnet-tools --version 9.1.0.7988
/tmp/dotnet-tools/ilspycmd -p -o /tmp/dp100-decompiled \
  '../2，PC Software/net8dll/linux/ATKDP100DLL.dll'
```

## USB/HID framing

VID `0x2e3c`, PID `0xaf01`. Linux identifies the device as
`ALIENTEK ATK-MDP100`. Its descriptor declares 64-byte unnumbered input/output
reports and a one-byte feature report. There is no application report ID.

Hidraw writes require a synthetic leading zero byte, giving **65 bytes**.
Hidraw reads return **64 bytes**, without that synthetic byte. The vendor's
HidSharp implementation exposes 65-byte buffers in both directions, adding the
zero itself; do not copy its input offsets directly to hidraw.

Application frame (offsets exclude the synthetic write prefix):

| Offset | Size | Meaning |
|---|---:|---|
| 0 | 1 | Address: host `fb`, device `fa` |
| 1 | 1 | Function |
| 2 | 1 | Sequence |
| 3 | 1 | Payload byte count N (max 58) |
| 4 | N | Payload |
| 4+N | 2 | CRC-16/MODBUS, little endian |
| 6+N | remaining | Unspecified trailing bytes |

CRC covers address through payload, initial `ffff`, reflected polynomial
`a001`, no final XOR. The standard `123456789` check is `4b37`.

**Trailing input bytes are not zero padding.** The device reuses a response
buffer: short replies contain bytes from older long replies after their CRC.
Only the declared payload and CRC are meaningful. Requests are zero-filled.

The DLL uses sequence zero for most commands, but the attached V1.4 unit echoes
nonzero sequences correctly. The Go client increments a byte sequence and
matches both function and sequence, skipping unrelated/late valid reports. After
256 requests it wraps; arbitrarily delayed replies across a complete wrap cannot
be distinguished. Other firmware must be checked for sequence echo support.

## Implemented functions

All multibyte integers below are unsigned and little endian.

### `10`: device information

Empty request, 40-byte reply:

| Offset | Size | Field |
|---|---:|---|
| 0 | 16 | Model, terminated/padded by zero and/or `ff` |
| 16 | 2 | Hardware version, tenths |
| 18 | 2 | Application version, tenths |
| 20 | 2 | Bootloader version, tenths |
| 22 | 2 | Run area (`00aa` application) |
| 24 | 12 | Full factory serial bytes |
| 36 | 2 | Manufacturing year |
| 38 | 1 | Month |
| 39 | 1 | Day |

The vendor public API displays only the last four serial bytes as eight hex
characters; the Go API retains all 12 bytes. The USB serial string has its own
format and is not assumed equal to this factory field.

### `30`: measurements

Empty request, 16-byte reply:

| Offset | Size | Field |
|---|---:|---|
| 0 | 2 | Input voltage, mV |
| 2 | 2 | Output voltage, mV |
| 4 | 2 | Output current, mA |
| 6 | 2 | Maximum available output voltage, mV |
| 8 | 2 | Temperature 1, inferred 0.1 °C |
| 10 | 2 | Temperature 2, inferred 0.1 °C |
| 12 | 2 | Internal 5 V supply, mV |
| 14 | 1 | Output mode: 0 CC, 1 CV, 2 off; bit 7 undervoltage |
| 15 | 1 | Protection: 0 normal, 1 OVP, 2 OCP, 3 OTP, 4 OPP, 5 reverse, 6 UVP |

Temperature scaling is inferred from plausible live values around 370–381,
not established by an explicit DLL unit annotation. Requests work synchronously;
the DLL also routes this function as unsolicited/event telemetry. The client
starts no polling goroutine and offers snapshots, not a subscription API.

### `35`: output/group configuration

Read request: one index byte. `00`–`09` reads a group; `80` reads active settings.
Reply: 10 bytes:

| Offset | Size | Field |
|---|---:|---|
| 0 | 1 | Group/index |
| 1 | 1 | Output state, 0 off, nonzero on |
| 2 | 2 | Voltage setpoint, mV |
| 4 | 2 | Current setpoint, mA |
| 6 | 2 | OVP, mV |
| 8 | 2 | OCP, mA |

The stored group state byte is not the active output indicator. Live group reads
returned zero state while the active-settings read returned one.

Writes use the same 10-byte layout with index flags:

- `group | 20`: set output state and all four limits.
- `group | 60`: vendor `SetGroupInfo`: store group 1–9, state zero.
- `group | a0`: vendor `UseGroup`: apply group 1–9, state zero.

Write reply: one status byte; **1 is success**, anything else rejection.

Hardware testing of `group 0 | 20` on V1.4 confirmed ACK status 1, output
on/off, and voltage/current setpoint changes. **OVP/OCP fields were ignored:**
a request for 5000 mV / 500 mA / OVP 5500 mV / OCP 600 mA returned success,
but readback retained the original OVP 30500 mV and OCP 5050 mA. No API claim
should imply this command can set protection thresholds on V1.4.

With output disabled, tested setpoints were 0 mV/0 mA, 10 mV/1 mA,
5000 mV/500 mA, 12000 mV/1000 mA, and 30000 mV/5000 mA. The 30 V request
was acknowledged and was briefly readable at 30000 before the control loop
clamped it to `MaximumOutputVoltageMV` (19420 mV on the attached input supply).
A 100 ms wait before readback consistently observed the clamp in these tests.
ACK therefore means neither exact application nor settled settings.

Enabled-output tests used a 100 mA current limit, no connected load, and the
existing protection thresholds. Telemetry reported CV/normal, zero current,
and these values after settling:

| Requested mV | Measured mV |
|---:|---:|
| 1000 | 1001 |
| 3300 | 3296 |
| 5000 | 4995 |
| 12000 | 11991 |

After every off command, telemetry reported off/normal and 0 mV. These are
PSU-reported measurements, not independent meter verification. Current-limit
regulation has not been tested under load. Final voltage/current/protection
settings were restored to the original values, with output deliberately OFF.
System settings and presets 1–9 were checked unchanged.

Subsequent output-OFF hardware tests verified the storage/application flags:

- `69` stores preset 9, including all four limits, without selecting it. Writing
  5000 mV / 100 mA / OVP 5500 mV / OCP 200 mA left active group 0 unchanged.
- `a9` recalls preset 9. A deliberately different payload (3300 / 150 / 6000 /
  300) was ignored; active group 9 held the stored 5000 / 100 / 5500 / 200.
- `60` works on working group 0 too, despite the public DLL rejecting group 0
  in SetGroupInfo. It updates active limits when group 0 is selected, including
  OVP/OCP. `a0` recalls working group 0.
- Recall with all-zero limit fields (`a9` and `a0`) works without overwriting
  stored values. `RecallPreset` uses this verified payload.
- Storing the selected group updates its active limits immediately: tested on
  both group 0 and group 9. `SetProtection` uses that operation, preserving the
  current voltage/current while replacing OVP/OCP (6500/350 on group 0 and
  7000/400 on group 9). It requires output OFF at the client level.

All ten groups and global settings were checked against the saved snapshot after
restoration; output remained OFF. Recovery snapshot for the final test:
`/tmp/dp100-before-presets-1997651494.json` (ephemeral development artifact).
Tests use a 150 ms settling wait before readback. No power cycle was performed,
so flash persistence remains unverified. Behavior when applying/storing with
output already ON is also unverified; RecallPreset and SetProtection refuse it.
Actual OVP/OCP trip behavior was not exercised.

The public DLL's OpenOut/CloseOut send OVP/OCP zero; this package transmits the
caller's explicit fields instead. Do not generalize the observed ignored fields
to other commands or firmware.

### `40`: global system configuration

Empty request to read; configuration payload to write. Struct layout:

| Offset | Size | Field |
|---|---:|---|
| 0 | 2 | Over-temperature threshold, **raw/ambiguous units** |
| 2 | 2 | OPP, 0.1 W |
| 4 | 1 | Backlight 0–4 |
| 5 | 1 | Buzzer volume 0–4 |
| 6 | 1 | Reverse-polarity protection enable (firmware >= 1.3) |
| 7 | 1 | Automatic output on boot (firmware >= 1.3) |

Write ACK: one byte, 1 means success. The enum also defines `45` as system SET,
but the supplied readable DLL **actually sends `40` for writes**, discriminated
by payload length. The Go implementation follows that behavior.

The manual calls OTP 0.1 °C with a 50–80 °C range, but the V1.4 unit returns raw
`0050` (80 decimal), suggesting whole °C for the threshold. This differs from
measurement temperature scaling. The API deliberately does not convert or enforce
an assumed OTP range. Preserve the read value until firmware-specific behavior
is verified. Reading 6-byte replies and writing 6 bytes for firmware <1.3 is a
compatibility inference from versioned features, **not hardware verified**; the
readable .NET 8 DLL itself always serializes the full 8-byte struct.

## Recovered but not exposed

The function enum also includes firmware operations `11` metadata, `12` start,
`13` data transfer, `14` end, `15` upgrade, scan `50`, sequential output `55`, and
`80` disconnect (DLL sends payload `ff`). These are not required for ordinary
control and were not sent to the connected PSU. Decompilation contains apparent
"encryption" helpers; the ordinary command send path does **not** call them.

The `.atk` container has since been decoded and CRC-validated offline; see
[firmware/README.md](firmware/README.md) for its layout, the low-level update wrappers, and
which parts of the protected Windows updater remain unresolved.

## Remaining verification

- Global system writes and output-ON preset behavior (OFF behavior is verified).
- Loaded current-limit regulation and independent output voltage measurements.
- OTP units and old-firmware payload compatibility.
- Firmware V1.5 differences and nonzero sequence behavior on other versions.
- Quantization, persistence, and protection threshold edge cases.
- Scan/sequential output and firmware transfer, only if needed later.
