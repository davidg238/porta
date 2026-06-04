// Copyright (c) 2026 Ekorau LLC

package tftp

import (
	"strconv"
	"sync"
)

// GetHandler returns the data to serve for a GET (RRQ) request.
type GetHandler func() []byte

// PutHandler is called when a PUT (WRQ) transfer completes.
type PutHandler func(path string, data []byte)

// Dispatcher routes parsed TFTP resources (full "base?k=v" strings) to the
// application. It is the porta-side alternative to RegisterGet/RegisterPut.
//
// Callbacks are invoked while the Server holds its internal lock, so an
// implementation must not re-enter the same Server (e.g. call HandlePacket or
// SetDispatcher) from within a callback — doing so deadlocks.
type Dispatcher interface {
	// Read serves bytes for an RRQ. A non-nil error → TFTP ERROR packet;
	// (nil, nil) → a valid empty body (the drain sentinel).
	Read(resource, peer string) ([]byte, error)
	// AcceptWrite gates a WRQ at request time. Non-nil → TFTP ERROR, no transfer.
	AcceptWrite(resource, peer string) error
	// Write ingests a completed WRQ body. Non-nil → TFTP ERROR.
	Write(resource, peer string, data []byte) error
	// Complete is called when a transfer finishes (ok=false on failure).
	Complete(op uint16, resource, peer string, ok bool)
}

// getTransfer tracks an in-progress read (server→client) transfer.
type getTransfer struct {
	chunks      [][]byte
	blockIndex  int // next chunk index to send (0-based)
	blksize     int
	oackPending bool
	resource    string // dispatcher-mode resource key (empty in legacy mode)
	peer        string
}

// putTransfer tracks an in-progress write (client→server) transfer.
type putTransfer struct {
	path     string
	handler  PutHandler
	buf      []byte
	blksize  int
	resource string // dispatcher-mode resource key (empty in legacy mode)
	peer     string
}

// Server dispatches TFTP packets to registered handlers and manages
// transfer state machines.
type Server struct {
	mu          sync.Mutex
	getHandlers map[string]GetHandler
	putHandlers map[string]PutHandler
	gets        map[string]*getTransfer // keyed by path
	puts        map[string]*putTransfer // keyed by path
	dispatcher  Dispatcher
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

// SetDispatcher switches the server to dispatcher mode.
func (s *Server) SetDispatcher(d Dispatcher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dispatcher = d
}

// HandlePacket processes a TFTP packet and returns zero or more response
// packets. It is equivalent to HandlePacketFrom(pkt, "").
func (s *Server) HandlePacket(pkt []byte) [][]byte {
	return s.HandlePacketFrom(pkt, "")
}

// HandlePacketFrom processes a packet, threading the peer address to the
// dispatcher. HandlePacket(pkt) is equivalent to HandlePacketFrom(pkt, "").
func (s *Server) HandlePacketFrom(pkt []byte, peer string) [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	op, err := ParseOpcode(pkt)
	if err != nil {
		return nil
	}

	switch op {
	case OpRRQ:
		if s.dispatcher != nil {
			return s.dispatchRRQ(pkt, peer)
		}
		return s.handleRRQ(pkt)
	case OpWRQ:
		if s.dispatcher != nil {
			return s.dispatchWRQ(pkt, peer)
		}
		return s.handleWRQ(pkt)
	case OpACK:
		return s.handleACK(pkt)
	case OpDATA:
		return s.handleDATA(pkt)
	default:
		return nil
	}
}

func (s *Server) dispatchRRQ(pkt []byte, peer string) [][]byte {
	resource, opts, err := ParseRequest(pkt)
	if err != nil {
		return [][]byte{BuildError(0, "malformed request")}
	}
	data, derr := s.dispatcher.Read(resource, peer)
	if derr != nil {
		return [][]byte{BuildError(1, derr.Error())}
	}
	blksize := DefaultBlockSize
	hasBlksize := false
	if bs, found := opts["blksize"]; found {
		if v, err := strconv.Atoi(bs); err == nil && v > 0 {
			blksize, hasBlksize = v, true
		}
	}
	chunks := ChunkData(data, blksize)
	xfer := &getTransfer{chunks: chunks, blksize: blksize, oackPending: hasBlksize, resource: resource, peer: peer}
	s.gets[resource] = xfer
	if hasBlksize {
		return [][]byte{BuildOACK(map[string]string{"blksize": strconv.Itoa(blksize)})}
	}
	xfer.blockIndex = 1
	return [][]byte{BuildData(1, chunks[0])}
}

func (s *Server) dispatchWRQ(pkt []byte, peer string) [][]byte {
	resource, opts, err := ParseRequest(pkt)
	if err != nil {
		return [][]byte{BuildError(0, "malformed request")}
	}
	if aerr := s.dispatcher.AcceptWrite(resource, peer); aerr != nil {
		return [][]byte{BuildError(2, aerr.Error())} // 2 = access violation
	}
	blksize := DefaultBlockSize
	hasBlksize := false
	if bs, found := opts["blksize"]; found {
		if v, err := strconv.Atoi(bs); err == nil && v > 0 {
			blksize, hasBlksize = v, true
		}
	}
	s.puts[resource] = &putTransfer{resource: resource, peer: peer, blksize: blksize}
	if hasBlksize {
		return [][]byte{BuildOACK(map[string]string{"blksize": strconv.Itoa(blksize)})}
	}
	return [][]byte{BuildACK(0)}
}

// finishGet completes a read transfer, notifying the dispatcher on success.
func (s *Server) finishGet(path string, xfer *getTransfer) [][]byte {
	delete(s.gets, path)
	if s.dispatcher != nil && xfer.resource != "" {
		s.dispatcher.Complete(OpRRQ, xfer.resource, xfer.peer, true)
	}
	return nil
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
				return s.finishGet(path, xfer)
			}
			// Send next block.
			next := xfer.blockIndex // 0-based chunk index for next block
			if next < len(xfer.chunks) {
				xfer.blockIndex++
				return [][]byte{BuildData(uint16(next+1), xfer.chunks[next])}
			}
			return s.finishGet(path, xfer)
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

		if len(data) < xfer.blksize {
			// Final block: ingest and clean up.
			delete(s.puts, path)
			if s.dispatcher != nil && xfer.resource != "" {
				if werr := s.dispatcher.Write(xfer.resource, xfer.peer, xfer.buf); werr != nil {
					s.dispatcher.Complete(OpWRQ, xfer.resource, xfer.peer, false)
					return [][]byte{BuildError(2, werr.Error())}
				}
				s.dispatcher.Complete(OpWRQ, xfer.resource, xfer.peer, true)
				return [][]byte{BuildACK(block)}
			}
			xfer.handler(xfer.path, xfer.buf) // legacy mode
			return [][]byte{BuildACK(block)}
		}

		return [][]byte{BuildACK(block)}
	}
	return nil
}
