//go:build !linux

package dp100

// Open is unavailable outside Linux. Use New with a custom Transport instead.
func Open(path string) (*Client, error) { return nil, ErrUnsupported }

// Discover is unavailable outside Linux.
func Discover() ([]Device, error) { return nil, ErrUnsupported }
