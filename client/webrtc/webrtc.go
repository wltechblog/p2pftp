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

// Chunk size constants as defined in the protocol
const (
	MinChunkSize     int32 = 4096   // 4KB minimum
	DefaultChunkSize int32 = 16384  // 16KB default
	MaxChunkSize     int32 = 262144 // 256KB maximum
)

// Peer represents a WebRTC peer connection
type Peer struct {
	conn                  *webrtc.PeerConnection
	signaler              *Signaler
	controlChannel        *webrtc.DataChannel
	dataChannel           *webrtc.DataChannel
	controlHandler        func([]byte)
	messageHandler        func(string)
	statusHandler         func(string)
	dataHandler           func([]byte)
	debugLog              *log.Logger
	tokenHandler          func(string)
	errorHandler          func(string)
	negotiated            bool
	maxChunkSize          int32 // Maximum supported chunk size
	negotiatedChunkSize   int32 // Negotiated chunk size after capabilities exchange
	capabilitiesExchanged bool  // Whether capabilities have been exchanged
	mu                    sync.Mutex
	iceConnected          bool
	iceTimeout            time.Duration
	pendingOffer          *webrtc.SessionDescription // Stores a pending offer until explicitly accepted
	pendingOfferFrom      string                     // Token of the peer who sent the pending offer
	connectionAccepted    bool                       // Whether the connection has been accepted
	bufferedICECandidates []webrtc.ICECandidateInit  // Buffer for ICE candidates before connection is accepted
	isInitiator           bool                       // Whether this peer initiated the connection
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

// determineOptimalChunkSize determines the optimal chunk size based on WebRTC capabilities
func determineOptimalChunkSize(pc *webrtc.PeerConnection, logger *log.Logger) int32 {
	// Start with the default chunk size from the protocol
	optimalSize := DefaultChunkSize

	// Try to get SCTP transport information if available
	// Note: This might not be available until after connection establishment
	// so we'll implement a more complete detection later in the connection lifecycle

	// For now, use the protocol-defined default
	logger.Printf("Using protocol default chunk size: %d bytes", optimalSize)

	// In a future implementation, we could try to access SCTP transport properties:
	// if pc.SCTP() != nil && pc.SCTP().Transport() != nil {
	//     // Get max message size if available
	//     maxMessageSize := pc.SCTP().Transport().MaxMessageSize()
	//     if maxMessageSize > 0 {
	//         // Subtract header size (8 bytes)
	//         detectedSize := int32(maxMessageSize) - 8
	//         logger.Printf("Detected max message size: %d bytes", detectedSize)
	//
	//         // Apply protocol constraints
	//         if detectedSize < MinChunkSize {
	//             logger.Printf("Detected size %d is below minimum, using %d", detectedSize, MinChunkSize)
	//             return MinChunkSize
	//         }
	//         if detectedSize > MaxChunkSize {
	//             logger.Printf("Detected size %d exceeds maximum, using %d", detectedSize, MaxChunkSize)
	//             return MaxChunkSize
	//         }
	//
	//         return detectedSize
	//     }
	// }

	return optimalSize
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

	// Determine optimal chunk size
	detectedMaxSize := determineOptimalChunkSize(pc, debug)

	peer := &Peer{
		conn:                  pc,
		signaler:              nil,
		controlChannel:        nil,
		dataChannel:           nil,
		controlHandler:        nil,
		messageHandler:        nil,
		statusHandler:         nil,
		dataHandler:           nil,
		tokenHandler:          nil,
		errorHandler:          nil,
		debugLog:              debug,
		negotiated:            false,
		maxChunkSize:          detectedMaxSize,
		negotiatedChunkSize:   DefaultChunkSize, // Start with default, will be negotiated
		capabilitiesExchanged: false,
		mu:                    sync.Mutex{},
		iceConnected:          false,
		iceTimeout:            30 * time.Second, // 30 second timeout for ICE connection
	}

	// Set up data channel handlers
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		peer.debugLog.Printf("New data channel received: %s (ID: %d)", dc.Label(), dc.ID())

		switch dc.Label() {
		case "p2pftp-control":
			peer.debugLog.Printf("Setting up control channel from OnDataChannel")
			peer.controlChannel = dc
			peer.setupControlChannel(dc)
		case "p2pftp-data":
			peer.debugLog.Printf("Setting up data channel from OnDataChannel")
			peer.dataChannel = dc
			peer.setupDataChannel(dc)
		default:
			peer.debugLog.Printf("Received unknown channel label: %s", dc.Label())
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
		// Use our new method to handle ICE candidates
		peer.HandleICECandidate(candidate)
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
		case "capabilities":
			// Handle capabilities message directly
			p.handleCapabilities(msg.Data)
		case "capabilities-ack":
			// Handle capabilities acknowledgment directly
			p.handleCapabilitiesAck(msg.Data)
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
	p.debugLog.Printf("Setting up data channel (ID: %d, Label: %s)", dc.ID(), dc.Label())

	// Set buffer thresholds for better performance
	dc.SetBufferedAmountLowThreshold(65536) // 64KB

	dc.OnBufferedAmountLow(func() {
		p.debugLog.Printf("Data channel buffer low event (ID: %d)", dc.ID())
	})

	dc.OnOpen(func() {
		p.debugLog.Printf("*** DATA CHANNEL OPENED (ID: %d, Label: %s) ***", dc.ID(), dc.Label())
		p.debugLog.Printf("Data channel state: %s", dc.ReadyState().String())
		p.debugLog.Printf("Data channel buffered amount: %d", dc.BufferedAmount())

		// Notify status handler about the data channel being open
		if p.statusHandler != nil {
			p.statusHandler(fmt.Sprintf("Data channel opened (ID: %d, Label: %s)", dc.ID(), dc.Label()))
		}

		// Send a test message to verify the channel is working
		testData := []byte{0, 0, 0, 0, 0, 0, 0, 8, 1, 2, 3, 4, 5, 6, 7, 8}
		p.debugLog.Printf("Sending test message on data channel open")
		err := dc.Send(testData)
		if err != nil {
			p.debugLog.Printf("Failed to send test message: %v", err)
		} else {
			p.debugLog.Printf("Test message sent successfully")
		}
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		// For data channel, we expect binary data
		if msg.IsString {
			p.debugLog.Printf("Warning: Received string data on binary channel (ID: %d): %s", dc.ID(), string(msg.Data))
		} else {
			p.debugLog.Printf("Data channel message received (ID: %d): %d bytes", dc.ID(), len(msg.Data))
			// Print first few bytes for debugging
			if len(msg.Data) > 0 {
				maxBytes := 16
				if len(msg.Data) < maxBytes {
					maxBytes = len(msg.Data)
				}
				p.debugLog.Printf("First %d bytes: %v", maxBytes, msg.Data[:maxBytes])

				// If data starts with a sequence number, extract and log it
				if len(msg.Data) >= 8 {
					sequence := int(msg.Data[0])<<24 | int(msg.Data[1])<<16 | int(msg.Data[2])<<8 | int(msg.Data[3])
					chunkSize := int(msg.Data[4])<<24 | int(msg.Data[5])<<16 | int(msg.Data[6])<<8 | int(msg.Data[7])
					p.debugLog.Printf("Data appears to be chunk %d with size %d", sequence, chunkSize)
				}
			}
		}

		// Pass data to handler regardless of type
		if p.dataHandler != nil {
			p.debugLog.Printf("Calling data handler with %d bytes", len(msg.Data))
			p.dataHandler(msg.Data)
		} else {
			p.debugLog.Printf("No data handler registered to process binary data")
		}
	})

	dc.OnClose(func() {
		p.debugLog.Printf("Data channel closed (ID: %d)", dc.ID())
	})

	dc.OnError(func(err error) {
		p.debugLog.Printf("Data channel error (ID: %d): %v", dc.ID(), err)
		if p.errorHandler != nil {
			p.errorHandler(fmt.Sprintf("Data channel error: %v", err))
		}
	})
}

// handleCapabilities processes a capabilities message from the peer
func (p *Peer) handleCapabilities(data []byte) {
	var capabilities struct {
		Type            string `json:"type"`
		MaxChunkSize    int32  `json:"maxChunkSize"`
		DetectedMaxSize int32  `json:"detectedMaxSize,omitempty"`
	}

	if err := json.Unmarshal(data, &capabilities); err != nil {
		p.debugLog.Printf("Error parsing capabilities: %v", err)
		return
	}

	if capabilities.Type != "capabilities" {
		p.debugLog.Printf("Invalid capabilities message type: %s", capabilities.Type)
		return
	}

	p.debugLog.Printf("Received peer's maximum chunk size: %d", capabilities.MaxChunkSize)

	// Enforce minimum and maximum limits
	peerMaxChunkSize := capabilities.MaxChunkSize
	if peerMaxChunkSize < MinChunkSize {
		p.debugLog.Printf("Peer's max chunk size %d is below minimum %d, using minimum",
			peerMaxChunkSize, MinChunkSize)
		peerMaxChunkSize = MinChunkSize
	}
	if peerMaxChunkSize > MaxChunkSize {
		p.debugLog.Printf("Peer's max chunk size %d exceeds maximum %d, using maximum",
			peerMaxChunkSize, MaxChunkSize)
		peerMaxChunkSize = MaxChunkSize
	}

	// Use the smaller of our max and peer's max
	negotiatedSize := p.maxChunkSize
	if peerMaxChunkSize < negotiatedSize {
		negotiatedSize = peerMaxChunkSize
	}

	p.debugLog.Printf("Negotiated chunk size: %d", negotiatedSize)
	p.negotiatedChunkSize = negotiatedSize

	// Send acknowledgment
	ack := struct {
		Type                string `json:"type"`
		NegotiatedChunkSize int32  `json:"negotiatedChunkSize"`
	}{
		Type:                "capabilities-ack",
		NegotiatedChunkSize: negotiatedSize,
	}

	data, err := json.Marshal(ack)
	if err != nil {
		p.debugLog.Printf("Error marshaling capabilities-ack: %v", err)
		return
	}

	err = p.controlChannel.Send(data)
	if err != nil {
		p.debugLog.Printf("Error sending capabilities-ack: %v", err)
		return
	}

	// Mark capabilities as exchanged
	p.capabilitiesExchanged = true
	p.debugLog.Printf("Capabilities exchange completed, using chunk size: %d", p.negotiatedChunkSize)
}

// handleCapabilitiesAck processes a capabilities acknowledgment from the peer
func (p *Peer) handleCapabilitiesAck(data []byte) {
	var ack struct {
		Type                string `json:"type"`
		NegotiatedChunkSize int32  `json:"negotiatedChunkSize"`
	}

	if err := json.Unmarshal(data, &ack); err != nil {
		p.debugLog.Printf("Error parsing capabilities-ack: %v", err)
		return
	}

	if ack.Type != "capabilities-ack" {
		p.debugLog.Printf("Invalid capabilities-ack message type: %s", ack.Type)
		return
	}

	p.debugLog.Printf("Received capabilities acknowledgment with negotiated chunk size: %d",
		ack.NegotiatedChunkSize)

	// Enforce minimum and maximum limits
	negotiatedSize := ack.NegotiatedChunkSize
	if negotiatedSize < MinChunkSize {
		p.debugLog.Printf("Negotiated chunk size %d is below minimum %d, using minimum",
			negotiatedSize, MinChunkSize)
		negotiatedSize = MinChunkSize
	}
	if negotiatedSize > MaxChunkSize {
		p.debugLog.Printf("Negotiated chunk size %d exceeds maximum %d, using maximum",
			negotiatedSize, MaxChunkSize)
		negotiatedSize = MaxChunkSize
	}

	p.negotiatedChunkSize = negotiatedSize

	// Mark capabilities as exchanged
	p.capabilitiesExchanged = true
	p.debugLog.Printf("Capabilities exchange completed, using chunk size: %d", p.negotiatedChunkSize)
}

func (p *Peer) enforceCapabilitiesTimeout() {
	// Wait for 5 seconds as specified in the protocol
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	select {
	case <-timer.C:
		// Check if capabilities have been exchanged
		if !p.capabilitiesExchanged {
			p.debugLog.Printf("ERROR: No capabilities exchange occurred within 5 seconds")
			// Notify the user
			if p.statusHandler != nil {
				p.statusHandler("Connection failed: No capabilities exchange within timeout period")
			}
			// Close the connection
			p.Close()
		}
	}
}

func (p *Peer) sendCapabilities() {
	p.debugLog.Printf("Sending capabilities with max chunk size: %d", p.maxChunkSize)

	// Wait a short time to ensure channel is fully established
	time.Sleep(100 * time.Millisecond)

	// Start a timeout for capabilities exchange
	go p.enforceCapabilitiesTimeout()

	capabilities := struct {
		Type            string `json:"type"`
		MaxChunkSize    int32  `json:"maxChunkSize"`
		DetectedMaxSize int32  `json:"detectedMaxSize"`
	}{
		Type:            "capabilities",
		MaxChunkSize:    p.maxChunkSize,
		DetectedMaxSize: p.maxChunkSize, // Currently the same, but could differ in future implementations
	}

	data, err := json.Marshal(capabilities)
	if err != nil {
		p.debugLog.Printf("Error marshaling capabilities: %v", err)
		return
	}

	// Helper function to send with timeout
	sendWithTimeout := func(isRetry bool) {
		// Create a channel to signal completion
		done := make(chan error, 1)

		// Send the message in a goroutine with timeout
		go func() {
			if isRetry {
				p.debugLog.Printf("Starting to send capabilities on retry...")
			} else {
				p.debugLog.Printf("Starting to send capabilities...")
			}

			err := p.controlChannel.Send(data)

			if isRetry {
				p.debugLog.Printf("Send capabilities on retry completed with error: %v", err)
			} else {
				p.debugLog.Printf("Send capabilities completed with error: %v", err)
			}

			done <- err
		}()

		// Wait for the send operation to complete with a timeout
		select {
		case err := <-done:
			if err != nil {
				if isRetry {
					p.debugLog.Printf("Error sending capabilities on retry: %v", err)
				} else {
					p.debugLog.Printf("Error sending capabilities: %v", err)
				}
			} else {
				if isRetry {
					p.debugLog.Printf("Capabilities sent successfully on retry")
				} else {
					p.debugLog.Printf("Capabilities sent successfully")
				}
			}
		case <-time.After(5 * time.Second):
			if isRetry {
				p.debugLog.Printf("Send capabilities on retry timed out after 5 seconds")
			} else {
				p.debugLog.Printf("Send capabilities timed out after 5 seconds")
			}
		}
	}

	// Check if channel is ready
	if p.controlChannel == nil || p.controlChannel.ReadyState() != webrtc.DataChannelStateOpen {
		p.debugLog.Printf("Cannot send capabilities: control channel not open or nil (state: %s)",
			p.controlChannel.ReadyState().String())

		// Try again after a delay
		go func() {
			time.Sleep(500 * time.Millisecond)
			if p.controlChannel != nil && p.controlChannel.ReadyState() == webrtc.DataChannelStateOpen {
				p.debugLog.Printf("Retrying sending capabilities")
				sendWithTimeout(true)
			}
		}()
		return
	}

	// Send capabilities
	sendWithTimeout(false)
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

	// Set this peer as the initiator
	p.mu.Lock()
	p.isInitiator = true
	p.mu.Unlock()

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

	// Create data channels
	p.createDataChannels()

	// Notify the user that the connection request has been sent
	if p.statusHandler != nil {
		p.statusHandler(fmt.Sprintf("Connection request sent to %s. Waiting for acceptance...", token))
	}

	// We'll create and send the offer only after the peer accepts the connection
	// This will be handled in the signaler's "accepted" message handler

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

	// Mark the connection as accepted
	p.mu.Lock()
	p.connectionAccepted = true
	p.mu.Unlock()

	// Send accept message
	err := p.signaler.SendAccept(token)
	if err != nil {
		return fmt.Errorf("failed to send accept message: %v", err)
	}

	// Create data channels
	p.createDataChannels()

	// Check if we have a pending offer from this peer
	p.mu.Lock()
	pendingOffer := p.pendingOffer
	pendingOfferFrom := p.pendingOfferFrom
	p.mu.Unlock()

	if pendingOffer != nil && pendingOfferFrom == token {
		p.debugLog.Printf("Processing pending offer from %s", token)

		// Process the pending offer
		if err := p.processPendingOffer(); err != nil {
			return fmt.Errorf("failed to process pending offer: %v", err)
		}
	}

	return nil
}

// processPendingOffer processes a stored offer after explicit acceptance
func (p *Peer) processPendingOffer() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.pendingOffer == nil {
		return fmt.Errorf("no pending offer to process")
	}

	p.debugLog.Printf("Processing pending offer from: %s", p.pendingOfferFrom)

	// Set remote description
	if err := p.conn.SetRemoteDescription(*p.pendingOffer); err != nil {
		return fmt.Errorf("error setting remote description: %v", err)
	}

	// Create answer
	answer, err := p.conn.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("error creating answer: %v", err)
	}

	// Set local description
	if err := p.conn.SetLocalDescription(answer); err != nil {
		return fmt.Errorf("error setting local description: %v", err)
	}

	// Send answer
	if err := p.signaler.SendAnswer(answer); err != nil {
		return fmt.Errorf("error sending answer: %v", err)
	}

	// Update connection status
	if p.statusHandler != nil {
		p.statusHandler("Connection established with " + p.pendingOfferFrom)
	}

	// Clear the pending offer
	p.pendingOffer = nil
	p.pendingOfferFrom = ""

	return nil
}

// HandleOffer processes an offer from a peer
// If the connection has already been accepted, it processes the offer immediately
// Otherwise, it stores the offer for later processing when Accept is called
func (p *Peer) HandleOffer(token string, offer webrtc.SessionDescription) {
	p.mu.Lock()
	connectionAccepted := p.connectionAccepted
	p.mu.Unlock()

	if connectionAccepted {
		// If the connection has already been accepted, process the offer immediately
		p.debugLog.Printf("Connection already accepted, processing offer from %s immediately", token)

		// Set remote description
		if err := p.conn.SetRemoteDescription(offer); err != nil {
			p.debugLog.Printf("Error setting remote description: %v", err)
			return
		}

		// Create answer
		answer, err := p.conn.CreateAnswer(nil)
		if err != nil {
			p.debugLog.Printf("Error creating answer: %v", err)
			return
		}

		// Set local description
		if err := p.conn.SetLocalDescription(answer); err != nil {
			p.debugLog.Printf("Error setting local description: %v", err)
			return
		}

		// Send answer
		if err := p.signaler.SendAnswer(answer); err != nil {
			p.debugLog.Printf("Error sending answer: %v", err)
			return
		}

		// Update connection status
		if p.statusHandler != nil {
			p.statusHandler(fmt.Sprintf("Connection established with %s", token))
		}
	} else {
		// Otherwise, store the offer for later processing when Accept is called
		p.mu.Lock()
		defer p.mu.Unlock()

		p.debugLog.Printf("Storing offer from %s for later acceptance", token)
		p.pendingOffer = &offer
		p.pendingOfferFrom = token

		// Notify the user about the pending connection
		if p.statusHandler != nil {
			p.statusHandler(fmt.Sprintf("Connection offer received from: %s. Use 'accept %s' to accept.",
				token, token))
		}
	}
}

// HandleICECandidate processes an ICE candidate
// If the connection is not yet accepted and this peer is the initiator,
// the candidate will be buffered until the connection is accepted
func (p *Peer) HandleICECandidate(candidate *webrtc.ICECandidate) {
	if candidate == nil {
		p.debugLog.Printf("ICE gathering completed")
		return
	}

	p.debugLog.Printf("New ICE candidate: %s", candidate.String())
	candidateInit := candidate.ToJSON()

	p.mu.Lock()
	defer p.mu.Unlock()

	// If connection is accepted or this peer is not the initiator, send ICE candidates immediately
	// Otherwise, buffer them until the connection is accepted
	if p.connectionAccepted || !p.isInitiator {
		if p.signaler != nil {
			p.debugLog.Printf("Sending ICE candidate immediately")
			err := p.signaler.SendICE(candidateInit)
			if err != nil {
				p.debugLog.Printf("Failed to send ICE candidate: %v", err)
			}
		} else {
			p.debugLog.Printf("Cannot send ICE candidate: signaler not initialized")
		}
	} else {
		// Buffer the ICE candidate for later
		p.debugLog.Printf("Buffering ICE candidate until connection is accepted")
		p.bufferedICECandidates = append(p.bufferedICECandidates, candidateInit)
	}
}

// SendBufferedICECandidates sends any buffered ICE candidates
func (p *Peer) SendBufferedICECandidates() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.bufferedICECandidates) == 0 {
		p.debugLog.Printf("No buffered ICE candidates to send")
		return
	}

	p.debugLog.Printf("Sending %d buffered ICE candidates", len(p.bufferedICECandidates))

	if p.signaler == nil {
		p.debugLog.Printf("Cannot send buffered ICE candidates: signaler not initialized")
		return
	}

	for _, candidate := range p.bufferedICECandidates {
		err := p.signaler.SendICE(candidate)
		if err != nil {
			p.debugLog.Printf("Failed to send buffered ICE candidate: %v", err)
		}
	}

	// Clear the buffer after sending
	p.bufferedICECandidates = nil
}

/* Commented out due to duplicate implementation in datachannels_fixed.go
func (p *Peer) createDataChannels() {
	// Create control channel with reliable configuration
	controlConfig := &webrtc.DataChannelInit{
		Ordered: boolPtr(true), // Ordered delivery
	}

	// Create control channel
	p.debugLog.Printf("Creating control data channel with ordered delivery")
	controlChannel, err := p.conn.CreateDataChannel("p2pftp-control", controlConfig)
	if err != nil {
		p.debugLog.Printf("Failed to create control channel: %v", err)
		return
	}
	p.debugLog.Printf("Control channel created with ID: %d", controlChannel.ID())
	p.controlChannel = controlChannel

	// Set up the control channel
	p.setupControlChannel(controlChannel)

	// Create data channel with reliable configuration
	// Use ordered delivery for reliability and set protocol to support large messages
	dataConfig := &webrtc.DataChannelInit{
		Ordered:           boolPtr(true),     // Ordered delivery for reliability
		MaxPacketLifeTime: uint16Ptr(10000),  // 10 seconds lifetime
		Protocol:          "binary-transfer", // Custom protocol to indicate binary transfer
	}

	// Create data channel
	p.debugLog.Printf("Creating data channel with ordered delivery for reliability")
	dataChannel, err := p.conn.CreateDataChannel("p2pftp-data", dataConfig)
	if err != nil {
		p.debugLog.Printf("Failed to create data channel: %v", err)
		return
	}
	p.debugLog.Printf("Data channel created with ID: %d", dataChannel.ID())
	p.dataChannel = dataChannel

	// Set up the data channel
	p.setupDataChannel(dataChannel)
}
*/

// SendMessage sends a chat message through the data channel
// This is a completely new implementation that uses a non-blocking approach
func (p *Peer) SendMessage(msg string) error {
	p.debugLog.Printf("SendMessage called with message: %s", msg)

	// Lock the mutex for the initial checks
	p.mu.Lock()

	// Basic checks for control channel (for chat messages)
	if p.controlChannel == nil {
		p.mu.Unlock()
		p.debugLog.Printf("Cannot send message - control channel not established")
		return fmt.Errorf("control channel not established")
	}

	if p.controlChannel.ReadyState() != webrtc.DataChannelStateOpen {
		p.mu.Unlock()
		p.debugLog.Printf("Cannot send message - control channel not open (state: %s)",
			p.controlChannel.ReadyState().String())
		return fmt.Errorf("control channel not open (state: %s)", p.controlChannel.ReadyState().String())
	}

	// Get a reference to the control channel while still holding the lock
	controlChannel := p.controlChannel

	// Unlock the mutex before the send operation
	p.mu.Unlock()

	// Create a simple text message
	message := struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}{
		Type:    "message",
		Content: msg,
	}

	data, err := json.Marshal(message)
	if err != nil {
		p.debugLog.Printf("Failed to marshal message: %v", err)
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	p.debugLog.Printf("Sending chat message: %s", msg)

	// Use a non-blocking approach - send in background and return immediately
	go func() {
		textData := string(data)
		p.debugLog.Printf("Sending message as text in background: %s", textData)

		// Try SendText first
		err := controlChannel.SendText(textData)
		if err != nil {
			p.debugLog.Printf("SendText failed: %v, trying Send...", err)

			// If SendText fails, try Send
			err = controlChannel.Send(data)
			if err != nil {
				p.debugLog.Printf("Send also failed: %v", err)
			} else {
				p.debugLog.Printf("Message sent successfully using Send")
			}
		} else {
			p.debugLog.Printf("Message sent successfully using SendText")
		}
	}()

	// Return immediately without waiting for the send to complete
	// This keeps the UI responsive even if the WebRTC implementation is slow
	p.debugLog.Printf("Returning from SendMessage (message sending in background)")
	return nil
}

// SendControl sends a control message through the data channel
// This is a completely new implementation that uses a non-blocking approach
func (p *Peer) SendControl(data []byte) error {
	// Lock the mutex for the initial checks
	p.mu.Lock()

	// Basic checks
	if p.controlChannel == nil {
		p.mu.Unlock()
		p.debugLog.Printf("Cannot send control message - control channel not established")
		return fmt.Errorf("control channel not established")
	}

	if p.controlChannel.ReadyState() != webrtc.DataChannelStateOpen {
		p.mu.Unlock()
		p.debugLog.Printf("Cannot send control message - control channel not open (state: %s)",
			p.controlChannel.ReadyState().String())
		return fmt.Errorf("control channel not open (state: %s)", p.controlChannel.ReadyState().String())
	}

	// Get a reference to the control channel while still holding the lock
	controlChannel := p.controlChannel

	// Unlock the mutex before the send operation
	p.mu.Unlock()

	p.debugLog.Printf("Sending control message: %d bytes", len(data))

	// Use a non-blocking approach - send in background and return immediately
	go func() {
		p.debugLog.Printf("Sending control message in background")

		// Try Send
		err := controlChannel.Send(data)
		if err != nil {
			p.debugLog.Printf("Send failed: %v, trying SendText...", err)

			// If Send fails, try SendText
			err = controlChannel.SendText(string(data))
			if err != nil {
				p.debugLog.Printf("SendText also failed: %v", err)
			} else {
				p.debugLog.Printf("Control message sent successfully using SendText")
			}
		} else {
			p.debugLog.Printf("Control message sent successfully using Send")
		}
	}()

	// Return immediately without waiting for the send to complete
	// This keeps the UI responsive even if the WebRTC implementation is slow
	p.debugLog.Printf("Returning from SendControl (message sending in background)")
	return nil
}

/* Commented out due to duplicate implementation in senddata_fixed.go
// SendData sends binary data through the data channel
func (p *Peer) SendData(data []byte) error {
	// Only lock the mutex for the initial checks, not for the entire send operation
	p.mu.Lock()

	p.debugLog.Printf("SendData called with %d bytes", len(data))

	if !p.IsConnected() {
		p.mu.Unlock()
		p.debugLog.Printf("Cannot send data: peer connection not established")
		return fmt.Errorf("peer connection not established")
	}

	if p.dataChannel == nil {
		p.mu.Unlock()
		p.debugLog.Printf("Cannot send data: data channel not established (nil)")
		return fmt.Errorf("data channel not established")
	}

	// Check if the data channel is open
	state := p.dataChannel.ReadyState()
	p.debugLog.Printf("Data channel state: %s", state.String())

	if state != webrtc.DataChannelStateOpen {
		p.mu.Unlock()
		p.debugLog.Printf("Cannot send data: data channel not open (state: %s)", state.String())
		return fmt.Errorf("data channel not open (state: %s)", state.String())
	}

	// Get a reference to the data channel while still holding the lock
	dataChannel := p.dataChannel

	// Check if the data size is too large for the data channel
	// WebRTC has a practical limit around 16KB for reliable transmission
	maxSize := 16384 // 16KB is a safe limit for most WebRTC implementations

	// If data is larger than the safe limit, fall back to control channel
	if len(data) > maxSize {
		p.mu.Unlock()
		p.debugLog.Printf("Data size %d exceeds safe WebRTC limit of %d bytes, falling back to control channel",
			len(data), maxSize)

		// Extract sequence number from the first 4 bytes
		sequence := -1
		if len(data) >= 4 {
			sequence = int(data[0])<<24 | int(data[1])<<16 | int(data[2])<<8 | int(data[3])
		}

		// Extract chunk data (skip the 8-byte header)
		chunkData := data[8:]

		// Create a control message with the chunk data
		chunkMsg := map[string]interface{}{
			"type":     "file-chunk",
			"sequence": sequence,
			"size":     len(chunkData),
			"data":     base64.StdEncoding.EncodeToString(chunkData),
		}

		// Convert to JSON
		chunkJSON, err := json.Marshal(chunkMsg)
		if err != nil {
			return fmt.Errorf("error marshaling chunk data: %v", err)
		}

		// Send via control channel
		return p.SendControl(chunkJSON)
	}

	// Unlock the mutex before the send operation to allow other sends to proceed
	p.mu.Unlock()

	// Extract sequence number from the first 4 bytes for better logging
	sequence := -1
	if len(data) >= 4 {
		sequence = int(data[0])<<24 | int(data[1])<<16 | int(data[2])<<8 | int(data[3])
		p.debugLog.Printf("Sending chunk with sequence number: %d", sequence)
	}

	// Send directly without a goroutine for the first attempt
	p.debugLog.Printf("Starting to send data message of %d bytes (sequence: %d)...", len(data), sequence)

	// Try to send the data
	err := dataChannel.Send(data)

	if err != nil {
		p.debugLog.Printf("Failed to send data message (sequence: %d): %v", sequence, err)

		// If we get an error, try sending via control channel as fallback
		p.debugLog.Printf("Falling back to control channel for sequence: %d", sequence)

		// Extract chunk data (skip the 8-byte header)
		chunkData := data[8:]

		// Create a control message with the chunk data
		chunkMsg := map[string]interface{}{
			"type":     "file-chunk",
			"sequence": sequence,
			"size":     len(chunkData),
			"data":     base64.StdEncoding.EncodeToString(chunkData),
		}

		// Convert to JSON
		chunkJSON, err := json.Marshal(chunkMsg)
		if err != nil {
			return fmt.Errorf("error marshaling chunk data: %v", err)
		}

		// Send via control channel
		return p.SendControl(chunkJSON)
	} else {
		p.debugLog.Printf("Data message sent successfully (sequence: %d)", sequence)
	}

	// Return immediately without waiting for the send to complete
	// This keeps the UI responsive even if the WebRTC implementation is slow
	p.debugLog.Printf("Returning from SendData (message sending in background)")
	return nil
}
*/

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

// GetNegotiatedChunkSize returns the negotiated chunk size for file transfers
func (p *Peer) GetNegotiatedChunkSize() int32 {
	p.mu.Lock()
	defer p.mu.Unlock()

	// If capabilities have been exchanged, return the negotiated size
	if p.capabilitiesExchanged {
		return p.negotiatedChunkSize
	}

	// Otherwise return the default size
	return DefaultChunkSize
}

// IsDataChannelOpen returns whether the data channel is open and ready for use
func (p *Peer) IsDataChannelOpen() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.dataChannel != nil && p.dataChannel.ReadyState() == webrtc.DataChannelStateOpen
}

// StoreRequestToken stores the most recent connection request token
func (p *Peer) StoreRequestToken(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Store the token in the CLI instance
	if p.statusHandler != nil {
		// Use a special format that the CLI can parse to extract the token
		p.statusHandler(fmt.Sprintf("__STORE_REQUEST_TOKEN__:%s", token))
	}
}

// Close closes the WebRTC peer connection and cleans up resources
func (p *Peer) Close() {
	p.debugLog.Printf("Closing peer connection")

	// Close data channels if they exist
	if p.dataChannel != nil {
		p.debugLog.Printf("Closing data channel")
		if err := p.dataChannel.Close(); err != nil {
			p.debugLog.Printf("Error closing data channel: %v", err)
		}
	}

	if p.controlChannel != nil {
		p.debugLog.Printf("Closing control channel")
		if err := p.controlChannel.Close(); err != nil {
			p.debugLog.Printf("Error closing control channel: %v", err)
		}
	}

	// Close the peer connection
	if p.conn != nil {
		p.debugLog.Printf("Closing peer connection")
		if err := p.conn.Close(); err != nil {
			p.debugLog.Printf("Error closing peer connection: %v", err)
		}
	}

	// Close the signaler if it exists
	if p.signaler != nil {
		p.debugLog.Printf("Closing signaler")
		p.signaler.Close()
	}
}
