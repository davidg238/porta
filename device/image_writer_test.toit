// device/image_writer_test.toit
import expect show *
import io
import crypto.crc
import uuid
import .image_writer show ImageStreamWriter ImageInstaller

class FakeInstaller implements ImageInstaller:
  begun-size/int := -1
  buf_/io.Buffer := io.Buffer
  committed/bool := false
  aborted/bool := false
  result_/uuid.Uuid := uuid.Uuid.uuid5 "porta-test" "fake"
  begin size/int -> none: begun-size = size
  write chunk/ByteArray -> none: buf_.write chunk
  commit -> uuid.Uuid: committed = true; return result_
  abort -> none: aborted = true
  image -> ByteArray: return buf_.bytes

crc-of image/ByteArray -> int:
  s := crc.Crc.little-endian 32 --polynomial=0xEDB88320 --initial-state=0xffff_ffff --xor-result=0xffff_ffff
  s.add image
  return s.get-as-int

feed w/ImageStreamWriter bytes/ByteArray chunk/int -> none:
  i := 0
  while i < bytes.size:
    j := min bytes.size (i + chunk)
    w.write bytes[i .. j]
    i = j

main:
  image := ByteArray 1000: it & 0xff
  good := crc-of image

  // happy path: streamed in 128-byte blocks, size+crc verified
  fi := FakeInstaller
  w := ImageStreamWriter fi --size=image.size --crc=good
  expect-equals image.size fi.begun-size   // begin called at construction
  feed w image 128
  id := w.commit
  expect fi.committed
  expect-equals image (fi.image)
  expect-equals fi.result_ id

  // crc mismatch → abort + throw
  fi2 := FakeInstaller
  w2 := ImageStreamWriter fi2 --size=image.size --crc=(good ^ 0x1)
  feed w2 image 128
  expect-throw "CRC32 mismatch": w2.commit
  expect fi2.aborted

  // truncated → abort + throw
  fi3 := FakeInstaller
  w3 := ImageStreamWriter fi3 --size=image.size --crc=good
  feed w3 image[0..500] 128
  expect-throw "truncated stream: expected 1000 bytes, got 500": w3.commit
  expect fi3.aborted
