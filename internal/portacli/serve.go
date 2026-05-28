package portacli

import (
	"fmt"
	"log"
	"net"

	"github.com/davidg238/porta/internal/handler"
	"github.com/davidg238/porta/internal/tftp"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the TFTP daemon serving the command queue + payloads",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			srv := tftp.NewServer()
			srv.SetDispatcher(handler.New(st, nowSec))

			addr := fmt.Sprintf(":%d", port)
			conn, err := net.ListenPacket("udp", addr)
			if err != nil {
				return err
			}
			defer conn.Close()
			log.Printf("porta: serving TFTP on udp %s (db=%s)", addr, dbPath)
			return serveUDP(conn, srv)
		},
	}
	cmd.Flags().IntVar(&port, "port", 6969, "UDP port")
	return cmd
}

// serveUDP reads TFTP packets and writes the server's replies back to the peer.
func serveUDP(conn net.PacketConn, srv *tftp.Server) error {
	buf := make([]byte, 2048)
	for {
		n, peer, err := conn.ReadFrom(buf)
		if err != nil {
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
