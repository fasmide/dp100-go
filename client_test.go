package dp100

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeTransport struct {
	mu       sync.Mutex
	writes   [][]byte
	replies  chan []byte
	closed   chan struct{}
	once     sync.Once
	respond  func([]byte) [][]byte
	writeErr error
}

func newFake() *fakeTransport {
	return &fakeTransport{replies: make(chan []byte, 32), closed: make(chan struct{})}
}
func (f *fakeTransport) Close() error { f.once.Do(func() { close(f.closed) }); return nil }
func (f *fakeTransport) WriteReport(ctx context.Context, b []byte) error {
	f.mu.Lock()
	f.writes = append(f.writes, append([]byte(nil), b...))
	f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	if f.respond != nil {
		for _, r := range f.respond(b) {
			select {
			case f.replies <- r:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}
func (f *fakeTransport) ReadReport(ctx context.Context) ([]byte, error) {
	select {
	case r := <-f.replies:
		return r, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.closed:
		return nil, ErrClosed
	}
}
func reply(cmd, seq byte, data []byte) []byte {
	b := make([]byte, 64)
	b[0], b[1], b[2], b[3] = 0xfa, cmd, seq, byte(len(data))
	copy(b[4:], data)
	binary.LittleEndian.PutUint16(b[4+len(data):], crc16(b[:4+len(data)]))
	return b
}
func captureResponder(t *testing.T) func([]byte) [][]byte {
	return func(b []byte) [][]byte {
		captures := map[byte]string{commandInfo: infoCapture, commandMeasurements: measurementsCapture, commandSettings: settingsCapture, commandSystem: systemCapture}
		frame, err := decode(unhex(t, captures[b[2]]))
		if err != nil {
			t.Fatal(err)
		}
		return [][]byte{reply(b[2], b[3], frame.data)}
	}
}
func TestReadAPI(t *testing.T) {
	f := newFake()
	f.respond = captureResponder(t)
	c := New(f)
	defer c.Close()
	ctx := context.Background()
	info, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "ATK-DP100" || info.Firmware != 14 || info.Hardware != 14 || info.Bootloader != 11 || info.RunArea != 0xaa || info.Year != 2024 || info.Month != 4 || info.Day != 3 {
		t.Fatalf("info: %+v", info)
	}
	if info.Firmware.String() != "V1.4" {
		t.Fatal(info.Firmware)
	}
	m, err := c.Measurements(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m.InputVoltageMV != 19990 || m.OutputVoltageMV != 19106 || m.OutputCurrentMA != 307 || m.MaximumOutputVoltageMV != 19420 || m.Temperature1DeciC != 370 || m.Temperature2DeciC != 376 || m.InternalSupplyMV != 5078 || m.Mode != ConstantVoltage || m.State != Normal {
		t.Fatalf("measurements: %+v", m)
	}
	s, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Group != 0 || !s.Enabled || s.VoltageMV != 19110 || s.CurrentMA != 2663 || s.OverVoltageMV != 30500 || s.OverCurrentMA != 5050 {
		t.Fatalf("settings: %+v", s)
	}
	sys, err := c.System(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sys.OverTemperatureRaw != 80 || sys.OverPowerDeciW != 1050 || sys.Backlight != 2 || sys.Volume != 1 || !sys.ReverseProtection || sys.AutoOutput {
		t.Fatalf("system: %+v", sys)
	}
	if _, err := c.Preset(ctx, 9); err != nil {
		t.Fatal(err)
	}
}
func TestWriteAPI(t *testing.T) {
	f := newFake()
	f.respond = func(b []byte) [][]byte { return [][]byte{reply(b[2], b[3], []byte{1})} }
	c := New(f)
	defer c.Close()
	ctx := context.Background()
	limits := Limits{12000, 1000, 15000, 2000}
	for _, tc := range []struct {
		name         string
		run          func() error
		index, state byte
	}{
		{"output", func() error { return c.SetOutput(ctx, OutputSettings{Group: 0, Enabled: true, Limits: limits}) }, 0x20, 1},
		{"off", func() error { return c.SetOutput(ctx, OutputSettings{Group: 2, Limits: limits}) }, 0x22, 0},
		{"store", func() error { return c.StorePreset(ctx, 3, limits) }, 0x63, 0},
		{"apply", func() error { return c.ApplyPreset(ctx, 4, limits) }, 0xa4, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err != nil {
				t.Fatal(err)
			}
			b := f.writes[len(f.writes)-1]
			if b[2] != commandSettings || b[4] != 10 || b[5] != tc.index || b[6] != tc.state || uint16At(b, 7) != 12000 || uint16At(b, 9) != 1000 || uint16At(b, 11) != 15000 || uint16At(b, 13) != 2000 {
				t.Fatalf("write = %x", b)
			}
			if uint16At(b, 15) != crc16(b[1:15]) {
				t.Fatal("bad CRC")
			}
		})
	}
}
func TestSetSystem(t *testing.T) {
	for _, version := range []uint16{12, 14} {
		t.Run(Version(version).String(), func(t *testing.T) {
			f := newFake()
			f.respond = func(b []byte) [][]byte {
				if b[2] == commandInfo {
					data := make([]byte, 40)
					binary.LittleEndian.PutUint16(data[18:], version)
					return [][]byte{reply(b[2], b[3], data)}
				}
				return [][]byte{reply(b[2], b[3], []byte{1})}
			}
			c := New(f)
			defer c.Close()
			err := c.SetSystem(context.Background(), SystemSettings{OverTemperatureRaw: 80, OverPowerDeciW: 1050, Backlight: 4, Volume: 3})
			if err != nil {
				t.Fatal(err)
			}
			b := f.writes[1]
			want := byte(8)
			if version < 13 {
				want = 6
			}
			if b[2] != commandSystem || b[4] != want || uint16At(b, 5) != 80 || uint16At(b, 7) != 1050 || b[9] != 4 || b[10] != 3 {
				t.Fatalf("write=%x", b)
			}
			if version < 13 {
				if err := c.SetSystem(context.Background(), SystemSettings{AutoOutput: true}); err == nil {
					t.Fatal("unsupported auto output accepted")
				}
			}
		})
	}
}
func TestValidationDoesNotWrite(t *testing.T) {
	f := newFake()
	c := New(f)
	defer c.Close()
	ctx := context.Background()
	for _, err := range []error{
		c.SetOutput(ctx, OutputSettings{Group: 10}), c.SetOutput(ctx, OutputSettings{Limits: Limits{VoltageMV: 30001}}),
		c.StorePreset(ctx, 0, Limits{}), c.ApplyPreset(ctx, 10, Limits{}), c.SetSystem(ctx, SystemSettings{Volume: 5}),
	} {
		if err == nil {
			t.Fatal("invalid argument accepted")
		}
	}
	if _, err := c.Preset(ctx, 10); err == nil {
		t.Fatal("invalid preset accepted")
	}
	if len(f.writes) != 0 {
		t.Fatal("invalid request wrote to hardware")
	}
}
func TestUnrelatedResponses(t *testing.T) {
	f := newFake()
	f.respond = func(b []byte) [][]byte {
		return [][]byte{
			reply(commandMeasurements, b[3], make([]byte, 16)), reply(commandInfo, b[3]-1, make([]byte, 40)), reply(b[2], b[3], make([]byte, 40)),
		}
	}
	c := New(f)
	defer c.Close()
	if _, err := c.Info(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestWriteErrorsAndNoRetry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		data     []byte
		writeErr error
	}{
		{name: "reject", data: []byte{0}}, {name: "malformed", data: nil}, {name: "transport", writeErr: errors.New("write failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			f.writeErr = tc.writeErr
			f.respond = func(b []byte) [][]byte { return [][]byte{reply(b[2], b[3], tc.data)} }
			c := New(f)
			defer c.Close()
			err := c.SetOutput(context.Background(), OutputSettings{})
			if err == nil {
				t.Fatal("error lost")
			}
			if tc.name == "reject" {
				var deviceErr *DeviceError
				if !errors.As(err, &deviceErr) || deviceErr.Status != 0 {
					t.Fatal(err)
				}
			}
			if len(f.writes) != 1 {
				t.Fatal("write was retried")
			}
		})
	}
}
func TestCancellationAndClose(t *testing.T) {
	f := newFake()
	c := New(f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Info(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(f.writes) != 0 {
		t.Fatal("cancelled request wrote")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.Info(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := c.Info(context.Background()); done <- err }()
	time.Sleep(time.Millisecond)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not unblock read")
	}
	if _, err := c.Info(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentRequests(t *testing.T) {
	f := newFake()
	f.respond = func(b []byte) [][]byte { return [][]byte{reply(b[2], b[3], make([]byte, 40))} }
	c := New(f)
	defer c.Close()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Info(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for i, b := range f.writes {
		if b[3] != byte(i+1) {
			t.Fatalf("sequence = %d", b[3])
		}
	}
}
func TestMalformedPayloads(t *testing.T) {
	f := newFake()
	f.respond = func(b []byte) [][]byte { return [][]byte{reply(b[2], b[3], []byte{1, 2})} }
	c := New(f)
	defer c.Close()
	ctx := context.Background()
	_, a := c.Info(ctx)
	_, b := c.Measurements(ctx)
	_, d := c.Settings(ctx)
	_, e := c.System(ctx)
	for _, err := range []error{a, b, d, e} {
		if err == nil {
			t.Fatal("malformed payload accepted")
		}
	}
}
