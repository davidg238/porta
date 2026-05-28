package tftp

import (
	"strconv"
	"sync"
)

// GetHandler returns the data to serve for a GET (RRQ) request.
type GetHandler func() []byte

// PutHandler is called when a PUT (WRQ) transfer completes.
type PutHandler func(path string, data []byte)

// getTransfer tracks an in-progress read (server→client) transfer.
type getTransfer struct {
	chunks      [][]byte
	blockIndex  int // next chunk index to send (0-based)
	blksize     int
	oackPending bool
}

// putTransfer tracks an in-progress write (client→server) transfer.
type putTransfer struct {
	path    string
	handler PutHandler
	buf     []byte
	blksize int
}

// Server dispatches TFTP packets to registered handlers and manages
// transfer state machines.
type Server struct {
	mu          sync.Mutex
	getHandlers map[string]GetHandler
	putHandlers map[string]PutHandler
	gets        map[string]*getTransfer // keyed by path
	puts        map[string]*putTransfer // keyed by path
}

// NewServer creates a TFTP server with empty handler registrations.
func NewServer() *Server {
	return &Server{
		getHandlers: make(map[string]GetHandler),
		putHandlers: make(map[string]PutHandler),
		gets:        make(map[string]*getTransfer),
		puts:        make(map[string]*putTransfer),
	}
}

// RegisterGet registers a handler for the given path that provides data
// when a client sends an RRQ.
func (s *Server) RegisterGet(path string, handler GetHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getHandlers[path] = handler
}

// RegisterPut registers a handler for the given path that receives data
// when a client completes a WRQ transfer.
func (s *Server) RegisterPut(path string, handler PutHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putHandlers[path] = handler
}

// HandlePacket processes a TFTP packet and returns zero or more response
// packets.
func (s *Server) HandlePacket(pkt []byte) [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	op, err := ParseOpcode(pkt)
	if err != nil {
		return nil
	}

	switch op {
	case OpRRQ:
		return s.handleRRQ(pkt)
	case OpWRQ:
		return s.handleWRQ(pkt)
	case OpACK:
		return s.handleACK(pkt)
	case OpDATA:
		return s.handleDATA(pkt)
	default:
		return nil
	}
}

func (s *Server) handleRRQ(pkt []byte) [][]byte {
	path, opts, err := ParseRequest(pkt)
	if err != nil {
		return [][]byte{BuildError(0, "malformed request")}
	}

	handler, ok := s.getHandlers[path]
	if !ok {
		return [][]byte{BuildError(1, "file not found")}
	}

	data := handler()
	blksize := DefaultBlockSize
	hasBlksize := false
	if bs, found := opts["blksize"]; found {
		if v, err := strconv.Atoi(bs); err == nil && v > 0 {
			blksize = v
			hasBlksize = true
		}
	}

	chunks := ChunkData(data, blksize)
	xfer := &getTransfer{
		chunks:      chunks,
		blockIndex:  0,
		blksize:     blksize,
		oackPending: hasBlksize,
	}
	s.gets[path] = xfer

	if hasBlksize {
		return [][]byte{BuildOACK(map[string]string{"blksize": strconv.Itoa(blksize)})}
	}
	// No blksize negotiation: send first DATA block immediately.
	xfer.blockIndex = 1
	return [][]byte{BuildData(1, chunks[0])}
}

func (s *Server) handleWRQ(pkt []byte) [][]byte {
	path, opts, err := ParseRequest(pkt)
	if err != nil {
		return [][]byte{BuildError(0, "malformed request")}
	}

	handler, ok := s.putHandlers[path]
	if !ok {
		return [][]byte{BuildError(1, "file not found")}
	}

	blksize := DefaultBlockSize
	hasBlksize := false
	if bs, found := opts["blksize"]; found {
		if v, err := strconv.Atoi(bs); err == nil && v > 0 {
			blksize = v
			hasBlksize = true
		}
	}

	s.puts[path] = &putTransfer{
		path:    path,
		handler: handler,
		blksize: blksize,
	}

	if hasBlksize {
		return [][]byte{BuildOACK(map[string]string{"blksize": strconv.Itoa(blksize)})}
	}
	return [][]byte{BuildACK(0)}
}

func (s *Server) handleACK(pkt []byte) [][]byte {
	block, err := ParseACK(pkt)
	if err != nil {
		return nil
	}

	// Find the active get transfer. With single-device polling, there is
	// at most one active get transfer.
	for path, xfer := range s.gets {
		if xfer.oackPending && block == 0 {
			// Client acknowledged the OACK, send first data block.
			xfer.oackPending = false
			xfer.blockIndex = 1
			return [][]byte{BuildData(1, xfer.chunks[0])}
		}

		if int(block) == xfer.blockIndex {
			// Check if transfer is complete (last block was short or empty).
			if xfer.blockIndex >= len(xfer.chunks) {
				delete(s.gets, path)
				return nil
			}
			// Send next block.
			next := xfer.blockIndex // 0-based chunk index for next block
			if next < len(xfer.chunks) {
				xfer.blockIndex++
				return [][]byte{BuildData(uint16(next+1), xfer.chunks[next])}
			}
			delete(s.gets, path)
			return nil
		}
	}
	return nil
}

func (s *Server) handleDATA(pkt []byte) [][]byte {
	block, data, err := ParseData(pkt)
	if err != nil {
		return nil
	}

	// Find the active put transfer.
	for path, xfer := range s.puts {
		xfer.buf = append(xfer.buf, data...)
		ack := BuildACK(block)

		if len(data) < xfer.blksize {
			// Final block: call the handler and clean up.
			xfer.handler(xfer.path, xfer.buf)
			delete(s.puts, path)
		}

		return [][]byte{ack}
	}
	return nil
}
