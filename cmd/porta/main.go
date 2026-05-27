package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/davidg238/porta/internal/cli"
	"github.com/davidg238/porta/internal/debug"
	"github.com/davidg238/porta/internal/debugui"
	"github.com/davidg238/porta/internal/gateway"
	"github.com/davidg238/porta/internal/mcpserver"
	"github.com/davidg238/porta/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	dbPath := flag.String("db", "jast.db", "SQLite database path")
	udpPort := flag.Int("udp", 5683, "UDP port for TFTP device polls")
	tcpPort := flag.Int("tcp", 5684, "TCP port for CLI connections")
	mcpPort := flag.Int("mcp", 5685, "HTTP port for MCP/SSE server")
	compileURL := flag.String("compile-url", "http://127.0.0.1:5686",
		"Base URL of the ST compile service (POST /compile)")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	// Open store
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()
	log.Printf("store: opened %s", *dbPath)

	// Create gateway
	gw := gateway.New(st)

	// Start UDP TFTP listener
	udpAddr := fmt.Sprintf(":%d", *udpPort)
	udpConn, err := net.ListenPacket("udp6", udpAddr)
	if err != nil {
		log.Fatalf("udp: %v", err)
	}
	defer udpConn.Close()
	log.Printf("udp: listening on %s", udpAddr)
	go serveUDP(udpConn, gw)

	// Start TCP CLI listener
	tcpAddr := fmt.Sprintf(":%d", *tcpPort)
	tcpLn, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		log.Fatalf("tcp: %v", err)
	}
	defer tcpLn.Close()
	log.Printf("tcp: listening on %s", tcpAddr)
	cliListener := cli.NewListener(gw, tcpLn)
	go cliListener.Serve()

	// Start MCP/SSE server
	dbgMgr := debug.NewManager(st)
	mcpSrv := mcpserver.New(st, dbgMgr, *compileURL)
	log.Printf("mcp: compile service at %s", *compileURL)
	debugHub := debugui.NewHub()
	mcpserver.SetDebugHub(debugHub)
	debugHandler := debugui.NewHandler(debugHub, st, dbgMgr)
	mcpAddr := fmt.Sprintf(":%d", *mcpPort)
	sseHandler := mcp.NewSSEHandler(func(r *http.Request) *mcp.Server { return mcpSrv }, nil)
	mcpMux := http.NewServeMux()
	mcpMux.Handle("/sse", sseHandler)
	debugHandler.Register(mcpMux)
	go func() {
		log.Printf("mcp: listening on %s", mcpAddr)
		if err := http.ListenAndServe(mcpAddr, mcpMux); err != nil {
			log.Printf("mcp: %v", err)
		}
	}()

	log.Println("jast-gw running")

	// Wait for signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down")
}

func serveUDP(conn net.PacketConn, gw *gateway.Gateway) {
	buf := make([]byte, 2048)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			return // conn closed
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])

		replies := gw.HandleDevicePacket(pkt, addr.String())
		for _, reply := range replies {
			conn.WriteTo(reply, addr)
		}
	}
}
