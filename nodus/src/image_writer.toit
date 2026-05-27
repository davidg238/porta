// device/image_writer.toit
import crypto.crc
import io
import uuid

/**
Sink for image bytes: begin once (with size), write in order, then exactly one
  of commit / abort. Backed on-device by a ContainerImageWriter; backed in tests
  by an in-memory recorder.
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
An io.Writer that streams a raw container image into an $ImageInstaller and
  verifies length + CRC32-IEEE on $commit. Size and CRC come from the goal (the
  former self-describing 8-byte blob header is gone — metadata rides in the
  command now). Live memory is bounded to one block.
*/
class ImageStreamWriter extends io.Writer:
  installer_/ImageInstaller
  expected-size_/int
  expected-crc_/int
  written_/int := 0
  summer_/crc.Crc := crc.Crc.little-endian 32
      --polynomial=0xEDB88320
      --initial-state=0xffff_ffff
      --xor-result=0xffff_ffff

  constructor .installer_ --size/int --crc/int:
    expected-size_ = size
    expected-crc_ = crc
    installer_.begin size

  try-write_ data/io.Data from/int to/int -> int:
    // Copy out: ContainerImageWriter.write neuters its argument, so the chunk
    // must not alias data the TFTP layer still owns.
    chunk := ByteArray (to - from)
    chunk.replace 0 data from to
    summer_.add chunk
    written_ += chunk.size
    installer_.write chunk
    return to - from

  commit -> uuid.Uuid:
    if written_ != expected-size_:
      installer_.abort
      throw "truncated stream: expected $expected-size_ bytes, got $written_"
    if summer_.get-as-int != expected-crc_:
      installer_.abort
      throw "CRC32 mismatch"
    return installer_.commit

  abort -> none:
    installer_.abort
