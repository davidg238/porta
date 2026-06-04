// Copyright (c) 2026 Ekorau LLC

package tftp

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestBuildRRQ_NoBlksize(t *testing.T) {
	pkt := BuildRRQ("firmware.bin", 0)
	// opcode 1, path\0, "octet"\0
	if binary.BigEndian.Uint16(pkt[0:2]) != OpRRQ {
		t.Fatalf("expected opcode %d, got %d", OpRRQ, binary.BigEndian.Uint16(pkt[0:2]))
	}
	rest := pkt[2:]
	parts := bytes.Split(rest, []byte{0})
	// path, mode, trailing empty
	if string(parts[0]) != "firmware.bin" {
		t.Fatalf("expected path firmware.bin, got %q", parts[0])
	}
	if string(parts[1]) != "octet" {
		t.Fatalf("expected mode octet, got %q", parts[1])
	}
	// No blksize option fields
	if len(parts) > 3 {
		t.Fatalf("expected no options, got extra parts: %v", parts[2:])
	}
}

func TestBuildRRQ_WithBlksize(t *testing.T) {
	pkt := BuildRRQ("test.st", 64)
	rest := pkt[2:]
	parts := bytes.Split(rest, []byte{0})
	// path, mode, "blksize", "64", trailing empty
	if string(parts[0]) != "test.st" {
		t.Fatalf("path: got %q", parts[0])
	}
	if string(parts[1]) != "octet" {
		t.Fatalf("mode: got %q", parts[1])
	}
	if string(parts[2]) != "blksize" {
		t.Fatalf("expected blksize key, got %q", parts[2])
	}
	if string(parts[3]) != "64" {
		t.Fatalf("expected blksize value 64, got %q", parts[3])
	}
}

func TestBuildWRQ(t *testing.T) {
	pkt := BuildWRQ("upload.bin", 64)
	if binary.BigEndian.Uint16(pkt[0:2]) != OpWRQ {
		t.Fatalf("expected opcode %d, got %d", OpWRQ, binary.BigEndian.Uint16(pkt[0:2]))
	}
	rest := pkt[2:]
	parts := bytes.Split(rest, []byte{0})
	if string(parts[0]) != "upload.bin" {
		t.Fatalf("path: got %q", parts[0])
	}
	if string(parts[1]) != "octet" {
		t.Fatalf("mode: got %q", parts[1])
	}
	if string(parts[2]) != "blksize" {
		t.Fatalf("expected blksize key, got %q", parts[2])
	}
	if string(parts[3]) != "64" {
		t.Fatalf("expected blksize value 64, got %q", parts[3])
	}
}

func TestBuildData(t *testing.T) {
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	pkt := BuildData(7, payload)
	if binary.BigEndian.Uint16(pkt[0:2]) != OpDATA {
		t.Fatal("wrong opcode")
	}
	if binary.BigEndian.Uint16(pkt[2:4]) != 7 {
		t.Fatal("wrong block number")
	}
	if !bytes.Equal(pkt[4:], payload) {
		t.Fatal("wrong payload")
	}
}

func TestBuildACK(t *testing.T) {
	pkt := BuildACK(42)
	if len(pkt) != 4 {
		t.Fatalf("ACK must be 4 bytes, got %d", len(pkt))
	}
	if binary.BigEndian.Uint16(pkt[0:2]) != OpACK {
		t.Fatal("wrong opcode")
	}
	if binary.BigEndian.Uint16(pkt[2:4]) != 42 {
		t.Fatal("wrong block number")
	}
}

func TestBuildOACK(t *testing.T) {
	opts := map[string]string{"blksize": "64"}
	pkt := BuildOACK(opts)
	if binary.BigEndian.Uint16(pkt[0:2]) != OpOACK {
		t.Fatal("wrong opcode")
	}
	rest := pkt[2:]
	parts := bytes.Split(rest, []byte{0})
	// key, value, trailing empty
	if string(parts[0]) != "blksize" {
		t.Fatalf("expected blksize key, got %q", parts[0])
	}
	if string(parts[1]) != "64" {
		t.Fatalf("expected 64, got %q", parts[1])
	}
}

func TestBuildError(t *testing.T) {
	pkt := BuildError(1, "File not found")
	if binary.BigEndian.Uint16(pkt[0:2]) != OpERROR {
		t.Fatal("wrong opcode")
	}
	if binary.BigEndian.Uint16(pkt[2:4]) != 1 {
		t.Fatal("wrong error code")
	}
	msg := pkt[4 : len(pkt)-1] // strip trailing null
	if string(msg) != "File not found" {
		t.Fatalf("wrong message: %q", msg)
	}
	if pkt[len(pkt)-1] != 0 {
		t.Fatal("missing trailing null")
	}
}

func TestParseOpcode_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		pkt  []byte
		want uint16
	}{
		{"RRQ", BuildRRQ("x", 0), OpRRQ},
		{"WRQ", BuildWRQ("x", 0), OpWRQ},
		{"DATA", BuildData(1, []byte{1}), OpDATA},
		{"ACK", BuildACK(0), OpACK},
		{"OACK", BuildOACK(map[string]string{"blksize": "64"}), OpOACK},
		{"ERROR", BuildError(0, "err"), OpERROR},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseOpcode(tc.pkt)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, got)
			}
		})
	}
}

func TestParseOpcode_TooShort(t *testing.T) {
	_, err := ParseOpcode([]byte{0})
	if err == nil {
		t.Fatal("expected error for short packet")
	}
}

func TestParseRequest(t *testing.T) {
	pkt := BuildRRQ("hello.st", 64)
	path, opts, err := ParseRequest(pkt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "hello.st" {
		t.Fatalf("path: got %q", path)
	}
	if opts["blksize"] != "64" {
		t.Fatalf("blksize: got %q", opts["blksize"])
	}
}

func TestParseRequest_NoOptions(t *testing.T) {
	pkt := BuildRRQ("foo.bin", 0)
	path, opts, err := ParseRequest(pkt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "foo.bin" {
		t.Fatalf("path: got %q", path)
	}
	if len(opts) != 0 {
		t.Fatalf("expected no options, got %v", opts)
	}
}

func TestParseData(t *testing.T) {
	payload := []byte{1, 2, 3, 4, 5}
	pkt := BuildData(99, payload)
	block, data, err := ParseData(pkt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if block != 99 {
		t.Fatalf("block: got %d", block)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("data mismatch")
	}
}

func TestParseACK(t *testing.T) {
	pkt := BuildACK(255)
	block, err := ParseACK(pkt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if block != 255 {
		t.Fatal("wrong block")
	}
}

func TestChunkData_Normal(t *testing.T) {
	data := make([]byte, 150)
	for i := range data {
		data[i] = byte(i)
	}
	chunks := ChunkData(data, 64)
	// 150 / 64 = 2 full + 1 partial (22 bytes)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if len(chunks[0]) != 64 {
		t.Fatalf("chunk 0 len: %d", len(chunks[0]))
	}
	if len(chunks[1]) != 64 {
		t.Fatalf("chunk 1 len: %d", len(chunks[1]))
	}
	if len(chunks[2]) != 22 {
		t.Fatalf("chunk 2 len: %d", len(chunks[2]))
	}
}

func TestChunkData_ExactBoundary(t *testing.T) {
	data := make([]byte, 128)
	chunks := ChunkData(data, 64)
	// Exact boundary: 2 full chunks + 1 empty final chunk
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks (exact boundary), got %d", len(chunks))
	}
	if len(chunks[0]) != 64 {
		t.Fatalf("chunk 0 len: %d", len(chunks[0]))
	}
	if len(chunks[1]) != 64 {
		t.Fatalf("chunk 1 len: %d", len(chunks[1]))
	}
	if len(chunks[2]) != 0 {
		t.Fatalf("expected empty final chunk, got len %d", len(chunks[2]))
	}
}

func TestChunkData_Empty(t *testing.T) {
	chunks := ChunkData([]byte{}, 64)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for empty data, got %d", len(chunks))
	}
	if len(chunks[0]) != 0 {
		t.Fatalf("expected empty chunk, got len %d", len(chunks[0]))
	}
}
