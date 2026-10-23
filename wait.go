package dp100

import (
	"context"
	"fmt"
	"time"
)

// WaitEnabled polls live Settings until Enabled matches enabled, returning the
// last successfully read settings even on error. It sends NO writes and never
// retries a write. Supply a context deadline to bound the entire wait; each read
// also has the normal one-second transaction limit. Communication errors stop
// immediately. interval must be positive; the first read is immediate.
//
// A match verifies only the reported logical enable state at that instant, not
// the physical output, regulation mode, or capacitor discharge. This method does
// not read telemetry, check faults, impose a voltage threshold, or require
// consecutive samples. Those policies belong to the caller. Other operations
// may interleave between reads. Use SetEnabled separately if a write is desired.
func (c *Client) WaitEnabled(ctx context.Context, enabled bool, interval time.Duration) (OutputSettings, error) {
	var last OutputSettings
	if interval <= 0 {
		return last, fmt.Errorf("dp100: polling interval must be positive")
	}
	for {
		s, err := c.Settings(ctx)
		if err != nil {
			return last, err
		}
		last = s
		if s.Enabled == enabled {
			return last, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, ctx.Err()
		case <-c.done:
			timer.Stop()
			return last, ErrClosed
		case <-timer.C:
		}
	}
}
