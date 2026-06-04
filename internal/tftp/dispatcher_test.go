// Copyright (c) 2026 Ekorau LLC

package tftp

import (
	"errors"
	"testing"
)

// fakeDispatcher records calls and serves canned responses.
type fakeDispatcher struct {
	readData  []byte
	readErr   error
	writeErr  error
	acceptErr error
	completed []string // "op:resource:ok"
	wrote     []byte
}

func (f *fakeDispatcher) Read(resource, peer string) ([]byte, error) { return f.readData, f.readErr }
func (f *fakeDispatcher) AcceptWrite(resource, peer string) error    { return f.acceptErr }
func (f *fakeDispatcher) Write(resource, peer string, data []byte) error {
	f.wrote = data
	return f.writeErr
}
func (f *fakeDispatcher) Complete(op uint16, resource, peer string, ok bool) {
	tag := "rrq"
	if op == OpWRQ {
		tag = "wrq"
	}
	res := tag + ":" + resource + ":"
	if ok {
		res += "ok"
	} else {
		res += "fail"
	}
	f.completed = append(f.completed, res)
}

// drive feeds an RRQ (no blksize) then ACKs each DATA block to completion,
// returning the concatenated served bytes.
func driveRRQ(s *Server, resource, peer string) (data []byte, sawError bool) {
	replies := s.HandlePacketFrom(BuildRRQ(resource, 0), peer)
	for len(replies) > 0 {
		pkt := replies[0]
		op, _ := ParseOpcode(pkt)
		switch op {
		case OpERROR:
			return data, true
		case OpDATA:
			block, chunk, _ := ParseData(pkt)
			data = append(data, chunk...)
			replies = s.HandlePacketFrom(BuildACK(block), peer)
		default:
			return data, false
		}
	}
	return data, false
}

func TestDispatcherReadServesCommand(t *testing.T) {
	d := &fakeDispatcher{readData: []byte(`{"verb":"run"}`)}
	s := NewServer()
	s.SetDispatcher(d)
	data, sawErr := driveRRQ(s, "commands?id=abc", "1.2.3.4:5")
	if sawErr {
		t.Fatal("unexpected ERROR")
	}
	if string(data) != `{"verb":"run"}` {
		t.Errorf("served %q", data)
	}
	if len(d.completed) != 1 || d.completed[0] != "rrq:commands?id=abc:ok" {
		t.Errorf("completed = %v", d.completed)
	}
}

func TestDispatcherDrainIsEmptySuccessNotError(t *testing.T) {
	d := &fakeDispatcher{readData: nil, readErr: nil} // empty queue sentinel
	s := NewServer()
	s.SetDispatcher(d)
	data, sawErr := driveRRQ(s, "commands?id=abc", "p")
	if sawErr {
		t.Fatal("drain must be empty SUCCESS, not ERROR")
	}
	if len(data) != 0 {
		t.Errorf("drain body = %q, want empty", data)
	}
}

func TestDispatcherReadErrorIsTFTPError(t *testing.T) {
	d := &fakeDispatcher{readErr: errors.New("boom")}
	s := NewServer()
	s.SetDispatcher(d)
	_, sawErr := driveRRQ(s, "commands?id=abc", "p")
	if !sawErr {
		t.Fatal("read error must produce a TFTP ERROR packet")
	}
}

func TestDispatcherWriteIngestsAndCompletes(t *testing.T) {
	d := &fakeDispatcher{}
	s := NewServer()
	s.SetDispatcher(d)
	replies := s.HandlePacketFrom(BuildWRQ("report?id=abc", 0), "p")
	if op, _ := ParseOpcode(replies[0]); op != OpACK {
		t.Fatalf("WRQ reply op = %d, want ACK", op)
	}
	replies = s.HandlePacketFrom(BuildData(1, []byte(`{"apps":{}}`)), "p")
	if op, _ := ParseOpcode(replies[0]); op != OpACK {
		t.Fatalf("DATA reply op = %d, want ACK", op)
	}
	if string(d.wrote) != `{"apps":{}}` {
		t.Errorf("wrote %q", d.wrote)
	}
	if len(d.completed) != 1 || d.completed[0] != "wrq:report?id=abc:ok" {
		t.Errorf("completed = %v", d.completed)
	}
}

func TestDispatcherAcceptWriteRejection(t *testing.T) {
	d := &fakeDispatcher{acceptErr: errors.New("access denied")}
	s := NewServer()
	s.SetDispatcher(d)
	replies := s.HandlePacketFrom(BuildWRQ("data?id=abc", 0), "p")
	if op, _ := ParseOpcode(replies[0]); op != OpERROR {
		t.Fatalf("rejected WRQ op = %d, want ERROR", op)
	}
}
