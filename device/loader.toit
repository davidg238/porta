import system.containers
import tftp show TFTPClient

import .blob_sink show BlobInstallWriter
import .flash_image show ContainerImageInstaller

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
  streams the container image into transient storage, and starts it.

Connects a $TFTPClient to $GATEWAY-HOST:$GATEWAY-PORT and reads the file named
  "firmware" (the full blob as `[u32 size_le][u32 crc32_le][image bytes]`)
  straight into a $BlobInstallWriter. The writer peels the 8-byte header,
  streams the image bytes into a $ContainerImageInstaller (verifying size and
  CRC32) without ever holding the whole image in RAM, and commits. The returned
  UUID is then passed to `containers.start` to launch the payload container.

The payload is expected to print "delivered tick N" heartbeat lines on the
  serial console — that output is the smoke-test success proof.

Deep-sleep and re-poll on wake are deferred to M2.
*/
main:
  client := TFTPClient --host=GATEWAY-HOST
  client.port = GATEWAY-PORT  // TFTPClient has no --port constructor arg; set before open.
  client.open
  sink := BlobInstallWriter (ContainerImageInstaller "payload")
  installed := false
  try:
    client.read "firmware" --to-writer=sink
    id := sink.commit
    print "loader: installed $id, starting"
    containers.start id
    installed = true
    print "loader: started payload"
  finally:
    client.close
    // If the transfer or commit threw mid-way, release the (possibly opened)
    // ContainerImageWriter so it does not leak across an M2 re-poll loop.
    if not installed: sink.abort
