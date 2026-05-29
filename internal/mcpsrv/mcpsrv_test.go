package mcpsrv

import (
	"context"
	"testing"

	"github.com/davidg238/porta/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newTestStore opens an in-memory store for tests.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// dialTestClient connects an in-memory MCP client to the server under test.
func dialTestClient(t *testing.T, s *Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.mcp.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestNewServerHasNoToolsYet(t *testing.T) {
	s := New(newTestStore(t))
	cs := dialTestClient(t, s)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(res.Tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(res.Tools))
	}
}
