// Copyright 2023 Ekorau LLC
// Vendored verbatim from ~/workspaceToit/vindriktning/vindriktning.toit — the
// hardware-proven PM1006 (IKEA VINDRIKTNING) driver. Kept as-is (incl. the dummy
// tx=gpio.Pin 17 the UART needs) rather than reimplemented.

import io show Reader
import serial
import gpio
import uart show Port

class Vindriktning:

  frame := #[]
  air-quality_ := -1

  rx_/gpio.Pin
  port/Port
  reader/Reader

  constructor rx/int:
    rx_ = gpio.Pin rx
    port = Port
            --tx = gpio.Pin 17 // Not used.
            --rx = rx_
            --baud-rate = 9600
    reader = port.in

  air-quality -> int:
    return air-quality_

  checksum bytes/ByteArray -> int:
    sum := 0
    for i := 0; i < (bytes.size); i++:
      sum += bytes[i]
    return sum & 0x00FF

  last-frame -> string:
    return frame.size == 0?
      "-- none --":
      // From CoPilot.
      "$frame[0] $frame[1] $frame[2] $frame[3] $frame[4] $(frame[5]) $frame[6] $(frame[7]) $(frame[8]) $(frame[9]) $(frame[10]) $(frame[11]) $(frame[12]) $(frame[13]) $(frame[14]) $(frame[15]) $(frame[16]) $(frame[17]) $(frame[18]) $(frame[19])"
      // (List 20: "$(%02x frame[it])").join " "  // Elegant alternative from kasperl.

  next -> ByteArray?:
    frame = reader.read
    while frame.size != 20 or frame[0] != 0x16 or frame[1] != 0x11 or frame[2] != 0x0b or (checksum frame) != 0:
      frame = reader.read
    air-quality_ = frame[5] << 8 | frame[6]
    return frame
