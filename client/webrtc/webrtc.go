package webrtc

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/pion/webrtc/v3"
)

// Helper functions for WebRTC pointer types
func uint16Ptr(v uint16) *uint16 {
	return &v
}

func boolPtr(v bool) *bool {
	return &v
}

// Peer represents a WebRTC peer connection
type Peer struct {
	conn           *webrtc.PeerConnection
	signaler       *Signaler
	controlChannel *webrtc.DataChannel
	dataChannel    *webrtc.DataChannel
	controlHandler func([]byte)
	messageHandler func(string)
	statusHandler  func(string)
	dataHandler    func([]byte)
	debugLog       *log.Logger
	tokenHandler   func(string)
	errorHandler   func(string)
	negotiated     bool
	maxChunkSize   int32
	mu             sync.Mutex
	iceConnected   bool
	iceTimeout     time.Duration
}

// SetTokenHandler sets a handler for when the server assigns a token
func (p *Peer) SetTokenHandler(handler func(string)) {
	p.tokenHandler = handler
}

// SetErrorHandler sets a handler for when the server returns an error
func (p *Peer) SetErrorHandler(handler func(string)) {
	p.errorHandler = handler
}

// SetDataHandler sets a handler for data channel messages
func (p *Peer) SetDataHandler(handler func([]byte)) {
	p.dataHandler = handler
}

// NewPeer creates a new WebRTC peer
func NewPeer(debug *log.Logger) (*Peer, error) {
	// Create peer connection configuration with multiple STUN servers for better connectivity
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{
					"stun:stun.l.google.com:19302",
					"stun:stun1.l.google.com:19302",
					"stun:stun2.l.google.com:19302",
					"stun:stun3.l.google.com:19302",
					"stun:stun4.l.google.com:19302",
				},
			},
		},
		ICETransportPolicy: webrtc.ICETransportPolicyAll,
	}

	// Create new peer connection
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create peer connection: %v", err)
	}

	if debug == nil {
		debug = log.New(io.Discard, "", 0)
	}

	peer := &Peer{
		conn:           pc,
		signaler:       nil,
		controlChannel: nil,
		dataChannel:    nil,
		controlHandler: nil,
		messageHandler: nil,
		statusHandler:  nil,
		dataHandler:    nil,
		tokenHandler:   nil,
		errorHandler:   nil,
		debugLog:       debug,
		negotiated:     false,
		maxChunkSize:   16384,
		mu:             sync.Mutex{},
		iceConnected:   false,
		iceTimeout:     30 * time.Second, // 30 second timeout for ICE connection
	}

	// Set up data channel handlers
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		peer.debugLog.Printf("New data channel: %s", dc.Label())

		switch dc.Label() {
		case "p2pftp-control":
			peer.controlChannel = dc
			peer.setupControlChannel(dc)
		case "p2pftp-data":
			peer.dataChannel = dc
			peer.setupDataChannel(dc)
		}
	})

	// Log state changes and handle ICE connection state
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		peer.debugLog.Printf("ICE Connection State changed: %s", state.String())

		// Update connection status
		switch state {
		case webrtc.ICEConnectionStateConnected, webrtc.ICEConnectionStateCompleted:
			peer.mu.Lock()
			peer.iceConnected = true
			peer.mu.Unlock()
			peer.debugLog.Printf("ICE connection established successfully")
		case webrtc.ICEConnectionStateFailed, webrtc.ICEConnectionStateDisconnected, webrtc.ICEConnectionStateClosed:
			peer.mu.Lock()
			peer.iceConnected = false
			peer.mu.Unlock()
			peer.debugLog.Printf("ICE connection failed or closed")
		case webrtc.ICEConnectionStateChecking:
			// Start a timeout for ICE connection
			go func() {
				time.Sleep(peer.iceTimeout)

				peer.mu.Lock()
				if !peer.iceConnected && peer.conn.ICEConnectionState() == webrtc.ICEConnectionStateChecking {
					peer.debugLog.Printf("ICE connection timed out after %v", peer.iceTimeout)
					if peer.statusHandler != nil {
						peer.statusHandler("Connection timed out. Please try again.")
					}
				}
				peer.mu.Unlock()
			}()
		}

		if peer.statusHandler != nil {
			peer.statusHandler(fmt.Sprintf("Connection state: %s", state.String()))
		}
	})

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		peer.debugLog.Printf("Peer Connection State changed: %s", state.String())
	})

	pc.OnSignalingStateChange(func(state webrtc.SignalingState) {
		peer.debugLog.Printf("Signaling State changed: %s", state.String())
	})

	// Add ICE candidate handler
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			peer.debugLog.Printf("ICE gathering completed")
			return
		}

		peer.debugLog.Printf("New ICE candidate: %s", candidate.String())

		// Send the ICE candidate to the remote peer via signaling server
		if peer.signaler != nil {
			candidateInit := candidate.ToJSON()
			err := peer.signaler.SendICE(candidateInit)
			if err != nil {
				peer.debugLog.Printf("Failed to send ICE candidate: %v", err)
			}
		} else {
			peer.debugLog.Printf("Cannot send ICE candidate: signaler not initialized")
		}
	})

	return peer, nil
}

func (p *Peer) setupControlChannel(dc *webrtc.DataChannel) {
	dc.OnOpen(func() {
		p.debugLog.Printf("Control channel opened")
		// Send capabilities after channel is open
		p.sendCapabilities()
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		// Log message details with more information for debugging
		if msg.IsString {
			p.debugLog.Printf("Control message received (string): %s", string(msg.Data))
		} else {
			p.debugLog.Printf("Control message received (binary): %d bytes", len(msg.Data))
			// Print first few bytes for debugging
			if len(msg.Data) > 0 {
				maxBytes := 32
				if len(msg.Data) < maxBytes {
					maxBytes = len(msg.Data)
				}
				p.debugLog.Printf("First %d bytes: %v", maxBytes, msg.Data[:maxBytes])
			}
		}

		// Always try to parse as JSON regardless of IsString flag
		var msgData map[string]interface{}
		if err := json.Unmarshal(msg.Data, &msgData); err != nil {
			p.debugLog.Printf("Error parsing control message: %v", err)
			// Still pass the raw data to the control handler
			if p.controlHandler != nil {
				p.controlHandler(msg.Data)
			}
			return
		}

		// Successfully parsed JSON, now handle by message type
		msgType, ok := msgData["type"].(string)
		if !ok {
			p.debugLog.Printf("Message missing 'type' field: %v", msgData)
			return
		}

		p.debugLog.Printf("Received message of type: %s", msgType)

		// Handle different message types
		switch msgType {
		case "message":
			// Handle chat message
			content, ok := msgData["content"].(string)
			if ok && p.messageHandler != nil {
				p.debugLog.Printf("Dispatching chat message: %s", content)
				p.messageHandler(content)
			} else {
				p.debugLog.Printf("Invalid message format or missing content field: %v", msgData)
			}
		default:
			// Pass to general control handler
			if p.controlHandler != nil {
				p.controlHandler(msg.Data)
			}
		}
	})

	dc.OnClose(func() {
		p.debugLog.Printf("Control channel closed")
	})

	dc.OnError(func(err error) {
		p.debugLog.Printf("Control channel error: %v", err)
		if p.errorHandler != nil {
			p.errorHandler(fmt.Sprintf("Control channel error: %v", err))
		}
	})
}

func (p *Peer) setupDataChannel(dc *webrtc.DataChannel) {
	dc.OnOpen(func() {
		p.debugLog.Printf("Data channel opened")
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		// For data channel, we expect binary data
		if msg.IsString {
			p.debugLog.Printf("Warning: Received string data on binary channel: %s", string(msg.Data))
		} else {
			p.debugLog.Printf("Data channel message received: %d bytes", len(msg.Data))
		}

		// Pass data to handler regardless of type
		if p.dataHandler != nil {
			p.dataHandler(msg.Data)
		}
	})

	dc.OnClose(func() {
		p.debugLog.Printf("Data channel closed")
	})

	dc.OnError(func(err error) {
		p.debugLog.Printf("Data channel error: %v", err)
		if p.errorHandler != nil {
			p.errorHandler(fmt.Sprintf("Data channel error: %v", err))
		}
	})
}

func (p *Peer) sendCapabilities() {
	p.debugLog.Printf("Sending capabilities with max chunk size: %d", p.maxChunkSize)

	// Wait a short time to ensure channel is fully established
	time.Sleep(100 * time.Millisecond)

	capabilities := struct {
		Type         string `json:"type"`
		MaxChunkSize int32  `json:"maxChunkSize"`
	}{
		Type:         "capabilities",
		MaxChunkSize: p.maxChunkSize,
	}

	data, err := json.Marshal(capabilities)
	if err != nil {
		p.debugLog.Printf("Error marshaling capabilities: %v", err)
		return
	}

	// Check if channel is ready
	if p.controlChannel.ReadyState() != webrtc.DataChannelStateOpen {
		p.debugLog.Printf("Cannot send capabilities: control channel not open (state: %s)",
			p.controlChannel.ReadyState().String())

		// Try again after a delay
		go func() {
			time.Sleep(500 * time.Millisecond)
			if p.controlChannel != nil && p.controlChannel.ReadyState() == webrtc.DataChannelStateOpen {
				p.debugLog.Printf("Retrying sending capabilities")
				err := p.controlChannel.Send(data)
				if err != nil {
					p.debugLog.Printf("Error sending capabilities on retry: %v", err)
				} else {
					p.debugLog.Printf("Capabilities sent successfully on retry")
				}
			}
		}()
		return
	}

	err = p.controlChannel.Send(data)
	if err != nil {
		p.debugLog.Printf("Error sending capabilities: %v", err)
	} else {
		p.debugLog.Printf("Capabilities sent successfully")
	}
}

// Register connects to the signaling server and gets assigned a token
func (p *Peer) Register(wsURL string) error {
	p.debugLog.Printf("Registering with signaling server: %s", wsURL)

	signaler, err := NewSignaler(wsURL, "", p.debugLog)
	if err != nil {
		return fmt.Errorf("failed to create signaler: %v", err)
	}
	p.signaler = signaler
	p.signaler.SetPeer(p)

	// Wait for token assignment
	if err := p.signaler.WaitForToken(10 * time.Second); err != nil {
		return fmt.Errorf("token assignment failed: %v", err)
	}

	return nil
}

// Connect initiates a connection to a peer
func (p *Peer) Connect(wsURL, token string) error {
	if token == "" {
		return fmt.Errorf("peer token is required")
	}

	p.debugLog.Printf("Connecting to peer: %s", token)

	if p.signaler == nil {
		// Register with server first if not already connected
		if err := p.Register(wsURL); err != nil {
			return fmt.Errorf("failed to register with server: %v", err)
		}
	}

	// Set peer token in signaler
	p.signaler.peerToken = token

	// Send connect request
	err := p.signaler.SendConnectRequest(token)
	if err != nil {
		return fmt.Errorf("failed to send connect request: %v", err)
	}

	// Create data channels before creating offer
	p.createDataChannels()

	// Create offer
	offer, err := p.conn.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("failed to create offer: %v", err)
	}

	// Set local description
	err = p.conn.SetLocalDescription(offer)
	if err != nil {
		return fmt.Errorf("failed to set local description: %v", err)
	}

	// Send offer through signaling server
	err = p.signaler.SendOffer(offer)
	if err != nil {
		return fmt.Errorf("failed to send offer: %v", err)
	}

	return nil
}

// Accept accepts a connection from a peer
func (p *Peer) Accept(wsURL, token string) error {
	if token == "" {
		return fmt.Errorf("peer token is required")
	}

	p.debugLog.Printf("Accepting connection from peer: %s", token)

	if p.signaler == nil {
		// Register with server first if not already connected
		if err := p.Register(wsURL); err != nil {
			return fmt.Errorf("failed to register with server: %v", err)
		}
	}

	// Set peer token in signaler
	p.signaler.peerToken = token

	// Send accept message
	err := p.signaler.SendAccept(token)
	if err != nil {
		return fmt.Errorf("failed to send accept message: %v", err)
	}

	// Create data channels
	p.createDataChannels()

	return nil
}

func (p *Peer) createDataChannels() {
	// Helper function to convert string to *string is no longer needed
	// since we're not setting the Protocol field

	// Create control channel for JSON messages
	// Simplify the configuration to avoid protocol issues
	controlConfig := &webrtc.DataChannelInit{
		ID:         uint16Ptr(1),
		Ordered:    boolPtr(true),
		Negotiated: boolPtr(true),
		// Don't set protocol explicitly to avoid compatibility issues
	}

	p.debugLog.Printf("Creating control channel")
	controlChannel, err := p.conn.CreateDataChannel("p2pftp-control", controlConfig)
	if err != nil {
		p.debugLog.Printf("Failed to create control channel: %v", err)
		return
	}
	p.controlChannel = controlChannel
	p.setupControlChannel(controlChannel)

	// Create data channel for binary data
	// Simplify the configuration to avoid protocol issues
	dataConfig := &webrtc.DataChannelInit{
		ID:         uint16Ptr(2),
		Ordered:    boolPtr(true),
		Negotiated: boolPtr(true),
		// Don't set protocol explicitly to avoid compatibility issues
	}

	p.debugLog.Printf("Creating data channel")
	dataChannel, err := p.conn.CreateDataChannel("p2pftp-data", dataConfig)
	if err != nil {
		p.debugLog.Printf("Failed to create data channel: %v", err)
		return
	}
	p.dataChannel = dataChannel
	p.setupDataChannel(dataChannel)
}

// SendMessage sends a chat message through the control channel
func (p *Peer) SendMessage(msg string) error {
	if !p.IsConnected() {
		return fmt.Errorf("peer connection not established")
	}

	if p.controlChannel == nil {
		return fmt.Errorf("control channel not established")
	}

	// Check if the data channel is open
	if p.controlChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("control channel not open (state: %s)", p.controlChannel.ReadyState().String())
	}

	message := struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}{
		Type:    "message",
		Content: msg,
	}

	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	p.debugLog.Printf("Sending chat message: %s", msg)

	// Try sending as text first
	textData := string(data)
	p.debugLog.Printf("Sending as text: %s", textData)
	err = p.controlChannel.SendText(textData)
	if err != nil {
		p.debugLog.Printf("Failed to send as text, trying binary: %v", err)
		// Fall back to binary if text fails
		err = p.controlChannel.Send(data)
		if err != nil {
			return fmt.Errorf("failed to send message: %v", err)
		}
	}

	p.debugLog.Printf("Message sent successfully")
	return nil
}

// SendControl sends a control message through the control channel
func (p *Peer) SendControl(data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.IsConnected() {
		return fmt.Errorf("peer connection not established")
	}

	if p.controlChannel == nil {
		return fmt.Errorf("control channel not established")
	}

	// Check if the control channel is open
	if p.controlChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("control channel not open (state: %s)", p.controlChannel.ReadyState().String())
	}

	return p.controlChannel.Send(data)
}

// SendData sends binary data through the data channel
func (p *Peer) SendData(data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.IsConnected() {
		return fmt.Errorf("peer connection not established")
	}

	if p.dataChannel == nil {
		return fmt.Errorf("data channel not established")
	}

	// Check if the data channel is open
	if p.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("data channel not open (state: %s)", p.dataChannel.ReadyState().String())
	}

	return p.dataChannel.Send(data)
}

// SetControlHandler sets the handler for control channel messages
func (p *Peer) SetControlHandler(handler func([]byte)) {
	p.controlHandler = handler
}

// SetMessageHandler sets the handler for chat messages
func (p *Peer) SetMessageHandler(handler func(string)) {
	p.messageHandler = handler
}

// SetStatusHandler sets the handler for connection status updates
func (p *Peer) SetStatusHandler(handler func(string)) {
	p.statusHandler = handler
}

// IsConnected returns true if the peer connection is established and ready for data transfer
func (p *Peer) IsConnected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn == nil {
		return false
	}

	// Check both the peer connection state and the ICE connection state
	peerState := p.conn.ConnectionState()
	iceState := p.conn.ICEConnectionState()

	// Connection is considered established if:
	// 1. Peer connection state is Connected AND
	// 2. ICE connection state is Connected or Completed
	return (peerState == webrtc.PeerConnectionStateConnected) &&
		(iceState == webrtc.ICEConnectionStateConnected || iceState == webrtc.ICEConnectionStateCompleted)
}
