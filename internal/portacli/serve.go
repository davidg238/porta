// Copyright (c) 2026 Ekorau LLC

// internal/portacli/serve.go
package portacli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"

	"github.com/davidg238/porta/internal/apisrv"
	"github.com/davidg238/porta/internal/handler"
	"github.com/davidg238/porta/internal/httpsrv"
	"github.com/davidg238/porta/internal/mcpsrv"
	"github.com/davidg238/porta/internal/tftp"
	"github.com/davidg238/porta/internal/web"
	"github.com/spf13/cobra"
)

// defaultAllowCIDR returns the RFC1918 + loopback + Tailscale set, as a fresh
// slice per invocation (cobra StringSliceVar shares the backing slice across
// resets — see TestDefaultAllowCIDRReturnsFreshSlice).
//
// 100.64.0.0/10 is the CGNAT range Tailscale assigns to tailnet peers, so the
// operator surface is reachable from the operator's phone/laptop over Tailscale
// out of the box (e.g. while away from the office LAN).
func defaultAllowCIDR() []string {
	return []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"100.64.0.0/10",
		"127.0.0.0/8",
		"::1/128",
	}
}

func newServeCmd() *cobra.Command {
	var port, httpPort int
	var httpBind string
	var httpAllowCIDR []string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the porta gateway: UDP/TFTP listener + optional HTTP operator surface",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()

			// UDP listener (preserves the existing serveUDPLoop; ctx
			// cancels via conn.Close from a sidekick goroutine, and
			// net.ErrClosed from ReadFrom = clean shutdown).
			udpAddr := fmt.Sprintf(":%d", port)
			udpConn, err := net.ListenPacket("udp", udpAddr)
			if err != nil {
				return err
			}
			defer udpConn.Close()
			tftpSrv := tftp.NewServer()
			tftpSrv.SetDispatcher(handler.New(st, nowSec))
			log.Printf("porta: serving TFTP on udp %s (db=%s)", udpAddr, dbPath)
			udpErr := make(chan error, 1)
			go func() { udpErr <- serveUDPLoop(ctx, udpConn, tftpSrv) }()

			// HTTP listener (B4a, optional). When --http-port 0, httpErr
			// stays nil so the select arm blocks forever — UDP keeps
			// serving until ctx cancel. (A closed channel would fire
			// the select immediately and exit the serve, which is the
			// bug found in T5 review.)
			var httpErr chan error
			if httpPort > 0 {
				srv, err := httpsrv.New(httpsrv.Config{
					Bind:      httpBind,
					Port:      httpPort,
					AllowCIDR: httpAllowCIDR,
				}, st)
				if err != nil {
					return err
				}
				web.New(st).Register(srv.Mux)
				mcpsrv.New(st).Register(srv.Mux)
				apisrv.New(st).Register(srv.Mux)
				httpErr = make(chan error, 1)
				go func() { httpErr <- srv.Run(ctx) }()
				log.Printf("porta: serving HTTP on %s:%d", httpBind, httpPort)
			}

			return awaitServeExit(ctx, udpErr, httpErr)
		},
	}
	cmd.Flags().IntVar(&port, "port", 6969, "UDP/TFTP port")
	cmd.Flags().IntVar(&httpPort, "http-port", 6970, "operator HTTP port (0 = disabled)")
	cmd.Flags().StringVar(&httpBind, "http-bind", "0.0.0.0", "operator HTTP bind address")
	cmd.Flags().StringSliceVar(&httpAllowCIDR, "http-allow-cidr",
		defaultAllowCIDR(),
		"CIDR ranges allowed to reach the operator HTTP listener (repeatable; empty = serve any peer)")
	return cmd
}

// serveUDPLoop reads TFTP packets and writes the server's replies back to
// the peer. Mirrors the pre-B4a serveUDP body, with two additions: a
// sidekick goroutine that closes the conn when ctx fires, and clean-exit
// recognition for the net.ErrClosed that results.
func serveUDPLoop(ctx context.Context, conn net.PacketConn, srv *tftp.Server) error {
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	buf := make([]byte, 2048)
	for {
		n, peer, err := conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		for _, reply := range srv.HandlePacketFrom(pkt, peer.String()) {
			if _, err := conn.WriteTo(reply, peer); err != nil {
				log.Printf("porta: WriteTo(%s): %v", peer, err)
			}
		}
	}
}

// awaitServeExit blocks until a listener goroutine exits or ctx is cancelled.
//
//   - A listener error (udpErr or non-nil httpErr) propagates verbatim.
//   - A clean HTTP exit (httpErr delivers nil) while UDP still runs falls
//     through to wait on UDP.
//   - On ctx cancel (SIGINT/SIGTERM) it DRAINS both channels first, so the
//     UDP loop and the HTTP server's 5s graceful Shutdown complete before
//     RunE returns and its deferred conn/store Close fire (#7). httpErr is
//     nil in --http-port 0 mode; nil-channel select arms block forever and
//     the nil guard skips draining it.
func awaitServeExit(ctx context.Context, udpErr, httpErr <-chan error) error {
	select {
	case err := <-udpErr:
		return err
	case err := <-httpErr:
		if err == nil {
			return <-udpErr
		}
		return err
	case <-ctx.Done():
		<-udpErr
		if httpErr != nil {
			<-httpErr
		}
		return nil
	}
}
