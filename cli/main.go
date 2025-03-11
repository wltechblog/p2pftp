package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"

	"github.com/gorilla/websocket"
)

// Message matches the server's message structure
type Message struct {
	Type      string `json:"type"`
	Token     string `json:"token,omitempty"`
	PeerToken string `json:"peerToken,omitempty"`
	SDP       string `json:"sdp,omitempty"`
	ICE       string `json:"ice,omitempty"`
}

type Client struct {
	conn     *websocket.Conn
	token    string
	messages chan Message
	ui       *UI
}

func main() {
	addr := flag.String("addr", "localhost:8089", "server address")
	flag.Parse()

// Create WebSocket URL
u := url.URL{Scheme: "wss", Host: *addr, Path: "/ws"}
	
	// Connect to WebSocket server
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer conn.Close()

	client := &Client{
		conn:     conn,
		messages: make(chan Message, 100),
	}

	// Create and start UI
	ui := NewUI(client)
	client.ui = ui

	// Start message handler
	go client.handleMessages()

	// Run UI
	if err := ui.Run(); err != nil {
		fmt.Printf("Error running UI: %v\n", err)
	}
}

func (c *Client) handleMessages() {
	for {
var msg Message
err := c.conn.ReadJSON(&msg)
if err != nil {
log.Printf("Error reading message: %v", err)
return
}

// Log raw message for debugging
c.ui.LogDebug(fmt.Sprintf("← Received: %+v", msg))

switch msg.Type {
		case "token":
			c.token = msg.Token
			c.ui.SetToken(msg.Token)
		case "request":
			c.ui.ShowConnectionRequest(msg.Token)
		case "accepted":
			c.ui.ShowConnectionAccepted(msg.Token)
		case "rejected":
			c.ui.ShowConnectionRejected(msg.Token)
		case "offer":
			// Handle WebRTC offer
		case "answer":
			// Handle WebRTC answer
		case "ice":
			// Handle ICE candidate
		case "error":
			c.ui.ShowError(msg.SDP)
		}
	}
}

func (c *Client) SendMessage(msg Message) error {
// Log outgoing message
c.ui.LogDebug(fmt.Sprintf("→ Sending: %+v", msg))
return c.conn.WriteJSON(msg)
}

func (c *Client) Connect(peerToken string) error {
	return c.SendMessage(Message{
		Type:      "connect",
		PeerToken: peerToken,
	})
}

func (c *Client) Accept(peerToken string) error {
	return c.SendMessage(Message{
		Type:      "accept",
		PeerToken: peerToken,
	})
}

func (c *Client) Reject(peerToken string) error {
	return c.SendMessage(Message{
		Type:      "reject",
		PeerToken: peerToken,
	})
}
