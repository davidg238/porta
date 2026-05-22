import expect show *
import io
import io show LITTLE-ENDIAN
import crypto.crc
import uuid

import .header show HEADER-SIZE
import .blob_sink show BlobInstallWriter ImageInstaller

/**
Records what a $BlobInstallWriter drives, standing in for the device-only
  `ContainerImageWriter`-backed installer so the header-stripping / streaming
  logic can be exercised on the host.
*/
class FakeInstaller implements ImageInstaller:
  begun-size/int := -1
  buf_/io.Buffer := io.Buffer
  committed/bool := false
  aborted/bool := false
  result_/uuid.Uuid := uuid.Uuid.uuid5 "porta-test" "fake-installer"

  begin size/int -> none:
    begun-size = size
  write chunk/ByteArray -> none:
    buf_.write chunk
  commit -> uuid.Uuid:
    committed = true
    return result_
  abort -> none:
    aborted = true

  image -> ByteArray:
    return buf_.bytes

crc-of image/ByteArray -> int:
  summer := crc.Crc.little-endian 32
      --polynomial=0xEDB88320
      --initial-state=0xffff_ffff
      --xor-result=0xffff_ffff
  summer.add image
  return summer.get-as-int

frame image/ByteArray crc32/int -> ByteArray:
  blob := ByteArray (HEADER-SIZE + image.size)
  LITTLE-ENDIAN.put-uint32 blob 0 image.size
  LITTLE-ENDIAN.put-uint32 blob 4 crc32
  blob.replace HEADER-SIZE image
  return blob

/** Feeds $blob to $writer in fixed-size $chunk slices, like TFTP DATA blocks. */
feed writer/BlobInstallWriter blob/ByteArray chunk/int -> none:
  i := 0
  while i < blob.size:
    j := min blob.size (i + chunk)
    writer.write blob[i .. j]
    i = j

main:
  image := ByteArray 1000: it & 0xff
  crc32 := crc-of image
  blob := frame image crc32

  // Happy path with 3-byte chunks: the 8-byte header straddles three writes,
  // and the boundary between header and image lands mid-chunk.
  fake := FakeInstaller
  w := BlobInstallWriter fake
  feed w blob 3
  id := w.commit
  expect-equals image.size fake.begun-size
  expect-equals image fake.image
  expect fake.committed
  expect-not fake.aborted
  expect-equals fake.result_ id

  // Whole blob in a single write.
  fake2 := FakeInstaller
  w2 := BlobInstallWriter fake2
  w2.write blob
  w2.commit
  expect-equals image fake2.image

  // Header split exactly on its 8-byte boundary, then image in 512-byte blocks.
  fake5 := FakeInstaller
  w5 := BlobInstallWriter fake5
  w5.write blob[0 .. HEADER-SIZE]
  feed w5 blob[HEADER-SIZE ..] 512
  w5.commit
  expect-equals image fake5.image

  // Header delivered one byte at a time stresses the accumulation loop.
  fake6 := FakeInstaller
  w6 := BlobInstallWriter fake6
  feed w6 blob 1
  w6.commit
  expect-equals image fake6.image

  // Empty image: header completes, begin 0, no image writes, CRC of empty.
  empty := ByteArray 0
  empty-blob := frame empty (crc-of empty)
  fake7 := FakeInstaller
  w7 := BlobInstallWriter fake7
  feed w7 empty-blob 3
  w7.commit
  expect-equals 0 fake7.begun-size
  expect-equals empty fake7.image
  expect fake7.committed

  // Incomplete header (fewer than 8 bytes): commit throws and, because begin
  // was never called, the installer is NOT aborted.
  fake8 := FakeInstaller
  w8 := BlobInstallWriter fake8
  w8.write blob[0 .. 5]
  expect-throw "blob too short: header incomplete (5 < $HEADER-SIZE)":
    w8.commit
  expect-equals -1 fake8.begun-size
  expect-not fake8.aborted
  expect-not fake8.committed

  // abort passthrough releases the installer after a partial transfer.
  fake9 := FakeInstaller
  w9 := BlobInstallWriter fake9
  feed w9 blob[.. 20] 4  // Header + a few image bytes, then give up.
  w9.abort
  expect fake9.aborted
  expect-not fake9.committed

  // Truncated stream: drop the last 10 image bytes.
  fake3 := FakeInstaller
  w3 := BlobInstallWriter fake3
  feed w3 blob[.. blob.size - 10] 7
  expect-throw "truncated stream: expected 1000 bytes, got 990":
    w3.commit
  expect fake3.aborted
  expect-not fake3.committed

  // CRC mismatch: flip a bit in the advertised CRC.
  bad := frame image (crc32 ^ 0x1)
  fake4 := FakeInstaller
  w4 := BlobInstallWriter fake4
  feed w4 bad 5
  expect-throw "CRC32 mismatch":
    w4.commit
  expect fake4.aborted
  expect-not fake4.committed

  print "blob_sink: all tests passed"
