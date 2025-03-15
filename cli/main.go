package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"

	"github.com/gorilla/websocket"

	"github.com/wltechblog/p2pftp/cli/ui"
)

// main is the entry point for the CLI application
func main() {
    // Parse command line arguments
    addr := flag.String("addr", "localhost:8089", "server address")
    flag.Parse()

    // Create WebSocket URL
    u := url.URL{Scheme: "wss", Host: *addr, Path: "/ws"}
    log.Printf("Connecting to %s...", u.String())

    // Connect to the server
    conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
    if err != nil {
        log.Fatal("WebSocket dial error:", err)
    }
    defer conn.Close()

    // Create client
    client := NewClient(conn)

    // Create UI with back-reference to client
    userInterface := ui.NewUI(client)
    client.SetUI(userInterface)

    // Start message handler
    go client.handleMessages()

    // Run UI (blocks until exit)
    if err := userInterface.Run(); err != nil {
        fmt.Printf("Error running UI: %v\n", err)
    }
}