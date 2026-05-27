// device/flash_image.toit
import system.containers
import uuid

import .image_writer show ImageInstaller

/**
On-device image installer adapted from jaguar's flash-image, reshaped as a
  push-style $ImageInstaller so an ImageStreamWriter streams a TFTP transfer
  straight into flash without buffering the whole image.

Commits with `--run-boot=false`: the supervisor — not the firmware — owns
  starting containers (no JAGUAR-INSTALLED-MAGIC, no auto-restart on boot). The
  committed image still persists in the flash registry across power-cycles.
*/
class ContainerImageInstaller implements ImageInstaller:
  writer_/containers.ContainerImageWriter? := null

  begin size/int -> none:
    writer_ = containers.ContainerImageWriter size

  write chunk/ByteArray -> none:
    writer_.write chunk

  commit -> uuid.Uuid:
    result := writer_.commit --run-boot=false
    writer_ = null  // later abort (e.g. from a finally) becomes a no-op
    return result

  abort -> none:
    if writer_ != null:
      writer_.close
      writer_ = null
