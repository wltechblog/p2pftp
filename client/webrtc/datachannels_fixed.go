package webrtc

import (
	"github.com/pion/webrtc/v3"
)

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

	// Create data channel with optimized configuration for binary data
	dataConfig := &webrtc.DataChannelInit{
		Ordered:        boolPtr(true), // Ordered delivery for reliability
		MaxRetransmits: uint16Ptr(3),  // Limit retransmissions to 3 attempts
	}

	// Create data channel
	p.debugLog.Printf("Creating data channel with optimized configuration for file transfers")
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
