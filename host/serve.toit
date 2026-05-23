// host/serve.toit — minimal Porta gateway: serves a goal-state + raw image.
import host.directory
import host.file
import log
import tftp show FilesystemStorage TFTPServer
import .goal show build-goal

/** Root directory for the TFTP filesystem storage. */
ROOT ::= "/tmp/porta-tftp"

/** Unprivileged UDP port (69 needs root). The supervisor must match this. */
PORT ::= 6969

/** App name; the node fetches the image under this filename. */
PAYLOAD-NAME ::= "payload"

/** Interval (seconds) advertised in the goal — fast, to observe multi-rate. */
PAYLOAD-INTERVAL-S ::= 5

/**
Serves the captured image as the raw "payload" file plus a "goal" file in the
  Artemis-shaped goal-state format. No blob framing — size+crc ride in the goal.
*/
main:
  image := file.read-contents "image"
  crc32 := int.parse (file.read-contents "image.crc32").to-string.trim
  goal := build-goal --name=PAYLOAD-NAME --size=image.size --crc=crc32 --interval-s=PAYLOAD-INTERVAL-S
  if not file.is-directory ROOT: directory.mkdir --recursive ROOT
  file.write-contents image --path="$ROOT/$PAYLOAD-NAME"
  file.write-contents goal --path="$ROOT/goal"
  print "serving goal ($goal.size B) + $PAYLOAD-NAME (image=$image.size crc32=$crc32) on UDP/$PORT"
  storage := FilesystemStorage --root=ROOT --read-only
  server := TFTPServer --storage=storage --port=PORT --logger=log.default
  server.start
