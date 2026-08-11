// bus/serial.go
package bus

import (
	"io"

	"go.bug.st/serial"
)

// OpenSerial opens the RS-485 device at the Carrier bus rate (38400 8N1).
func OpenSerial(device string) (io.ReadWriteCloser, error) {
	return serial.Open(device, &serial.Mode{
		BaudRate: 38400,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	})
}
