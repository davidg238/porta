import io
import system.containers
import tftp show TFTPClient

import .flash_image show flash-image
import .header

/**
Gateway LAN IP where `host/serve.toit` is running. Adjust to match the host
  machine's address on the shared network.
*/
GATEWAY-HOST ::= "192.168.0.175"

/**
UDP port the TFTP server listens on. Must match the PORT constant in
  `host/serve.toit`. Port 6969 is used so the server needs no root privileges
  (port 69 would require root).
*/
GATEWAY-PORT ::= 6969

/**
Executes the M1 smoke-test pass: pulls the firmware blob from the TFTP gateway,
  flashes the container image into transient storage, and starts it.

Connects a $TFTPClient to $GATEWAY-HOST:$GATEWAY-PORT, reads the file named
  "firmware" (the full blob as `[u32 size_le][u32 crc32_le][image bytes]`),
  parses the 8-byte $Header to extract image size and CRC32, wraps the image
  bytes in an $io.Reader, and hands them to $flash-image which verifies the
  digest and commits the image. The returned UUID is then passed to
  `containers.start` to launch the payload container.

The payload is expected to print "delivered tick N" heartbeat lines on the
  serial console — that output is the smoke-test success proof.

Deep-sleep and re-poll on wake are deferred to M2.
*/
main:
  client := TFTPClient --host=GATEWAY-HOST
  client.port = GATEWAY-PORT  // TFTPClient has no --port constructor arg; set before open
  client.open
  blob/ByteArray := #[]
  try:
    blob = client.read-bytes "firmware"
  finally:
    client.close
  header := parse-header blob
  image-bytes := blob[8 .. 8 + header.size]
  print "loader: pulled blob=$blob.size image=$header.size crc32=$header.crc32"
  id := flash-image header.size (io.Reader image-bytes) "payload" --crc32=header.crc32
  print "loader: installed $id, starting"
  containers.start id
  print "loader: started payload"
