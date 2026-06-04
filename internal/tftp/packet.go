// Copyright (c) 2026 Ekorau LLC

// Package tftp implements a TFTP packet codec per RFC 1350 / RFC 2348.
package tftp

import (
	"encoding/binary"
	"fmt"
	"strconv"
)

// TFTP opcodes.
const (
	OpRRQ   uint16 = 1
	OpWRQ   uint16 = 2
	OpDATA  uint16 = 3
	OpACK   uint16 = 4
	OpERROR uint16 = 5
	OpOACK  uint16 = 6

	DefaultBlockSize = 512
)

// buildRequest constructs an RRQ or WRQ packet.
func buildRequest(op uint16, path string, blksize int) []byte {
	// opcode(2) + path + \0 + "octet" + \0 [+ "blksize" + \0 + size + \0]
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, op)
	buf = append(buf, []byte(path)...)
	buf = append(buf, 0)
	buf = append(buf, []byte("octet")...)
	buf = append(buf, 0)
	if blksize > 0 {
		buf = append(buf, []byte("blksize")...)
		buf = append(buf, 0)
		buf = append(buf, []byte(strconv.Itoa(blksize))...)
		buf = append(buf, 0)
	}
	return buf
}

// BuildRRQ constructs a TFTP Read Request. If blksize > 0 the blksize option
// is appended (RFC 2348).
func BuildRRQ(path string, blksize int) []byte {
	return buildRequest(OpRRQ, path, blksize)
}

// BuildWRQ constructs a TFTP Write Request. If blksize > 0 the blksize option
// is appended (RFC 2348).
func BuildWRQ(path string, blksize int) []byte {
	return buildRequest(OpWRQ, path, blksize)
}

// BuildData constructs a TFTP DATA packet.
func BuildData(block uint16, data []byte) []byte {
	buf := make([]byte, 4+len(data))
	binary.BigEndian.PutUint16(buf[0:2], OpDATA)
	binary.BigEndian.PutUint16(buf[2:4], block)
	copy(buf[4:], data)
	return buf
}

// BuildACK constructs a 4-byte TFTP ACK packet.
func BuildACK(block uint16) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint16(buf[0:2], OpACK)
	binary.BigEndian.PutUint16(buf[2:4], block)
	return buf
}

// BuildOACK constructs a TFTP Option Acknowledgement packet.
func BuildOACK(opts map[string]string) []byte {
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, OpOACK)
	for k, v := range opts {
		buf = append(buf, []byte(k)...)
		buf = append(buf, 0)
		buf = append(buf, []byte(v)...)
		buf = append(buf, 0)
	}
	return buf
}

// BuildError constructs a TFTP ERROR packet.
func BuildError(code uint16, msg string) []byte {
	buf := make([]byte, 4+len(msg)+1)
	binary.BigEndian.PutUint16(buf[0:2], OpERROR)
	binary.BigEndian.PutUint16(buf[2:4], code)
	copy(buf[4:], []byte(msg))
	buf[len(buf)-1] = 0
	return buf
}

// ParseOpcode extracts the 2-byte opcode from a TFTP packet.
func ParseOpcode(pkt []byte) (uint16, error) {
	if len(pkt) < 2 {
		return 0, fmt.Errorf("tftp: packet too short (%d bytes)", len(pkt))
	}
	return binary.BigEndian.Uint16(pkt[0:2]), nil
}

// ParseRequest extracts the path and options from an RRQ or WRQ packet.
func ParseRequest(pkt []byte) (path string, opts map[string]string, err error) {
	if len(pkt) < 4 {
		return "", nil, fmt.Errorf("tftp: request packet too short")
	}
	// Skip opcode, split remainder on null bytes.
	rest := pkt[2:]
	fields := splitNull(rest)
	if len(fields) < 2 {
		return "", nil, fmt.Errorf("tftp: malformed request")
	}
	path = fields[0]
	// fields[1] is mode ("octet"), skip it
	opts = make(map[string]string)
	for i := 2; i+1 < len(fields); i += 2 {
		opts[fields[i]] = fields[i+1]
	}
	return path, opts, nil
}

// ParseData extracts the block number and payload from a DATA packet.
func ParseData(pkt []byte) (block uint16, data []byte, err error) {
	if len(pkt) < 4 {
		return 0, nil, fmt.Errorf("tftp: data packet too short")
	}
	block = binary.BigEndian.Uint16(pkt[2:4])
	data = pkt[4:]
	return block, data, nil
}

// ParseACK extracts the block number from an ACK packet.
func ParseACK(pkt []byte) (block uint16, err error) {
	if len(pkt) < 4 {
		return 0, fmt.Errorf("tftp: ack packet too short")
	}
	return binary.BigEndian.Uint16(pkt[2:4]), nil
}

// ChunkData splits data into blksize-sized chunks. If the data length is an
// exact multiple of blksize, an empty final chunk is appended (TFTP end-of-
// transfer signal). Empty input returns a single empty chunk.
func ChunkData(data []byte, blksize int) [][]byte {
	if len(data) == 0 {
		return [][]byte{{}}
	}
	var chunks [][]byte
	for i := 0; i < len(data); i += blksize {
		end := i + blksize
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, data[i:end])
	}
	// Exact boundary: append empty chunk to signal end of transfer.
	if len(data)%blksize == 0 {
		chunks = append(chunks, []byte{})
	}
	return chunks
}

// splitNull splits a byte slice on null bytes, returning non-trailing strings.
func splitNull(b []byte) []string {
	var result []string
	start := 0
	for i, c := range b {
		if c == 0 {
			result = append(result, string(b[start:i]))
			start = i + 1
		}
	}
	return result
}
