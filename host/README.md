# host/ — throwaway smoke-test harness

Two small, disposable tools. Implement per
`../docs/specs/2026-05-21-toit-tftp-loader-design.md` (components 1 & 2) and the
plan in `../docs/plans/`.

## 1. Capture sink (one-time, ~40 lines, Go or Python)
- Accept `PUT /run` and `PUT /install`.
- Save the request body to `image` and the `X-Jaguar-CRC32` header value.
- Run `jag run ../device/hello.toit` (or `jag container install`) targeted at
  this sink instead of a real device → a guaranteed-correct, SDK-matched image.

## 2. TFTP harness (host Toit, uses ~/workspaceToit/tftp/src/tftp_server.toit)
- Serve one file `"firmware"` = `[u32 size_le][u32 crc32_le][image bytes]`.
  - `size`  = length of the captured image bytes
  - `crc32` = the captured `X-Jaguar-CRC32` value, reused verbatim
- Raise `--blksize` (e.g. 1024); the image is tens of KB.

> `image` / `firmware` are regenerated artifacts and are gitignored.
