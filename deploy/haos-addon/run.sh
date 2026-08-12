#!/bin/sh
set -e
mkdir -p /share/infinid
exec /infinid \
  -serial /dev/serial/by-id/usb-FTDI_FT232R_USB_UART_BH002W7M-if00-port0 \
  -capture /share/infinid/capture.jsonl
