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

type WebRTCState struct {
peerToken    string
isInitiator  bool
connected    bool
}

type Client struct {
conn     *websocket.Conn
token    string
messages chan Message
ui       *UI
webrtc   *WebRTCState
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
webrtc:   &WebRTCState{},
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
c.webrtc.peerToken = msg.Token
c.webrtc.isInitiator = false

case "accepted":
c.ui.ShowConnectionAccepted(msg.Token)
c.webrtc.peerToken = msg.Token
c.webrtc.isInitiator = true
// Send initial offer after acceptance
c.SendMessage(Message{
Type:      "offer",
PeerToken: c.webrtc.peerToken,
SDP:       "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=group:BUNDLE 0\r\na=ice-options:trickle\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\nc=IN IP4 0.0.0.0\r\na=mid:0\r\na=sctpmap:5000 webrtc-datachannel 1024\r\n",
})

case "rejected":
c.ui.ShowConnectionRejected(msg.Token)
c.webrtc = &WebRTCState{}

case "offer":
c.ui.LogDebug(fmt.Sprintf("Received offer from %s", msg.Token))
// Send answer in response to offer
c.SendMessage(Message{
Type:      "answer",
PeerToken: msg.Token,
SDP:       "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=group:BUNDLE 0\r\na=ice-options:trickle\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\nc=IN IP4 0.0.0.0\r\na=mid:0\r\na=sctpmap:5000 webrtc-datachannel 1024\r\n",
})

case "answer":
c.ui.LogDebug(fmt.Sprintf("Received answer from %s", msg.Token))
// Exchange ICE candidates
c.SendMessage(Message{
Type:      "ice",
PeerToken: c.webrtc.peerToken,
ICE:       "candidate:0 1 UDP 2122252543 10.0.0.1 54321 typ host",
})

case "ice":
c.ui.LogDebug(fmt.Sprintf("Received ICE candidate from %s", msg.Token))
c.webrtc.connected = true
c.ui.LogDebug("WebRTC connection established")

case "error":
c.ui.ShowError(msg.SDP)
c.webrtc = &WebRTCState{}
}
}
}

func (c *Client) SendMessage(msg Message) error {
// Log outgoing message
c.ui.LogDebug(fmt.Sprintf("→ Sending: %+v", msg))
return c.conn.WriteJSON(msg)
}

func (c *Client) Connect(peerToken string) error {
c.webrtc = &WebRTCState{
peerToken: peerToken,
isInitiator: true,
}
return c.SendMessage(Message{
Type:      "connect",
PeerToken: peerToken,
})
}

func (c *Client) Accept(peerToken string) error {
c.webrtc = &WebRTCState{
peerToken: peerToken,
isInitiator: false,
}
return c.SendMessage(Message{
Type:      "accept",
PeerToken: peerToken,
})
}

func (c *Client) Reject(peerToken string) error {
c.webrtc = &WebRTCState{}
return c.SendMessage(Message{
Type:      "reject",
PeerToken: peerToken,
})
}
