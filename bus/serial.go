// bus/serial.go

package bus

import (
	"go.bug.st/serial"
)

// OpenSerial opens the RS-485 device at the Carrier bus rate (38400 8N1)
// and discards any stale driver-buffered input. It returns the concrete
// serial.Port so callers keep access to timeouts and RS-485 controls.
func OpenSerial(device string) (serial.Port, error) {
	port, err := serial.Open(device, &serial.Mode{
		BaudRate: 38400,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	})
	if err != nil {
		return nil, err
	}
	_ = port.ResetInputBuffer()
	return port, nil
}
