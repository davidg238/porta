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
An $ImageInstaller backed by a transient `ContainerImageWriter`.

$begin opens the writer for a known image size, $write streams bytes into it,
  and $commit / $abort finish or discard the install.
*/
class ContainerImageInstaller implements ImageInstaller:
  /**
  Install name, kept for the M2 named-install registry. Unused in M1 (transient
    install — no named registry), so callers need not change when M2 wires it up.
  */
  name_/string?
  writer_/containers.ContainerImageWriter? := null

  constructor .name_:

  begin size/int -> none:
    writer_ = containers.ContainerImageWriter size

  write chunk/ByteArray -> none:
    writer_.write chunk

  commit -> uuid.Uuid:
    result := writer_.commit
    // Drop the writer so a later abort (e.g. from a caller's finally) is a
    // no-op rather than closing an already-committed writer.
    writer_ = null
    return result

  abort -> none:
    if writer_ != null:
      writer_.close
      writer_ = null
