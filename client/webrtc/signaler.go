package webrtc

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

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
 debug.Printf("Connecting to signaling server: %s", wsURL)

 headers := make(http.Header)
 //headers.Add("Origin", wsURL) // Removing origin header

 conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
 if err != nil {
  if resp != nil {
   debug.Printf("Server response: %v", resp.Status)
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
