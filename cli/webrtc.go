package main

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/pion/webrtc/v3"
)

// setupPeerConnection creates and configures a new WebRTC peer connection
func (c *Client) setupPeerConnection() error {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{
					"stun:stun.l.google.com:19302",
					"stun:stun1.l.google.com:19302",
				},
			},
		},
	}

	peerConn, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return fmt.Errorf("failed to create peer connection: %v", err)
	}

	peerConn.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		c.ui.LogDebug(fmt.Sprintf("Connection state changed to: %s", state))
		switch state {
		case webrtc.PeerConnectionStateFailed:
			c.ui.ShowError("Connection failed - attempting ICE restart")
			if offer, err := peerConn.CreateOffer(&webrtc.OfferOptions{ICERestart: true}); err == nil {
				if err := peerConn.SetLocalDescription(offer); err == nil {
					offerObj := struct {
						Type string `json:"type"`
						SDP  string `json:"sdp"`
					}{
						Type: offer.Type.String(),
						SDP:  offer.SDP,
					}
					offerJSON, err := json.Marshal(offerObj)
					if err == nil {
						c.SendMessage(Message{
							Type:      "offer",
							PeerToken: c.webrtc.peerToken,
							SDP:       string(offerJSON),
						})
					}
				}
			}
		case webrtc.PeerConnectionStateDisconnected:
			c.ui.LogDebug("Connection disconnected - waiting for reconnection")
		case webrtc.PeerConnectionStateClosed:
			c.ui.LogDebug("Connection closed")
			c.disconnectPeer()
		}
	})

	// Common parameters
	ordered := true
	negotiated := true
	maxRetransmits := uint16(30)

	// Create control channel for metadata (ID: 1)
	controlID := uint16(1)
	controlChannelConfig := &webrtc.DataChannelInit{
		Ordered:        &ordered,
		MaxRetransmits: &maxRetransmits,
		Negotiated:     &negotiated,
		ID:             &controlID,
	}

	controlChannel, err := peerConn.CreateDataChannel("p2pftp-control", controlChannelConfig)
	if err != nil {
		return fmt.Errorf("failed to create control channel: %v", err)
	}

	// Create binary data channel for file transfers (ID: 2)
	dataID := uint16(2)
	dataChannelConfig := &webrtc.DataChannelInit{
		Ordered:        &ordered,
		MaxRetransmits: &maxRetransmits,
		Negotiated:     &negotiated,
		ID:             &dataID,
	}

	dataChannel, err := peerConn.CreateDataChannel("p2pftp-data", dataChannelConfig)
	if err != nil {
		return fmt.Errorf("failed to create data channel: %v", err)
	}

	// Set binary type for the data channel
	dataChannel.SetBufferedAmountLowThreshold(262144) // 256KB

	// Set up control channel handlers
	controlChannel.OnOpen(func() {
		c.ui.LogDebug("Control channel opened")

		// Store the control channel
		c.webrtc.controlChannel = controlChannel

		// Check if both channels are open
		if c.webrtc.dataChannel != nil && c.webrtc.dataChannel.ReadyState() == webrtc.DataChannelStateOpen {
			c.completeConnectionSetup()
		}
	})

	controlChannel.OnClose(func() {
		c.ui.LogDebug("Control channel closed")
		c.disconnectPeer()
	})
	
	// Handle control channel state changes
	controlChannel.OnBufferedAmountLow(func() {
		c.ui.LogDebug("Control channel buffer amount low")
	})
	
	// Add state change handler
	controlChannel.OnError(func(err error) {
		c.ui.LogDebug(fmt.Sprintf("Control channel error: %v", err))
	})

	// Handle control channel messages (JSON)
	controlChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		if !msg.IsString {
			c.ui.ShowError("Unexpected binary message on control channel")
			return
		}

		var data map[string]interface{}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.ui.ShowError(fmt.Sprintf("Failed to parse message: %v", err))
			return
		}

		msgType, ok := data["type"].(string)
		if !ok {
			c.ui.ShowError("Missing message type")
			return
		}

		switch msgType {
		case "message":
			if content, ok := data["content"].(string); ok {
				c.ui.ShowChat(c.webrtc.peerToken, content)
			}
		case "capabilities":
			// Handle capabilities message for chunk size negotiation
			if peerMaxChunkSize, ok := data["maxChunkSize"].(float64); ok {
				c.ui.LogDebug(fmt.Sprintf("Received peer's max chunk size: %d", int(peerMaxChunkSize)))

				// Use the smaller of our max and peer's max
				negotiatedSize := int(math.Min(float64(maxSupportedChunkSize), peerMaxChunkSize))
				maxChunkSize = negotiatedSize
				c.ui.LogDebug(fmt.Sprintf("Negotiated chunk size: %d", negotiatedSize))

				// Send acknowledgment with the negotiated size
				ack := struct {
					Type               string `json:"type"`
					NegotiatedChunkSize int    `json:"negotiatedChunkSize"`
				}{
					Type:               "capabilities-ack",
					NegotiatedChunkSize: negotiatedSize,
				}

				ackJSON, err := json.Marshal(ack)
				if err == nil {
					// Double-check that control channel is still initialized and open
					if c.webrtc.controlChannel != nil {
						if c.webrtc.controlChannel.ReadyState() == webrtc.DataChannelStateOpen {
							err := c.webrtc.controlChannel.SendText(string(ackJSON))
							if err != nil {
								c.ui.LogDebug(fmt.Sprintf("Error sending capabilities acknowledgment: %v", err))
							} else {
								c.ui.LogDebug("Sent capabilities acknowledgment")
							}
						} else {
							c.ui.LogDebug(fmt.Sprintf("Cannot send capabilities acknowledgment: control channel is not open (state: %s)",
								c.webrtc.controlChannel.ReadyState().String()))
						}
					} else {
						c.ui.LogDebug("Cannot send capabilities acknowledgment: control channel is not initialized")
					}
				}
			}
		case "capabilities-ack":
			// Handle capabilities acknowledgment
			if negotiatedSize, ok := data["negotiatedChunkSize"].(float64); ok {
				c.ui.LogDebug(fmt.Sprintf("Peer acknowledged chunk size: %d", int(negotiatedSize)))
				maxChunkSize = int(negotiatedSize)
			}
		case "file-info-update":
			// Handle file info updates (like MD5 hash)
			c.handleFileInfoUpdate(data)
		case "file-info":
			c.handleFileInfo(data)
		case "chunk-info":
			// Handle chunk info for upcoming binary data
			if sequence, ok := data["sequence"].(float64); ok {
				if totalChunks, ok := data["totalChunks"].(float64); ok {
					if size, ok := data["size"].(float64); ok {
						// Store expected chunk info
						c.webrtc.receiveTransfer.expectedChunk = &ChunkInfo{
							Sequence:    int(sequence),
							TotalChunks: int(totalChunks),
							Size:        int(size),
						}
						c.ui.LogDebug(fmt.Sprintf("Expecting chunk %d of size %d bytes", int(sequence), int(size)))
					}
				}
			}
		case "chunk-confirm":
			if sequence, ok := data["sequence"].(float64); ok {
				c.handleChunkConfirm(int(sequence))
			}
		case "file-complete":
			c.handleFileComplete()
		}
	})

	// Set up data channel handlers
	dataChannel.OnOpen(func() {
		c.ui.LogDebug("Data channel opened")

		// Store the data channel
		c.webrtc.dataChannel = dataChannel

		// Check if both channels are open
		if c.webrtc.controlChannel != nil && c.webrtc.controlChannel.ReadyState() == webrtc.DataChannelStateOpen {
			c.completeConnectionSetup()
		}
	})

	dataChannel.OnClose(func() {
		c.ui.LogDebug("Data channel closed")
		c.disconnectPeer()
	})
	
	// Handle data channel state changes
	dataChannel.OnBufferedAmountLow(func() {
		c.ui.LogDebug("Data channel buffer amount low")
	})
	
	// Add state change handler
	dataChannel.OnError(func(err error) {
		c.ui.LogDebug(fmt.Sprintf("Data channel error: %v", err))
	})

	// Handle binary data channel messages
	dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		// We expect binary data on this channel
		if msg.IsString {
			c.ui.ShowError("Unexpected text message on binary channel")
			return
		}

		// Check if we have enough data for the frame header (8 bytes minimum)
		if len(msg.Data) < 8 {
			c.ui.ShowError(fmt.Sprintf("Received binary data too small for frame header: %d bytes", len(msg.Data)))
			return
		}

		// Parse the frame header
		// Format: [4 bytes sequence][4 bytes length][data bytes]
		sequence := int(uint32(msg.Data[0])<<24 | uint32(msg.Data[1])<<16 | uint32(msg.Data[2])<<8 | uint32(msg.Data[3]))
		dataLength := int(uint32(msg.Data[4])<<24 | uint32(msg.Data[5])<<16 | uint32(msg.Data[6])<<8 | uint32(msg.Data[7]))

		// Validate the data length
		if len(msg.Data) != dataLength + 8 {
			c.ui.ShowError(fmt.Sprintf("Frame size mismatch. Header says %d bytes data, got %d bytes total",
				dataLength, len(msg.Data)))
			return
		}

		// Extract the actual data (make a copy to ensure it doesn't get modified)
		// Make sure we only copy exactly dataLength bytes, no more
		data := make([]byte, dataLength)
		copy(data, msg.Data[8:8+dataLength])

		// Log the received chunk
		c.ui.LogDebug(fmt.Sprintf("Received framed binary chunk %d (%d bytes data, %d bytes total)",
			sequence, dataLength, len(msg.Data)))

		// We don't need the expectedChunk anymore since we have the sequence in the frame
		// Just make sure we're in a transfer
		if !c.webrtc.receiveTransfer.inProgress {
			c.ui.ShowError("Received chunk but no download is in progress")
			return
		}

		// Get the total chunks from the transfer state
		totalChunks := c.webrtc.receiveTransfer.totalChunks

		// Process the binary chunk
		c.handleBinaryChunkData(sequence, totalChunks, dataLength, data)
	})

	// Store the peer connection
	c.webrtc.peerConn = peerConn
	
	// Log the initial connection state
	c.ui.LogDebug(fmt.Sprintf("Initial peer connection state: %s", peerConn.ConnectionState().String()))

	peerConn.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}

		candidateJSON, err := json.Marshal(candidate.ToJSON())
		if err != nil {
			c.ui.ShowError(fmt.Sprintf("Failed to marshal ICE candidate: %v", err))
			return
		}

		err = c.SendMessage(Message{
			Type:      "ice",
			PeerToken: c.webrtc.peerToken,
			ICE:       string(candidateJSON),
		})
		if err != nil {
			c.ui.ShowError(fmt.Sprintf("Failed to send ICE candidate: %v", err))
		}
	})

	return nil
}

// handleSDP processes SDP offers and answers for WebRTC connection establishment
func (c *Client) handleSDP(msg Message) {
	var sdpObj struct {
		Type string `json:"type"`
		SDP  string `json:"sdp"`
	}

	if err := json.Unmarshal([]byte(msg.SDP), &sdpObj); err != nil {
		c.ui.ShowError(fmt.Sprintf("Failed to parse SDP: %v", err))
		return
	}

	if msg.Type == "offer" {
		if err := c.setupPeerConnection(); err != nil {
			c.ui.ShowError(fmt.Sprintf("Failed to setup peer connection: %v", err))
			return
		}

		offer := webrtc.SessionDescription{
			Type: webrtc.SDPTypeOffer,
			SDP:  sdpObj.SDP,
		}

		if err := c.webrtc.peerConn.SetRemoteDescription(offer); err != nil {
			c.ui.ShowError(fmt.Sprintf("Failed to set remote description: %v", err))
			return
		}

		answer, err := c.webrtc.peerConn.CreateAnswer(nil)
		if err != nil {
			c.ui.ShowError(fmt.Sprintf("Failed to create answer: %v", err))
			return
		}

		if err := c.webrtc.peerConn.SetLocalDescription(answer); err != nil {
			c.ui.ShowError(fmt.Sprintf("Failed to set local description: %v", err))
			return
		}

		answerJSON, err := json.Marshal(struct {
			Type string `json:"type"`
			SDP  string `json:"sdp"`
		}{
			Type: answer.Type.String(),
			SDP:  answer.SDP,
		})
		if err != nil {
			c.ui.ShowError(fmt.Sprintf("Failed to marshal answer: %v", err))
			return
		}

		err = c.SendMessage(Message{
			Type:      "answer",
			PeerToken: c.webrtc.peerToken,
			SDP:       string(answerJSON),
		})
		if err != nil {
			c.ui.ShowError(fmt.Sprintf("Failed to send answer: %v", err))
		}
	} else if msg.Type == "answer" {
		answer := webrtc.SessionDescription{
			Type: webrtc.SDPTypeAnswer,
			SDP:  sdpObj.SDP,
		}

		if err := c.webrtc.peerConn.SetRemoteDescription(answer); err != nil {
			c.ui.ShowError(fmt.Sprintf("Failed to set remote description: %v", err))
		}
	}
}

// Complete the connection setup when both channels are open
func (c *Client) completeConnectionSetup() {
	// Double-check that both channels are actually initialized
	if c.webrtc.controlChannel == nil || c.webrtc.dataChannel == nil {
		c.ui.LogDebug("Cannot complete connection setup: one or both channels are not initialized")
		return
	}
	
	// Check that both channels are in the open state
	if c.webrtc.controlChannel.ReadyState() != webrtc.DataChannelStateOpen {
		c.ui.LogDebug(fmt.Sprintf("Cannot complete connection setup: control channel is not open (state: %s)",
			c.webrtc.controlChannel.ReadyState().String()))
		return
	}
	
	if c.webrtc.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		c.ui.LogDebug(fmt.Sprintf("Cannot complete connection setup: data channel is not open (state: %s)",
			c.webrtc.dataChannel.ReadyState().String()))
		return
	}
	
	// Check peer connection state
	if c.webrtc.peerConn != nil && c.webrtc.peerConn.ConnectionState() != webrtc.PeerConnectionStateConnected {
		c.ui.LogDebug(fmt.Sprintf("Warning: Peer connection is not in connected state (state: %s)",
			c.webrtc.peerConn.ConnectionState().String()))
	}

	c.webrtc.connected = true
	c.ui.LogDebug(fmt.Sprintf("Both channels ready for transfer. Control: %s, Data: %s",
		c.webrtc.controlChannel.ReadyState().String(), c.webrtc.dataChannel.ReadyState().String()))
	c.ui.ShowConnectionAccepted("")

	// Send capabilities message with our maximum supported chunk size
	capabilities := struct {
		Type         string `json:"type"`
		MaxChunkSize int    `json:"maxChunkSize"`
	}{
		Type:         "capabilities",
		MaxChunkSize: maxSupportedChunkSize,
	}

	capabilitiesJSON, err := json.Marshal(capabilities)
	if err == nil {
		// Double-check that control channel is still initialized and open
		if c.webrtc.controlChannel != nil {
			if c.webrtc.controlChannel.ReadyState() == webrtc.DataChannelStateOpen {
				err := c.webrtc.controlChannel.SendText(string(capabilitiesJSON))
				if err != nil {
					c.ui.LogDebug(fmt.Sprintf("Error sending capabilities: %v", err))
				} else {
					c.ui.LogDebug(fmt.Sprintf("Sent capabilities with max chunk size: %d", maxSupportedChunkSize))
				}
			} else {
				c.ui.LogDebug(fmt.Sprintf("Cannot send capabilities: control channel is not open (state: %s)",
					c.webrtc.controlChannel.ReadyState().String()))
			}
		} else {
			c.ui.LogDebug("Cannot send capabilities: control channel is not initialized")
		}
	}
}