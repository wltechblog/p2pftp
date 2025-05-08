package webrtc

import (
	"fmt"

	"github.com/pion/webrtc/v3"
)

// SendData sends binary data through the data channel
func (p *Peer) SendData(data []byte) error {
	// Only lock the mutex for the initial checks, not for the entire send operation
	p.mu.Lock()

	p.debugLog.Printf("SendData called with %d bytes", len(data))

	// Check connection state directly instead of calling IsConnected() to avoid deadlock
	if p.conn == nil {
		p.mu.Unlock()
		p.debugLog.Printf("Cannot send data: peer connection is nil")
		return fmt.Errorf("peer connection not established")
	}

	// Check connection state
	peerState := p.conn.ConnectionState()
	iceState := p.conn.ICEConnectionState()
	if peerState != webrtc.PeerConnectionStateConnected ||
		(iceState != webrtc.ICEConnectionStateConnected && iceState != webrtc.ICEConnectionStateCompleted) {
		p.mu.Unlock()
		p.debugLog.Printf("Cannot send data: peer connection not established (state: %s, ICE: %s)",
			peerState.String(), iceState.String())
		return fmt.Errorf("peer connection not established (state: %s, ICE: %s)",
			peerState.String(), iceState.String())
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
	// WebRTC has a practical limit around 8KB for reliable transmission
	maxSize := 8192 // 8KB is a safer limit for WebRTC data channels

	if len(data) > maxSize {
		p.mu.Unlock()
		p.debugLog.Printf("Data size %d exceeds safe WebRTC limit of %d bytes", len(data), maxSize)
		return fmt.Errorf("data size %d exceeds maximum safe size of %d bytes", len(data), maxSize)
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
		return fmt.Errorf("failed to send data: %v", err)
	}

	p.debugLog.Printf("Data message sent successfully (sequence: %d)", sequence)

	// Return immediately without waiting for the send to complete
	// This keeps the UI responsive even if the WebRTC implementation is slow
	p.debugLog.Printf("Returning from SendData (message sending in background)")
	return nil
}
