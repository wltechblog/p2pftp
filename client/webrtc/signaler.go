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
	Type      string `json:"type"`
	Token     string `json:"token,omitempty"`
	PeerToken string `json:"peerToken,omitempty"`
	SDP       string `json:"sdp,omitempty"`
	ICE       string `json:"ice,omitempty"`
}

// TokenHandler is called when a token is assigned by the server
type TokenHandler func(string)

// ErrorHandler is called when an error is received from the server
type ErrorHandler func(string)

// Signaler handles WebSocket signaling
type Signaler struct {
	conn      *websocket.Conn
	token     string
	peerToken string
	debugLog  *log.Logger
	peer      *Peer
	mu        sync.Mutex
	tokenChan chan struct{}
}

// NewSignaler creates a new WebSocket signaler with retry logic
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

	// Implement retry logic with exponential backoff
	maxRetries := 5
	var conn *websocket.Conn
	var resp *http.Response
	var err error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			debug.Printf("Retry attempt %d/%d after %v", attempt+1, maxRetries, backoff)
			time.Sleep(backoff)
		}

		conn, resp, err = dialer.Dial(wsURL, headers)
		if err == nil {
			break // Successfully connected
		}

		debug.Printf("Connection attempt %d failed: %v", attempt+1, err)

		if resp != nil {
			debug.Printf("Server response - Status: %v", resp.Status)
			debug.Printf("Response Headers: %v", resp.Header)

			if resp.Body != nil {
				bodyBytes := make([]byte, 1024)
				n, readErr := resp.Body.Read(bodyBytes)
				if readErr != nil && readErr != io.EOF {
					debug.Printf("Error reading response body: %v", readErr)
				} else if n > 0 {
					debug.Printf("Response body (partial): %s", bodyBytes[:n])
				}
				resp.Body.Close()
			}
		}

		if attempt == maxRetries-1 {
			return nil, fmt.Errorf("failed to connect after %d attempts: %v", maxRetries, err)
		}
	}
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
		conn:      conn,
		token:     token,
		debugLog:  debug,
		tokenChan: make(chan struct{}),
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
			close(s.tokenChan)
			s.mu.Unlock()

		case "error":
			s.debugLog.Printf("Server error: %s", msg.SDP)
			if s.peer != nil && s.peer.errorHandler != nil {
				s.peer.errorHandler(msg.SDP)
			}

		case "request":
			s.peerToken = msg.Token
			s.debugLog.Printf("Connection request from: %s", s.peerToken)
			if s.peer != nil && s.peer.statusHandler != nil {
				s.peer.statusHandler(fmt.Sprintf("Connection request from: %s", s.peerToken))
			}

		case "accepted":
			s.peerToken = msg.Token
			s.debugLog.Printf("Connection accepted by: %s", s.peerToken)
			if s.peer != nil && s.peer.statusHandler != nil {
				s.peer.statusHandler(fmt.Sprintf("Connection accepted by: %s", s.peerToken))
			}

		case "rejected":
			s.debugLog.Printf("Connection rejected by: %s", msg.Token)
			if s.peer != nil && s.peer.statusHandler != nil {
				s.peer.statusHandler(fmt.Sprintf("Connection rejected by: %s", msg.Token))
			}

		case "offer":
			s.debugLog.Printf("Received offer from: %s", msg.Token)
			var offer webrtc.SessionDescription
			if err := json.Unmarshal([]byte(msg.SDP), &offer); err != nil {
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
			if err := json.Unmarshal([]byte(msg.SDP), &answer); err != nil {
				s.debugLog.Printf("Error parsing answer: %v", err)
				return
			}
			s.peer.conn.SetRemoteDescription(answer)

		case "ice":
			s.debugLog.Printf("Received ICE candidate from: %s", msg.Token)
			var candidate webrtc.ICECandidateInit
			if err := json.Unmarshal([]byte(msg.ICE), &candidate); err != nil {
				s.debugLog.Printf("Error parsing ICE candidate: %v", err)
				return
			}
			s.debugLog.Printf("Adding ICE candidate: %s", msg.ICE)
			if err := s.peer.conn.AddICECandidate(candidate); err != nil {
				s.debugLog.Printf("Error adding ICE candidate: %v", err)
			} else {
				s.debugLog.Printf("ICE candidate added successfully")
			}
		}
	}
}

// SendConnectRequest sends a connection request to a peer
func (s *Signaler) SendConnectRequest(peerToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg := SignalingMessage{
		Type:      "connect",
		Token:     s.token,
		PeerToken: peerToken,
	}

	err := s.conn.WriteJSON(msg)
	if err != nil {
		return fmt.Errorf("failed to send connect request: %v", err)
	}

	s.debugLog.Printf("Sent connect request to: %s", peerToken)
	return nil
}

// SendAccept sends an accept message to a peer
func (s *Signaler) SendAccept(peerToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg := SignalingMessage{
		Type:      "accept",
		Token:     s.token,
		PeerToken: peerToken,
	}

	err := s.conn.WriteJSON(msg)
	if err != nil {
		return fmt.Errorf("failed to send accept message: %v", err)
	}

	s.debugLog.Printf("Sent accept message to: %s", peerToken)
	return nil
}

// SendReject sends a reject message to a peer
func (s *Signaler) SendReject(peerToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg := SignalingMessage{
		Type:      "reject",
		Token:     s.token,
		PeerToken: peerToken,
	}

	err := s.conn.WriteJSON(msg)
	if err != nil {
		return fmt.Errorf("failed to send reject message: %v", err)
	}

	s.debugLog.Printf("Sent reject message to: %s", peerToken)
	return nil
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
		Token:     s.token,     // Our token
		PeerToken: s.peerToken, // Target peer's token
		SDP:       string(sdp),
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
		Token:     s.token,     // Our token
		PeerToken: s.peerToken, // Target peer's token
		SDP:       string(sdp),
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

	// Check if we have a peer token
	if s.peerToken == "" {
		s.debugLog.Printf("Cannot send ICE: peer token is empty")
		return fmt.Errorf("peer token is empty")
	}

	ice, err := json.Marshal(candidate)
	if err != nil {
		return fmt.Errorf("failed to marshal ICE candidate: %v", err)
	}

	msg := SignalingMessage{
		Type:      "ice",
		Token:     s.token,     // Our token
		PeerToken: s.peerToken, // Target peer's token
		ICE:       string(ice),
	}

	s.debugLog.Printf("Sending ICE candidate to peer %s: %s", s.peerToken, string(ice))

	err = s.conn.WriteJSON(msg)
	if err != nil {
		return fmt.Errorf("failed to send ICE candidate: %v", err)
	}

	s.debugLog.Printf("ICE candidate sent successfully to peer: %s", s.peerToken)
	return nil
}

// WaitForToken waits for the server to assign a token with timeout
func (s *Signaler) WaitForToken(timeout time.Duration) error {
	select {
	case <-s.tokenChan:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("timeout waiting for token assignment")
	}
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
