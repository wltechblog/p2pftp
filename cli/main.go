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
ui       *UI
webrtc   *WebRTCState
}

func main() {
addr := flag.String("addr", "localhost:8089", "server address")
flag.Parse()

// Create WebSocket URL
u := url.URL{Scheme: "wss", Host: *addr, Path: "/ws"}
log.Printf("Connecting to %s...", u.String())

// Connect to WebSocket server
conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
if err != nil {
log.Fatal("WebSocket dial error:", err)
}
defer conn.Close()
log.Printf("Successfully connected to server")

client := &Client{
conn:    conn,
webrtc:  &WebRTCState{},
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
c.ui.LogDebug("Message handler started, waiting for server token...")
for {
var msg Message
err := c.conn.ReadJSON(&msg)
if err != nil {
c.ui.LogDebug(fmt.Sprintf("Error reading message: %v", err))
c.ui.ShowError("Connection error - please try again")
return
}

c.ui.LogDebug(fmt.Sprintf("Received: type=%s token=%s peerToken=%s", msg.Type, msg.Token, msg.PeerToken))

switch msg.Type {
case "token":
c.token = msg.Token
c.ui.SetToken(msg.Token)

case "request":
c.ui.ShowConnectionRequest(msg.Token)
c.webrtc.peerToken = msg.Token
c.webrtc.isInitiator = false

case "accepted":
if c.webrtc.peerToken == "" {
c.ui.ShowError("No active connection attempt")
continue
}
c.ui.LogDebug(fmt.Sprintf("Connection accepted by peer %s, sending offer...", msg.Token))
// Send SDP offer
offerSDP := `v=0
o=- 4294967296 1 IN IP4 127.0.0.1
s=-
t=0 0
a=group:BUNDLE 0
a=ice-lite
a=msid-semantic: WMS *
m=application 9 UDP/DTLS/SCTP webrtc-datachannel
c=IN IP4 0.0.0.0
b=AS:30
a=ice-ufrag:abc123
a=ice-pwd:secret
a=fingerprint:sha-256 01:02:03:04:05:06:07:08:09:0A:0B:0C:0D:0E:0F:10:11:12:13:14:15:16:17:18:19:1A:1B:1C:1D:1E:1F:20
a=setup:actpass
a=sctpmap:5000 webrtc-datachannel 1024`

if err := c.SendMessage(Message{
Type:      "offer",
PeerToken: c.webrtc.peerToken,
SDP:       offerSDP,
}); err != nil {
c.ui.ShowError("Failed to send offer")
return
}

case "answer":
c.ui.LogDebug("Received answer from peer, sending ICE candidate...")
if err := c.SendMessage(Message{
Type:      "ice",
PeerToken: c.webrtc.peerToken,
ICE:       "candidate:0 1 UDP 2122194623 192.168.1.100 5000 typ host",
}); err != nil {
c.ui.ShowError("Failed to send ICE candidate")
return
}
c.webrtc.connected = true
c.ui.ShowConnectionAccepted("Connection established")

case "ice":
c.ui.LogDebug("Received ICE candidate, connection ready")
c.webrtc.connected = true
c.ui.ShowConnectionAccepted("Connection established")

case "offer":
if c.webrtc.peerToken == "" {
c.webrtc.peerToken = msg.Token // Set peer token from offer
}
c.ui.LogDebug(fmt.Sprintf("Received offer from peer %s, sending answer...", msg.Token))
answerSDP := `v=0
o=- 0 0 IN IP4 127.0.0.1
s=-
t=0 0
a=group:BUNDLE 0
a=ice-options:trickle
m=application 9 UDP/DTLS/SCTP webrtc-datachannel
c=IN IP4 0.0.0.0
a=mid:0
a=sctpmap:5000 webrtc-datachannel 1024`

if err := c.SendMessage(Message{
Type:      "answer",
PeerToken: c.webrtc.peerToken,
SDP:       answerSDP,
}); err != nil {
c.ui.ShowError("Failed to send answer")
return
}

case "rejected":
c.ui.ShowConnectionRejected(msg.Token)
c.webrtc = &WebRTCState{}

case "error":
c.ui.ShowError(msg.SDP)
c.webrtc = &WebRTCState{}
}
}
}

func (c *Client) SendMessage(msg Message) error {
logMsg := fmt.Sprintf("Sending: type=%s", msg.Type)
if msg.PeerToken != "" {
logMsg += fmt.Sprintf(" to=%s", msg.PeerToken)
}
c.ui.LogDebug(logMsg)

err := c.conn.WriteJSON(msg)
if err != nil {
c.ui.ShowError("Send failed: " + err.Error())
return err
}
return nil
}

func (c *Client) Connect(peerToken string) error {
if c.webrtc.connected {
return fmt.Errorf("already connected to a peer")
}
c.webrtc = &WebRTCState{
peerToken:   peerToken,
isInitiator: true,
}
return c.SendMessage(Message{Type: "connect", PeerToken: peerToken})
}

func (c *Client) Accept(peerToken string) error {
if c.webrtc.connected {
return fmt.Errorf("already connected to a peer")
}
c.webrtc = &WebRTCState{peerToken: peerToken, isInitiator: false}
return c.SendMessage(Message{Type: "accept", PeerToken: peerToken})
}

func (c *Client) Reject(peerToken string) error {
c.webrtc = &WebRTCState{}
return c.SendMessage(Message{Type: "reject", PeerToken: peerToken})
}
