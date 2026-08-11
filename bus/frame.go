// bus/frame.go
// Frame codec for the Carrier Infinity ABCD bus.
// Ported from acd/infinitive (MIT) infinity/frame.go with exported types,
// an inlined CRC-16/ARC, and no logging dependencies.
package bus

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// Device addresses observed on the bus.
const (
	DevWallControl = uint16(0x2001)
	DevAirHandler  = uint16(0x3e01)
	DevHeatPump    = uint16(0x5201)
	DevDCM1        = uint16(0x6001) // damper control module, zones 1-4
	DevDCM2        = uint16(0x6101) // second DCM, zones 5-8
	DevSAM         = uint16(0x9201) // the address infinid answers as (v2)
)

// Frame ops.
const (
	OpAck02 = uint8(0x02)
	OpAck06 = uint8(0x06)
	OpRead  = uint8(0x0b)
	OpWrite = uint8(0x0c)
	OpNack  = uint8(0x15)
	OpAlarm = uint8(0x1e)
)

var opNames = map[uint8]string{
	OpAck02: "ACK02",
	OpAck06: "ACK06",
	OpRead:  "READ",
	OpWrite: "WRITE",
	OpNack:  "NACK",
	OpAlarm: "ALARM",
}

func opString(op uint8) string {
	if s, ok := opNames[op]; ok {
		return s
	}
	return fmt.Sprintf("UNKNOWN(%02x)", op)
}

// crc16ARC computes CRC-16/ARC: poly 0x8005 reflected (0xA001), init 0,
// xorout 0. Matches the npat-efault/crc16 configuration infinitive uses.
func crc16ARC(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// Frame is one bus frame. Wire layout:
// dst(2,BE) src(2,BE) dataLen(1) 0x00 0x00 op(1) data crc(2,LE).
type Frame struct {
	Dst  uint16
	Src  uint16
	Op   uint8
	Data []byte
}

func (f Frame) String() string {
	return fmt.Sprintf("%04x -> %04x: %-8s %x", f.Src, f.Dst, opString(f.Op), f.Data)
}

// Encode renders the frame to wire bytes including CRC.
func (f Frame) Encode() []byte {
	if len(f.Data) > 255 {
		panic("frame data too large")
	}
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, f.Dst)
	binary.Write(&b, binary.BigEndian, f.Src)
	b.WriteByte(byte(len(f.Data)))
	b.WriteByte(0)
	b.WriteByte(0)
	b.WriteByte(f.Op)
	b.Write(f.Data)
	crc := crc16ARC(b.Bytes())
	b.WriteByte(byte(crc))      // low byte first (little-endian on wire)
	b.WriteByte(byte(crc >> 8)) // high byte
	return b.Bytes()
}

// Decode parses one complete frame from buf (header + data + CRC exactly).
// Returns false on CRC mismatch, short buffer, or all-zero input.
func (f *Frame) Decode(buf []byte) bool {
	if len(buf) < 10 {
		return false
	}
	nonzero := false
	for _, c := range buf {
		if c != 0 {
			nonzero = true
			break
		}
	}
	if !nonzero {
		return false
	}
	l := len(buf) - 2
	want := crc16ARC(buf[:l])
	got := uint16(buf[l]) | uint16(buf[l+1])<<8
	if want != got {
		return false
	}
	f.Dst = binary.BigEndian.Uint16(buf[0:2])
	f.Src = binary.BigEndian.Uint16(buf[2:4])
	f.Op = buf[7]
	f.Data = append([]byte{}, buf[8:l]...)
	return true
}
