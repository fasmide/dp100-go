package dp100

import (
	"encoding/binary"
	"fmt"
)

const (
	reportSize               = 64
	maxPayload               = reportSize - 6
	commandInfo         byte = 0x10
	commandMeasurements byte = 0x30
	commandSettings     byte = 0x35
	commandSystem       byte = 0x40
)

type frame struct {
	command, sequence byte
	data              []byte
}

// crc16 implements CRC-16/MODBUS (initial value 0xffff, reflected polynomial 0xa001).
func crc16(data []byte) uint16 {
	crc := uint16(0xffff)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xa001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// Output reports include the synthetic zero report ID required by hidraw.
func encode(f frame) ([]byte, error) {
	if len(f.data) > maxPayload {
		return nil, fmt.Errorf("dp100: payload exceeds %d bytes", maxPayload)
	}
	b := make([]byte, reportSize+1)
	b[1], b[2], b[3], b[4] = 0xfb, f.command, f.sequence, byte(len(f.data))
	copy(b[5:], f.data)
	end := 5 + len(f.data)
	binary.LittleEndian.PutUint16(b[end:], crc16(b[1:end]))
	return b, nil
}

// Input reports have no report ID. Bytes after the CRC are unspecified, not padding.
func decode(b []byte) (frame, error) {
	if len(b) != reportSize {
		return frame{}, fmt.Errorf("%w: input report size %d, want %d", ErrProtocol, len(b), reportSize)
	}
	if b[0] != 0xfa {
		return frame{}, fmt.Errorf("%w: unexpected device address %#02x", ErrProtocol, b[0])
	}
	n := int(b[3])
	if n > maxPayload {
		return frame{}, fmt.Errorf("%w: invalid payload length %d", ErrProtocol, n)
	}
	end := 4 + n
	if binary.LittleEndian.Uint16(b[end:]) != crc16(b[:end]) {
		return frame{}, fmt.Errorf("%w: response CRC mismatch", ErrProtocol)
	}
	return frame{b[1], b[2], append([]byte(nil), b[4:end]...)}, nil
}

func requireLength(b []byte, n int) error {
	if len(b) != n {
		return fmt.Errorf("%w: payload size %d, want %d", ErrProtocol, len(b), n)
	}
	return nil
}

func uint16At(b []byte, offset int) uint16 { return binary.LittleEndian.Uint16(b[offset:]) }
