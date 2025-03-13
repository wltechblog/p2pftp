package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"

	"github.com/gorilla/websocket"
)

func main() {
    addr := flag.String("addr", "localhost:8089", "server address")
    flag.Parse()

    u := url.URL{Scheme: "wss", Host: *addr, Path: "/ws"}
    log.Printf("Connecting to %s...", u.String())

    conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
    if err != nil {
        log.Fatal("WebSocket dial error:", err)
    }
    defer conn.Close()

    // Create client first
    client := NewClient(conn)

    // Create UI with back-reference to client
    ui := NewUI(client)

    // Start message handler
    go client.handleMessages()

    // Run UI (blocks until exit)
    if err := ui.Run(); err != nil {
        fmt.Printf("Error running UI: %v\n", err)
    }
}
