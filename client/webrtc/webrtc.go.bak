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
	// Create peer connection configuration
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
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

	// Log state changes
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		peer.debugLog.Printf("ICE Connection State changed: %s", state.String())
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

	return peer, nil
}

func (p *Peer) setupControlChannel(dc *webrtc.DataChannel) {
	dc.OnOpen(func() {
		p.debugLog.Printf("Control channel opened")
		p.sendCapabilities()
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		p.debugLog.Printf("Control message received: %s", string(msg.Data))
		if p.controlHandler != nil {
			p.controlHandler(msg.Data)
		}

		// Parse message to check if it's a chat message
		var msgData map[string]interface{}
		if err := json.Unmarshal(msg.Data, &msgData); err != nil {
			p.debugLog.Printf("Error parsing control message: %v", err)
			return
		}

		msgType, ok := msgData["type"].(string)
		if !ok {
			return
		}

		if msgType == "message" && p.messageHandler != nil {
			content, ok := msgData["content"].(string)
			if ok {
				p.messageHandler(content)
			}
		}
	})

	dc.OnClose(func() {
		p.debugLog.Printf("Control channel closed")
	})

	dc.OnError(func(err error) {
		p.debugLog.Printf("Control channel error: %v", err)
	})
}

func (p *Peer) setupDataChannel(dc *webrtc.DataChannel) {
	dc.OnOpen(func() {
		p.debugLog.Printf("Data channel opened")
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		p.debugLog.Printf("Data channel message received: %d bytes", len(msg.Data))
		if p.dataHandler != nil {
			p.dataHandler(msg.Data)
		}
	})

	dc.OnClose(func() {
		p.debugLog.Printf("Data channel closed")
	})

	dc.OnError(func(err error) {
		p.debugLog.Printf("Data channel error: %v", err)
	})
}

func (p *Peer) sendCapabilities() {
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

	err = p.controlChannel.Send(data)
	if err != nil {
		p.debugLog.Printf("Error sending capabilities: %v", err)
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
	// Create control channel
	controlConfig := &webrtc.DataChannelInit{
		ID:         uint16Ptr(1),
		Ordered:    boolPtr(true),
		Negotiated: boolPtr(true),
	}

	controlChannel, err := p.conn.CreateDataChannel("p2pftp-control", controlConfig)
	if err != nil {
		p.debugLog.Printf("Failed to create control channel: %v", err)
		return
	}
	p.controlChannel = controlChannel
	p.setupControlChannel(controlChannel)

	// Create data channel
	dataChannel, err := p.conn.CreateDataChannel("p2pftp-data", &webrtc.DataChannelInit{
		ID:         uint16Ptr(2),
		Ordered:    boolPtr(true),
		Negotiated: boolPtr(true),
	})
	if err != nil {
		p.debugLog.Printf("Failed to create data channel: %v", err)
		return
	}
	p.dataChannel = dataChannel
	p.setupDataChannel(dataChannel)
}

// SendMessage sends a chat message through the control channel
func (p *Peer) SendMessage(msg string) error {
	if p.controlChannel == nil {
		return fmt.Errorf("control channel not established")
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

	return p.controlChannel.Send(data)
}

// SendControl sends a control message through the control channel
func (p *Peer) SendControl(data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.controlChannel == nil {
		return fmt.Errorf("control channel not established")
	}

	return p.controlChannel.Send(data)
}

// SendData sends binary data through the data channel
func (p *Peer) SendData(data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.dataChannel == nil {
		return fmt.Errorf("data channel not established")
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
