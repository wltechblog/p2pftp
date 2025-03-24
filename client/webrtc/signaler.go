package webrtc

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

// SignalingMessage represents a message exchanged with the signaling server
type SignalingMessage struct {
 Type      string          `json:"type"`
 Token     string          `json:"token,omitempty"`
 PeerToken string          `json:"peerToken,omitempty"`
 SDP       json.RawMessage `json:"sdp,omitempty"`
 ICE       json.RawMessage `json:"ice,omitempty"`
}

// TokenHandler is called when a token is assigned by the server
type TokenHandler func(string)

// Signaler handles WebSocket signaling
type Signaler struct {
 conn         *websocket.Conn
 token        string
 peerToken    string
 debugLog     *log.Logger
 peer         *Peer
 mu           sync.Mutex
}

// NewSignaler creates a new WebSocket signaler
func NewSignaler(wsURL, token string, debug *log.Logger) (*Signaler, error) {
 debug.Printf("Attempting connection to: %s", wsURL)
 debug.Printf("Constructed WebSocket URL: %s (token: %s)", wsURL, token)

 headers := make(http.Header)
 headers.Add("Origin", "http://p2pftp-client")
 headers.Add("User-Agent", "P2PFTP-CLI/1.0")
 
 // Create a custom dialer that skips TLS verification for debugging
 dialer := &websocket.Dialer{
  TLSClientConfig: &tls.Config{
   InsecureSkipVerify: true, // Skip certificate validation for testing
  },
  HandshakeTimeout: 45 * time.Second,
 }
 
 debug.Printf("Attempting WebSocket connection with TLS verification disabled")
 debug.Printf("Connection details - URL: %s, Headers: %v", wsURL, headers)
 
 // Dump request details for debugging
 dialer.EnableCompression = true
 
 // Add more detailed error handling
 conn, resp, err := dialer.Dial(wsURL, headers)
 if err != nil {
  debug.Printf("WebSocket connection failed with error: %v", err)
  
  if resp != nil {
   debug.Printf("Server response - Status: %v", resp.Status)
   debug.Printf("Response Headers: %v", resp.Header)
   
   // Try to read response body if available
   if resp.Body != nil {
    bodyBytes := make([]byte, 1024)
    n, readErr := resp.Body.Read(bodyBytes)
    if readErr != nil && readErr != io.EOF {
     debug.Printf("Error reading response body: %v", readErr)
    } else if n > 0 {
     debug.Printf("Response body (partial): %s", bodyBytes[:n])
    }
   }
   
   // Check for specific error conditions
   if strings.Contains(err.Error(), "bad handshake") {
    debug.Printf("Bad handshake detected. Possible causes:")
    debug.Printf("1. Server doesn't recognize the Origin header")
    debug.Printf("2. Server expects different protocol version")
    debug.Printf("3. Server requires authentication")
    debug.Printf("4. TLS certificate issues")
   }
  } else {
   debug.Printf("No response received from server. Possible network connectivity issue.")
  }
  
  return nil, fmt.Errorf("failed to connect to signaling server: %v", err)
 }
 debug.Printf("Connected to signaling server")

 s := &Signaler{
  conn:     conn,
  token:    token,
  debugLog: debug,
 }

 // Start message handler
 go s.handleMessages()

 return s, nil
}

// SetPeer sets the peer for this signaler
func (s *Signaler) SetPeer(peer *Peer) {
 s.mu.Lock()
 s.peer = peer
 s.mu.Unlock()
}

// handleMessages processes incoming WebSocket messages
func (s *Signaler) handleMessages() {
 for {
  var msg SignalingMessage
  err := s.conn.ReadJSON(&msg)
  if err != nil {
   s.debugLog.Printf("Error reading message: %v", err)
   return
  }

  s.debugLog.Printf("Received signal: %+v", msg)

  switch msg.Type {
  case "token":
   s.mu.Lock()
   s.token = msg.Token
   s.debugLog.Printf("Assigned token: %s", s.token)
   if s.peer != nil && s.peer.tokenHandler != nil {
    s.peer.tokenHandler(msg.Token)
   }
   s.mu.Unlock()

  case "request":
   s.peerToken = msg.Token
   s.debugLog.Printf("Connection request from: %s", s.peerToken)
   // TODO: Notify peer of connection request

  case "accepted":
   s.peerToken = msg.Token
   s.debugLog.Printf("Connection accepted by: %s", s.peerToken)
   // TODO: Start WebRTC connection

  case "rejected":
   s.debugLog.Printf("Connection rejected by: %s", msg.Token)
   // TODO: Handle rejection

  case "offer":
   s.debugLog.Printf("Received offer from: %s", msg.Token)
   var offer webrtc.SessionDescription
   if err := json.Unmarshal(msg.SDP, &offer); err != nil {
    s.debugLog.Printf("Error parsing offer: %v", err)
    return
   }
   s.peer.conn.SetRemoteDescription(offer)
   answer, err := s.peer.conn.CreateAnswer(nil)
   if err != nil {
    s.debugLog.Printf("Error creating answer: %v", err)
    return
   }
   s.peer.conn.SetLocalDescription(answer)
   if err := s.SendAnswer(answer); err != nil {
    s.debugLog.Printf("Error sending answer: %v", err)
    return
   }

  case "answer":
   s.debugLog.Printf("Received answer from: %s", msg.Token)
   var answer webrtc.SessionDescription
   if err := json.Unmarshal(msg.SDP, &answer); err != nil {
    s.debugLog.Printf("Error parsing answer: %v", err)
    return
   }
   s.peer.conn.SetRemoteDescription(answer)

  case "ice":
   s.debugLog.Printf("Received ICE candidate from: %s", msg.Token)
   var candidate webrtc.ICECandidateInit
   if err := json.Unmarshal(msg.ICE, &candidate); err != nil {
    s.debugLog.Printf("Error parsing ICE candidate: %v", err)
    return
   }
   s.peer.conn.AddICECandidate(candidate)
  }
 }
}

// SendOffer sends an SDP offer through the signaling server
func (s *Signaler) SendOffer(offer webrtc.SessionDescription) error {
 s.mu.Lock()
 defer s.mu.Unlock()

 sdp, err := json.Marshal(offer)
 if err != nil {
  return fmt.Errorf("failed to marshal SDP: %v", err)
 }

 msg := SignalingMessage{
  Type:      "offer",
  PeerToken: s.peerToken,
  SDP:       sdp,
 }

 err = s.conn.WriteJSON(msg)
 if err != nil {
  return fmt.Errorf("failed to send offer: %v", err)
 }

 return nil
}

// SendAnswer sends an SDP answer through the signaling server
func (s *Signaler) SendAnswer(answer webrtc.SessionDescription) error {
 s.mu.Lock()
 defer s.mu.Unlock()

 sdp, err := json.Marshal(answer)
 if err != nil {
  return fmt.Errorf("failed to marshal SDP: %v", err)
 }

 msg := SignalingMessage{
  Type:      "answer",
  PeerToken: s.peerToken,
  SDP:       sdp,
 }

 err = s.conn.WriteJSON(msg)
 if err != nil {
  return fmt.Errorf("failed to send answer: %v", err)
 }

 return nil
}

// SendICE sends an ICE candidate through the signaling server
func (s *Signaler) SendICE(candidate webrtc.ICECandidateInit) error {
 s.mu.Lock()
 defer s.mu.Unlock()

 ice, err := json.Marshal(candidate)
 if err != nil {
  return fmt.Errorf("failed to marshal ICE candidate: %v", err)
 }

 msg := SignalingMessage{
  Type:      "ice",
  PeerToken: s.peerToken,
  ICE:       ice,
 }

 err = s.conn.WriteJSON(msg)
 if err != nil {
  return fmt.Errorf("failed to send ICE candidate: %v", err)
 }

 return nil
}

// Close closes the WebSocket connection
func (s *Signaler) Close() error {
 s.mu.Lock()
 defer s.mu.Unlock()

 if s.conn != nil {
  return s.conn.Close()
 }
 return nil
}
