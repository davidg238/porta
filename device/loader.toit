import esp32
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
How long to deep-sleep between polls. On wake the ESP32 reboots, jaguar
  restarts this loader (a named installed container) and the payload (tagged
  with the Jaguar-installed magic), and $main runs again.
*/
POLL-PERIOD ::= Duration --s=30

/**
How long to let the freshly started payload print heartbeats before
  deep-sleeping, so the smoke test can observe it on the serial console.
*/
PAYLOAD-OBSERVE ::= Duration --s=5

/**
Runs one M2 poll cycle, then deep-sleeps.

Pulls the firmware blob from the TFTP gateway and streams it through a
  $BlobInstallWriter into a named $ContainerImageInstaller (so the payload
  persists across deep-sleep), starts the payload, lets it print for
  $PAYLOAD-OBSERVE, then `esp32.deep-sleep`s for $POLL-PERIOD. Deep-sleep wakes
  via a full reboot, so the cycle repeats by re-running $main rather than
  looping in-process.

A failed poll (gateway down, CRC mismatch, network drop) is caught and traced
  rather than propagated, so the device still deep-sleeps and retries on the
  next wake; the last successfully installed payload keeps running meanwhile
  (jaguar restarted it on boot). Mirrors jaguar's `catch --trace:
  run-installed-containers` (jaguar.toit:110).

On wake the serial console should show the payload heartbeat reappear
  (restarted by jaguar's `run-installed-containers`) and this loader re-poll —
  the M2 success criterion.

# Known limitation (smoke test)
Each wake starts the payload twice: once by jaguar's `run-installed-containers`
  (it auto-restarts the persisted, magic-tagged image) and once by this loader's
  re-pull + `containers.start`. While the gateway payload is unchanged both run
  the same image (same UUID, so flash holds only one image and nothing
  accumulates — deep-sleep clears running state each cycle), so it is harmless
  here, just redundant. The production client removes
  the overlap via the deferred "skip re-install if CRC unchanged" optimization
  (the loader defers to the already-running instance) or by dropping jaguar in a
  custom envelope where the loader is the sole starter. Not worth fixing in the
  smoke test.
*/
main:
  print "loader: awake, polling gateway $GATEWAY-HOST:$GATEWAY-PORT"
  // Never let a transient poll failure strand the device awake: trace it and
  // still deep-sleep so the next wake retries (cf. jaguar.toit:110).
  catch --trace: poll-once
  print "loader: observing payload for $PAYLOAD-OBSERVE"
  sleep PAYLOAD-OBSERVE
  print "loader: deep-sleeping for $POLL-PERIOD"
  esp32.deep-sleep POLL-PERIOD

/**
Pulls "firmware" over TFTP, streams it into flash as the named "payload"
  container, and starts it.

Aborts the install on any failure path so a partially-opened
  `ContainerImageWriter` cannot leak across a wake cycle.
*/
poll-once -> none:
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
    if not installed: sink.abort
