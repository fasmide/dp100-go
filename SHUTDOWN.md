# Investigating acknowledged OFF without physical shutdown

## Current conclusion

The reported trial 274 cannot be attributed to a single cause without its raw
OFF exchange and readbacks. A successful ACK does **not** establish that output
was disabled. We have not reproduced that OFF transaction: this investigation
sent only read queries to the hardware, no output commands.

### Evidence established

- The relevant files (`client.go`, `device.go`, `protocol.go`, `hidraw_linux.go`)
  match reported dependency revision `58494c25b8f0` before these diagnostic edits.
- `SetEnabled(false)` queries index `80`, extracts the group from the low nibble,
  and sends command `35`, index `group | 20`, state `00`, with the four limits
  preserved. There is no early return when settings already report OFF.
  Groups 0–9 are accepted; invalid low-nibble groups fail before the write.
- The readable vendor DLL's `CloseOut` sets state 0 and `SetBasic` ORs index
  with `20`. Store and recall use `60` and `a0`, respectively. Our disable does
  not intentionally take either path. The DLL sends OVP/OCP zero, unlike our
  preserving payload. Prior V1.4 testing found those fields ignored for `20`;
  whether retransmission interacts with the intermittent failure is unproven.
- Frames require the device address, exact report length, valid payload length,
  and CRC. Replies must match both command and 8-bit sequence. Write ACKs must
  contain exactly one byte, equal to 1. The vendor DLL is less strict: it normally
  uses sequence zero and matches only command, then checks status 1.
- Ordinary sequence wrap is supported. A duplicate reply delayed by a full
  256-request cycle has the same wire identity and **cannot be distinguished**
  from a new response. There is no generation number or on/off identity in an
  ACK. Queued duplicates after errors, device resets, and process/client reopen
  deserve capture. No arbitrary delay, sequence-reset workaround, queue draining,
  or automatic retry has been added.

### Important new read-only observation

On the attached USB serial `22C2EC1FC000`, identity reads confirmed hardware
V1.4, firmware V1.4, bootloader V1.1. A 260-measurement read-only run, with about
100 ms between queries, produced 263 matching responses including one sequence
wrap, zero mismatches. Settings before and after reported group 0, Enabled=false,
19110 mV / 2663 mA / OVP 30500 mV / OCP 5050 mA. Measurements throughout showed
CV/normal, approximately 19103 mV and 142–144 mA. Settings did not change.

An immediate follow-up raw capture confirmed this is not boolean decoding of a
nonzero byte. Relevant payloads (CRC-validated, trailing stale bytes omitted):

| Request | Reply payload | Meaning |
|---|---|---|
| `35`, seq `01`, `80` | `00 00 a6 4a 67 0a 24 77 ba 13` | Group 0, raw state 0 |
| `35`, seq `02`, `00` | `00 00 a6 4a 67 0a 24 77 ba 13` | Stored group 0, raw state 0 |
| `30`, seq `03`, empty | `1c 4e 9f 4a 8e 00 d2 4b 74 01 7d 01 d1 13 01 00` | 19103 mV, 142 mA, CV/normal |

This contradicts using `Settings.Enabled=false` alone as proof of physical OFF.
It does not establish whether the firmware reports requested state, a stale
state, or some other internal state in this condition. The vendor struct exposes
this same raw byte without additional interpretation. Earlier output-control
captures showed live state 1 while ordinary group reads returned 0, so neither
"always live" nor "always stored" is justified universally. Telemetry can also
be stale; neither readback is an independent electrical measurement.

The physical ON report supplied by the user is stronger evidence than an ACK.
The present read-only observation supports a settings/telemetry inconsistency,
not proof that a particular captured OFF command was ignored. We have no such
captured command yet. External power/backfeed paths and measurement freshness
remain potential confounders in any electrical diagnosis.

## API contract

- `SetEnabled`: one read/modify/write, returns ACK success only. No write retry.
- `WaitEnabled`: optional read-only context-bounded polling for the reported
  settings bit, returning last successful settings on failure. This is **not**
  physical verification and can return immediately in the contradictory state
  above. Use it only when that bit is the intended condition.
- Discharge verification: caller-owned policy using measurements, faults,
  voltage threshold, and consecutive samples. The existing condition
  `!settings.Enabled && measurements.Mode == dp100.OutputOff &&
  measurements.OutputVoltageMV < 1000` must not be weakened to settings alone.
- Settings and measurements are separate requests and not an atomic snapshot.
  Transient disagreement is possible. No upper bound on reporting/application
  latency is established, nor is CV while voltage remains high a documented
  normal discharge phase. A persistent contradiction should be retained as a
  diagnostic failure, not hidden by extending the timeout.
- The OFF checks in RecallPreset/SetProtection inspect the reported settings bit;
  they are not physical interlocks. Do not use them as safety guarantees.

## Capture the next incident

Enable tracing **before** the experiment's first transaction. `SetTrace` adds no
polls, delays, writes, or retries. Its synchronous callback does introduce a small
copy/time cost; do not print or flush files inside it. Retain a bounded in-memory
ring and dump on failure. Trace events include monotonic elapsed nanoseconds,
a local non-wrapping exchange counter, request command/sequence, raw transmit
reports, every received report (including ignored replies), decoded response
command/sequence/payload, match decisions, and I/O/decode errors.

```go
const capacity = 8192 // Size for the desired pre-failure history.
var mu sync.Mutex
ring := make([]dp100.TraceEvent, capacity)
next, count := 0, 0
client.SetTrace(func(e dp100.TraceEvent) {
    mu.Lock()
    ring[next] = e
    next = (next + 1) % capacity
    if count < capacity { count++ }
    mu.Unlock()
})
// On failure, stop/join experiment workers, disable trace, then snapshot in order.
client.SetTrace(nil)
mu.Lock()
history := make([]dp100.TraceEvent, count)
for i := range history {
    history[i] = ring[(next-count+i+capacity)%capacity]
}
mu.Unlock()
// Encode history to a protected local file. Reports/payloads are []byte;
// encoding/json represents them as base64. Err should be saved as text too.
```

Keep application-level records alongside the frames:

1. Module revision, device Info, USB serial, and OS/device path; note any other
   process/front-panel use. Do not run a second diagnostic client concurrently.
2. Trial number and elapsed timestamp immediately before/after `SetEnabled`;
   retain its error (including nil) without calling it "OFF verified".
3. Every settings and measurement read, each with its own start/end timestamp
   and error. Record full structs, including Enabled, Group, Mode, State, voltage,
   current, input voltage, and all limits. Record which discharge predicates pass
   and the consecutive-good counter, not just the final deadline error.
4. Capture at least a complete sequence cycle before OFF and all subsequent
   readbacks. On failure preserve the original OFF and any later ON requests;
   only the initial OFF followed by no later enabling write supports an ignored
   OFF hypothesis. Trace's "written" stage means transport completion only.
5. Check the OFF trace: valid live read -> index `group|20`, state `00` -> matching
   one-byte `01` ACK. Look for command/sequence collisions, unmatched late ACKs,
   prior errors, and especially a previously observed identical ACK one full
   sequence cycle earlier. Raw report identity alone cannot prove its age.
6. Correlate with front-panel state and, where safe, independent output voltage.
   Timestamp the physical observation. Do not infer actual discharge from a
   stored/requested state byte.

Normal shutdown must retain the application's existing deadline, fault policy,
voltage threshold and three-sample requirement. On failure, stop the experiment,
retain the capture, and use the application's explicit safety/recovery procedure.
Do not silently resend OFF or continue cycling after an uncertain result.

## Reproduction stages

Offline tests cover exact packets for every active group, wrong command/sequence,
ordinary wrap with late replies, unsolicited telemetry, malformed/rejected ACKs,
full-cycle ACK aliasing, acknowledged but unchanged state, delayed state, and
read-only waiting without write retries. Run `go test -race ./...`.

Next hardware step requires explicit authorization and load/isolation checks:
use the application's real sequential command cadence with tracing enabled,
record every ON/OFF and readback, and stop at the first contradiction. Prefer a
small supervised run before hundreds of cycles. Repeat only if safe; independently
measure output and isolate the controller/display interconnects as appropriate
for backfeed diagnosis. Do not change encoding to vendor-style zero protection
fields or fixed sequence zero without a separate controlled comparison.

**Disposition:** no demonstrated disable-encoding defect; command/sequence
matching is correct within the 8-bit protocol's ambiguity; readback is now shown
to be insufficient as a physical-state guarantee; firmware ACK-without-application
is plausible but not proven. The original event remains insufficiently captured.
