// Copyright (c) 2026 Ekorau LLC

// internal/httpsrv/server.go
package httpsrv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/davidg238/porta/internal/serverstat"
	"github.com/davidg238/porta/internal/store"
)

// defaultReadHeaderTimeout bounds how long the server waits for a client to
// send complete request headers, closing the connection otherwise. Defeats
// the Slowloris class (trickle-a-byte connection exhaustion). ReadTimeout/
// WriteTimeout are deliberately left unset so B4c file uploads and SSE
// streams can run long; ReadHeaderTimeout alone closes the slow-header hole.
const defaultReadHeaderTimeout = 5 * time.Second

// Config holds the operator HTTP listener configuration. Bind+Port match
// the cobra --http-bind/--http-port flags; AllowCIDR mirrors the
// --http-allow-cidr flag (caller passes the raw strings; New runs
// AllowCIDR to parse).
type Config struct {
	Bind      string   // e.g. "0.0.0.0" or "127.0.0.1"
	Port      int      // 0 means disabled (caller should not construct a Server)
	AllowCIDR []string // empty = serve any peer (no allowlist)
}

// Server wraps the http.Server with porta-specific plumbing. Mux is the
// shared mux that consumer packages register routes on; the allowlist
// middleware wraps every route uniformly via the http.Server.Handler.
type Server struct {
	http  *http.Server
	Mux   *http.ServeMux
	stats *serverstat.Stats // optional; enriches /health when set
}

// SetStats attaches the process stats holder so /health can report version,
// uptime, and the report-reject count. Call before Run.
func (s *Server) SetStats(st *serverstat.Stats) { s.stats = st }

// New constructs a Server with the allowlist middleware applied to a
// fresh mux. /health is pre-registered; consumer packages (mcp, web)
// register additional routes via s.Mux.Handle. Returns an error if
// AllowCIDR fails to parse any entry.
func New(cfg Config, st *store.Store) (*Server, error) {
	nets, err := AllowCIDR(cfg.AllowCIDR)
	if err != nil {
		return nil, fmt.Errorf("httpsrv: parse allow-cidr: %w", err)
	}
	mux := http.NewServeMux()
	s := &Server{
		http: &http.Server{
			// JoinHostPort brackets bare IPv6 binds ("::" → "[::]:6970");
			// fmt.Sprintf("%s:%d") would yield the invalid ":::6970".
			Addr:              net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.Port)),
			Handler:           AllowlistMiddleware(nets)(mux),
			ReadHeaderTimeout: defaultReadHeaderTimeout,
		},
		Mux: mux,
	}
	mux.Handle("/health", healthHandler(st, func() *serverstat.Stats { return s.stats }))
	return s, nil
}

// Run starts the listener and blocks until ctx is cancelled or the
// listener errors. On ctx cancel, performs a graceful Shutdown with a
// 5-second timeout. Returns nil for clean shutdown (ctx cancel or
// http.ErrServerClosed), non-nil for a real listener failure.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() { errCh <- s.http.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.http.Shutdown(shutCtx)
		// Drain the listener error channel; ListenAndServe returns
		// ErrServerClosed once Shutdown completes.
		<-errCh
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
