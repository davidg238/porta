import system.containers
import uuid

import .blob_sink show ImageInstaller

/**
On-device image installer lifted and adapted from jaguar's `flash-image`
  (jaguar/src/jaguar.toit), reshaped as a push-style $ImageInstaller so a
  `BlobInstallWriter` can stream a TFTP transfer straight into flash without
  buffering the whole image in RAM.
*/

/**
Marks a committed image as a named, Jaguar-installed container.

This is the verbatim magic value jaguar writes (`jaguar.toit:238`,
  `container_registry.toit:10`). Committing with `--data=$JAGUAR-INSTALLED-MAGIC`
  makes the device's jaguar firmware discover the image on every boot and
  auto-restart it via `run-installed-containers` — which is how the payload
  survives a deep-sleep wake. "Big cat."
*/
JAGUAR-INSTALLED-MAGIC ::= 0xb16_ca7

/**
An $ImageInstaller backed by a `ContainerImageWriter`.

$begin opens the writer for a known image size, $write streams bytes into it,
  and $commit / $abort finish or discard the install.
*/
class ContainerImageInstaller implements ImageInstaller:
  /**
  Install name. When non-null, $commit tags the image with
    $JAGUAR-INSTALLED-MAGIC so it persists and auto-restarts across a
    deep-sleep wake (named install); when null, the image is transient.
  */
  name_/string?
  writer_/containers.ContainerImageWriter? := null

  constructor .name_:

  begin size/int -> none:
    writer_ = containers.ContainerImageWriter size

  write chunk/ByteArray -> none:
    writer_.write chunk

  commit -> uuid.Uuid:
    // Mirror jaguar.toit:238: a named install is tagged with the magic so
    // run-installed-containers restarts it on boot; transient otherwise.
    data := name_ != null ? JAGUAR-INSTALLED-MAGIC : 0
    result := writer_.commit --data=data
    // Drop the writer so a later abort (e.g. from a caller's finally) is a
    // no-op rather than closing an already-committed writer.
    writer_ = null
    return result

  abort -> none:
    if writer_ != null:
      writer_.close
      writer_ = null
