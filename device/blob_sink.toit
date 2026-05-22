import crypto.crc
import io
import uuid

import .header show HEADER-SIZE parse-header

/**
Streaming reassembly of a porta firmware blob arriving over TFTP.

A $BlobInstallWriter is fed the blob as a stream of byte chunks (one TFTP DATA
  block at a time). It peels the 8-byte self-describing header off the front,
  hands the declared image size to an $ImageInstaller, streams every subsequent
  byte into that installer while computing CRC32-IEEE, and on $BlobInstallWriter.commit
  verifies size and CRC before committing. Live memory is bounded to the 8-byte
  header plus one block — the whole image is never buffered.
*/

/**
Sink for the image bytes carried by a porta firmware blob.

$BlobInstallWriter drives an installer in this order: $begin once (with the
  image size from the header), $write zero or more times (image bytes, in
  order), then exactly one of $commit (size and CRC verified) or $abort (size
  or CRC check failed). Implementations back this with the device's
  `ContainerImageWriter`; the test backs it with an in-memory recorder.
*/
interface ImageInstaller:
  /** Begins an install of $size image bytes. Called once, before any $write. */
  begin size/int -> none
  /** Writes the next $chunk of image bytes, in order. */
  write chunk/ByteArray -> none
  /** Commits the install and returns the image's UUID. */
  commit -> uuid.Uuid
  /** Aborts the install, releasing any resources without committing. */
  abort -> none

/**
An $io.Writer that reassembles a porta firmware blob into an $ImageInstaller.

Construct with the target $ImageInstaller, feed the blob via the inherited
  $io.Writer.write (the TFTP client does this per DATA block), then call $commit
  once the transfer is complete.
*/
class BlobInstallWriter extends io.Writer:
  installer_/ImageInstaller
  header-buf_/ByteArray := ByteArray HEADER-SIZE
  header-filled_/int := 0
  expected-size_/int := 0
  expected-crc32_/int := 0
  written_/int := 0
  summer_/crc.Crc := crc.Crc.little-endian 32
      --polynomial=0xEDB88320
      --initial-state=0xffff_ffff
      --xor-result=0xffff_ffff

  constructor .installer_:

  /**
  Consumes `data[from..to]`, splitting it across the header and image phases.

  Always reports the whole range as written: the installer's write is blocking
    and accepts everything it is given.
  */
  try-write_ data/io.Data from/int to/int -> int:
    pos := from
    if header-filled_ < HEADER-SIZE:
      take := min (HEADER-SIZE - header-filled_) (to - pos)
      header-buf_.replace header-filled_ data pos (pos + take)
      header-filled_ += take
      pos += take
      if header-filled_ == HEADER-SIZE:
        header := parse-header header-buf_
        expected-size_ = header.size
        expected-crc32_ = header.crc32
        installer_.begin header.size
    if pos < to and header-filled_ == HEADER-SIZE:
      // Copy the image bytes out of data: ContainerImageWriter.write neuters its
      // argument, so the chunk must not alias data the TFTP layer still owns.
      chunk := ByteArray (to - pos)
      chunk.replace 0 data pos to
      summer_.add chunk
      written_ += chunk.size
      installer_.write chunk
    return to - from

  /**
  Verifies the completed transfer and commits the image, returning its UUID.

  Throws "blob too short: ..." if fewer than 8 header bytes arrived,
    "truncated stream: ..." if fewer than the declared image bytes arrived, or
    "CRC32 mismatch" if the computed digest differs from the header's. On the
    latter two paths — i.e. once the install has begun — the installer is
    aborted before throwing. (If the header never completed, the installer was
    never begun and there is nothing to abort.)
  */
  commit -> uuid.Uuid:
    if header-filled_ < HEADER-SIZE:
      throw "blob too short: header incomplete ($header-filled_ < $HEADER-SIZE)"
    if written_ != expected-size_:
      installer_.abort
      throw "truncated stream: expected $expected-size_ bytes, got $written_"
    if summer_.get-as-int != expected-crc32_:
      installer_.abort
      throw "CRC32 mismatch"
    return installer_.commit

  /**
  Aborts the install, releasing any installer resources without committing.

  A no-op if the install never began (the header never completed) or already
    finished. Safe to call from a caller's error path.
  */
  abort -> none:
    installer_.abort
