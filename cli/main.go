package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"time"

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
log.Printf("Connecting to %s...", u.String())

// Connect to WebSocket server with custom dialer
dialer := websocket.Dialer{
EnableCompression: true,
HandshakeTimeout: 10 * time.Second,
}
log.Printf("Attempting WebSocket connection to %s...", u.String())
conn, resp, err := dialer.Dial(u.String(), nil)
if err != nil {
if resp != nil {
log.Printf("HTTP Response Status: %d", resp.StatusCode)
log.Printf("HTTP Response Headers: %v", resp.Header)
}
log.Fatal("WebSocket dial error:", err)
}
defer conn.Close()
log.Printf("Successfully connected to server")

client := &Client{
conn:     conn,
messages: make(chan Message, 100),
webrtc:   &WebRTCState{},
}

// Create and start UI
ui := NewUI(client)
client.ui = ui

// Start message handler
log.Printf("Starting message handler")
go client.handleMessages()

// Run UI
if err := ui.Run(); err != nil {
fmt.Printf("Error running UI: %v\n", err)
}
}

func (c *Client) handleMessages() {
c.ui.LogDebug("Message handler started")
for {
var msg Message
c.ui.LogDebug("Waiting for next message...")
err := c.conn.ReadJSON(&msg)
if err != nil {
c.ui.LogDebug(fmt.Sprintf("Error reading message: %v", err))
c.ui.ShowError(fmt.Sprintf("Connection error: %v", err))
return
}

// Log raw message for debugging
c.ui.LogDebug(fmt.Sprintf("← Received: %+v", msg))

switch msg.Type {
case "token":
c.token = msg.Token
c.ui.SetToken(msg.Token)
c.ui.LogDebug(fmt.Sprintf("Set local token to: %s", msg.Token))

case "request":
c.ui.LogDebug(fmt.Sprintf("Received connection request from peer %s", msg.Token))
c.ui.ShowConnectionRequest(msg.Token)
c.webrtc.peerToken = msg.Token
c.webrtc.isInitiator = false

case "accepted":
c.ui.LogDebug(fmt.Sprintf("Peer %s accepted connection", msg.Token))
c.ui.ShowConnectionAccepted(msg.Token)
if c.webrtc == nil {
c.ui.LogDebug("Error: WebRTC state is nil")
return
}
c.webrtc.peerToken = msg.Token
c.webrtc.isInitiator = true
// Send initial offer after acceptance
c.ui.LogDebug("Preparing to send WebRTC offer")
if err := c.SendMessage(Message{
Type:      "offer",
PeerToken: c.webrtc.peerToken,
SDP:       "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=group:BUNDLE 0\r\na=ice-options:trickle\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\nc=IN IP4 0.0.0.0\r\na=mid:0\r\na=sctpmap:5000 webrtc-datachannel 1024\r\n",
}); err != nil {
c.ui.LogDebug(fmt.Sprintf("Error sending offer: %v", err))
c.ui.ShowError(fmt.Sprintf("Failed to send offer: %v", err))
}

case "rejected":
c.ui.LogDebug(fmt.Sprintf("Connection rejected by peer %s", msg.Token))
c.ui.ShowConnectionRejected(msg.Token)
c.webrtc = &WebRTCState{}

case "offer":
c.ui.LogDebug(fmt.Sprintf("Received WebRTC offer from %s", msg.Token))
// Send answer in response to offer
c.ui.LogDebug("Preparing answer")
err := c.SendMessage(Message{
Type:      "answer",
PeerToken: msg.Token,
SDP:       "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=group:BUNDLE 0\r\na=ice-options:trickle\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\nc=IN IP4 0.0.0.0\r\na=mid:0\r\na=sctpmap:5000 webrtc-datachannel 1024\r\n",
})
if err != nil {
c.ui.LogDebug(fmt.Sprintf("Error sending answer: %v", err))
c.ui.ShowError(fmt.Sprintf("Failed to send answer: %v", err))
}

case "answer":
c.ui.LogDebug(fmt.Sprintf("Received WebRTC answer from %s", msg.Token))
// Exchange ICE candidates
c.ui.LogDebug("Sending ICE candidate")
err := c.SendMessage(Message{
Type:      "ice",
PeerToken: c.webrtc.peerToken,
ICE:       "candidate:0 1 UDP 2122252543 10.0.0.1 54321 typ host",
})
if err != nil {
c.ui.LogDebug(fmt.Sprintf("Error sending ICE: %v", err))
c.ui.ShowError(fmt.Sprintf("Failed to send ICE: %v", err))
}

case "ice":
c.ui.LogDebug(fmt.Sprintf("Received ICE candidate from %s", msg.Token))
c.webrtc.connected = true
c.ui.LogDebug("WebRTC connection established!")

case "error":
c.ui.LogDebug(fmt.Sprintf("Received error message: %s", msg.SDP))
c.ui.ShowError(msg.SDP)
c.webrtc = &WebRTCState{}
}
}
}

func (c *Client) SendMessage(msg Message) error {
c.ui.LogDebug(fmt.Sprintf("→ Preparing to send message type: %s to peer: %s", msg.Type, msg.PeerToken))

// Lock to prevent concurrent writes
c.ui.LogDebug("Acquiring write lock...")
c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
err := c.conn.WriteJSON(msg)
if err != nil {
c.ui.LogDebug(fmt.Sprintf("→ Send error: %v", err))
c.ui.ShowError("Connection error - please try again")
return fmt.Errorf("failed to send message: %v", err)
}

c.ui.LogDebug(fmt.Sprintf("→ Successfully sent message type: %s to peer: %s", msg.Type, msg.PeerToken))
return nil
}

func (c *Client) Connect(peerToken string) error {
c.ui.LogDebug(fmt.Sprintf("Connect called with peer token: %s", peerToken))

c.webrtc = &WebRTCState{
peerToken:   peerToken,
isInitiator: true,
}

c.ui.LogDebug("Sending connect message")
err := c.SendMessage(Message{
Type:      "connect",
PeerToken: peerToken,
})

if err != nil {
c.ui.LogDebug(fmt.Sprintf("Connect failed: %v", err))
c.webrtc = &WebRTCState{} // Reset state on error
return err
}

c.ui.LogDebug("Connect message sent successfully")
return nil
}

func (c *Client) Accept(peerToken string) error {
c.ui.LogDebug(fmt.Sprintf("Accepting connection from peer: %s", peerToken))

c.webrtc = &WebRTCState{
peerToken:   peerToken,
isInitiator: false,
}

err := c.SendMessage(Message{
Type:      "accept",
PeerToken: peerToken,
})

if err != nil {
c.ui.LogDebug(fmt.Sprintf("Accept failed: %v", err))
c.webrtc = &WebRTCState{} // Reset state on error
return err
}

c.ui.LogDebug("Accept message sent successfully")
return nil
}

func (c *Client) Reject(peerToken string) error {
c.ui.LogDebug(fmt.Sprintf("Rejecting connection from peer: %s", peerToken))

c.webrtc = &WebRTCState{}

err := c.SendMessage(Message{
Type:      "reject",
PeerToken: peerToken,
})

if err != nil {
c.ui.LogDebug(fmt.Sprintf("Reject failed: %v", err))
return err
}

c.ui.LogDebug("Reject message sent successfully")
return nil
}
