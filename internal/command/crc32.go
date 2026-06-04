// Copyright (c) 2026 Ekorau LLC

// Package command defines the porta command vocabulary, its wire codec, and
// payload helpers shared by the gateway control plane.
package command

import "hash/crc32"

// CRC32 computes the CRC32-IEEE of data, byte-identical to the protocol's
// image checksum (and to jag's X-Jaguar-CRC32). Go's stdlib IEEE table uses
// the reversed polynomial 0xEDB88320 with the standard init/xor, matching the
// Toit gateway's crc32.toit.
func CRC32(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}
