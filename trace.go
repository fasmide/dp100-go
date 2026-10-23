package dp100

import "time"

// TraceEvent records one stage of an exchange. Elapsed is measured using Go's
// monotonic clock from SetTrace. Report is an owned copy: 65 bytes including the
// zero report ID for writes, 64 bytes without it for reads. It includes raw mode,
// group/flag, enable, ACK, and telemetry bytes even when decoding fails.
//
// Stage is "write" (attempt, not proof of delivery), "written", "read", or
// "error". Command/Sequence identify the requested exchange. For valid read
// frames, ResponseCommand/ResponseSequence and Payload describe the response;
// Matched reports command/sequence equality, NOT a successful ACK or application
// of a setting. Valid is false for malformed reports. Err describes an error
// stage or a read decoding failure. A successful ACK is Matched && Valid with
// a one-byte Payload equal to 1, for a write request expecting an ACK.
type TraceEvent struct {
	Elapsed                           time.Duration
	Exchange                          uint64
	Stage                             string
	Command, Sequence                 byte
	ResponseCommand, ResponseSequence byte
	Valid, Matched                    bool
	Report, Payload                   []byte
	Err                               error
}

type traceSink struct {
	start time.Time
	emit  func(TraceEvent)
}

// SetTrace enables exchange diagnostics; nil disables them. It is safe to call
// concurrently with operations. An in-flight exchange keeps its original sink.
// The callback runs synchronously inside the transaction lock and must return
// promptly, must not call Client methods, and must not panic. Prefer appending
// to a bounded in-memory buffer and writing it elsewhere; slow logging changes
// timing. No background goroutine, extra I/O, or retries are introduced.
// Trace data can contain device identity and operational settings.
func (c *Client) SetTrace(emit func(TraceEvent)) {
	if emit == nil {
		c.trace.Store(nil)
		return
	}
	c.trace.Store(&traceSink{start: time.Now(), emit: emit})
}

func (s *traceSink) record(e TraceEvent) {
	if s == nil {
		return
	}
	e.Elapsed = time.Since(s.start)
	e.Report = append([]byte(nil), e.Report...)
	e.Payload = append([]byte(nil), e.Payload...)
	s.emit(e)
}
