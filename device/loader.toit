// porta on-device loader — SCAFFOLD ONLY.
//
// Implement per ../docs/specs/2026-05-21-toit-tftp-loader-design.md (component 3)
// and the plan in ../docs/plans/. Invoke the Toit skills first.
//
// Loop:
//   1. bring up WiFi (STA / IPv4)
//   2. TFTPClient --host=<gateway-ip>; read "firmware" -> Reader
//   3. read first 8 bytes -> size (u32 LE), crc32 (u32 LE)
//   4. hand the same reader to lifted flash-image: install the container
//   5. start the payload container
//   6. esp32.deep-sleep N; on wake, repeat   (M2; M1 omits sleep)
//
// flash-image + the named-install registry are lifted from
// ~/workspaceToit/jaguar/src/{jaguar.toit,container_registry.toit} with jag's
// HTTP-body reader replaced by the tftp_client reader.

main:
  // TODO(porta-agent): implement the poll -> flash-image -> run -> sleep loop.
  throw "not implemented"
