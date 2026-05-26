// device/pm1006.toit — minimal PM1006 (IKEA VINDRIKTNING) particulate-sensor frame
// reader. Pure decode helpers + a thin UART reader. Vendored from
// ~/workspaceToit/vindriktning/vindriktning.toit to keep the payload self-contained.
import gpio
import uart

PM1006-FRAME-SIZE ::= 20

/**
Whether $bytes is a well-formed PM1006 frame: exactly 20 bytes, header 16 11 0b,
  and a zero modulo-256 checksum over all 20 bytes.
*/
pm1006-valid-frame bytes/ByteArray -> bool:
  if bytes.size != PM1006-FRAME-SIZE: return false
  if bytes[0] != 0x16 or bytes[1] != 0x11 or bytes[2] != 0x0b: return false
  sum := 0
  bytes.do: sum += it
  return (sum & 0xff) == 0

/** The PM2.5 reading (µg/m³) carried in a valid PM1006 frame $bytes (bytes 5..6, big-endian). */
pm1006-pm25 bytes/ByteArray -> int:
  return (bytes[5] << 8) | bytes[6]

/** A PM1006 sensor on a UART RX pin (9600 baud, 8N1). Only RX is used; TX is null. */
class Pm1006:
  port_/uart.Port

  constructor --rx/int:
    port_ = uart.Port --tx=null --rx=(gpio.Pin rx) --baud-rate=9600

  /** Blocks reading the UART until a valid frame arrives; returns its PM2.5 µg/m³ value. */
  read-pm25 -> int:
    reader := port_.in
    while true:
      frame := reader.read
      if frame and (pm1006-valid-frame frame): return pm1006-pm25 frame

  close -> none: port_.close
