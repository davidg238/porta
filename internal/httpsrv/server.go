// internal/httpsrv/server.go
package httpsrv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/davidg238/porta/internal/store"
)

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
	http *http.Server
	Mux  *http.ServeMux
}

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
	mux.Handle("/health", healthHandler(st))
	return &Server{
		http: &http.Server{
			Addr:    fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port),
			Handler: AllowlistMiddleware(nets)(mux),
		},
		Mux: mux,
	}, nil
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
