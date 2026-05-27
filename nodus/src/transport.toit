// device/transport.toit
import io
import tftp show TFTPClient

/** Fetches named resources from the gateway over some transport. */
interface GatewayClient:
  /** Reads a small resource fully into memory (e.g. the goal-state). */
  fetch-bytes name/string -> ByteArray
  /** Streams a resource into $to-writer (e.g. an image). Returns bytes read. */
  fetch name/string --to-writer/io.Writer -> int
  /** Writes $bytes to the gateway under resource $name (a WRQ, e.g. the report). */
  put name/string bytes/ByteArray -> none
  close -> none

/**
Brings up a link and yields a $GatewayClient. WiFi is the only transport this
  milestone; ESPnow/bt-mesh implement this same interface later (see the spec's
  transport seam).
*/
interface Transport:
  connect -> GatewayClient

/** WiFi transport: link comes up inside TFTPClient.open (net.open). */
class WifiTransport implements Transport:
  host_/string
  port_/int

  constructor --host/string --port/int:
    host_ = host
    port_ = port

  connect -> GatewayClient:
    client := TFTPClient --host=host_
    client.port = port_   // TFTPClient has no --port ctor arg; set before open.
    client.open
    return TftpGatewayClient client

/** GatewayClient backed by the tftp package's TFTPClient. */
class TftpGatewayClient implements GatewayClient:
  client_/TFTPClient

  constructor .client_:

  fetch-bytes name/string -> ByteArray:
    return client_.read-bytes name

  fetch name/string --to-writer/io.Writer -> int:
    return client_.read name --to-writer=to-writer

  put name/string bytes/ByteArray -> none:
    client_.write-bytes bytes --filename=name

  close -> none:
    client_.close
