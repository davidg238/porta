// host/serve.toit — serves the captured image as the framed "firmware" blob.
import host.directory
import host.file
import log
import tftp show FilesystemStorage TFTPServer
import .blob

/** Root directory for the TFTP filesystem storage. */
ROOT ::= "/tmp/porta-tftp"

/**
UDP port on which the TFTP server listens.

Port 69 requires root or CAP_NET_BIND_SERVICE on Linux; 6969 is unprivileged.
The on-device loader must target the same port.
*/
PORT ::= 6969

/**
Frames the captured image blob and serves it over TFTP.

Reads the `image` and `image.crc32` files from the current directory, writes
  the framed blob to $ROOT/firmware, then blocks serving TFTP read requests
  on UDP/$PORT.
*/
main:
  image := file.read-contents "image"
  crc32 := int.parse (file.read-contents "image.crc32").to-string.trim
  blob := frame-blob image crc32
  if not file.is-directory ROOT: directory.mkdir --recursive ROOT
  file.write-contents blob --path="$ROOT/firmware"
  print "serving firmware: image=$image.size crc32=$crc32 blob=$blob.size on UDP/$PORT"
  storage := FilesystemStorage --root=ROOT --read-only
  server := TFTPServer --storage=storage --port=PORT --logger=log.default
  server.start
