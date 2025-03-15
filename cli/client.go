package main

import (
	"fmt"

	"github.com/gorilla/websocket"
)

// NewClient creates a new client instance
func NewClient(conn *websocket.Conn) *Client {
	// Initialize with a properly structured WebRTC state
	return &Client{
		conn: conn,
		webrtc: &WebRTCState{
			connected:       false,
			sendTransfer:    transferState{},
			receiveTransfer: transferState{},
		},
	}
}

// disconnectPeer cleans up resources when a peer connection is terminated
func (c *Client) disconnectPeer() {
	// Save the peer token in case we need it
	peerToken := c.webrtc.peerToken
	
	// Close file handles if they exist
	if c.webrtc.sendTransfer.fileTransfer != nil && c.webrtc.sendTransfer.fileTransfer.file != nil {
		c.webrtc.sendTransfer.fileTransfer.file.Close()
	}
	if c.webrtc.receiveTransfer.fileTransfer != nil && c.webrtc.receiveTransfer.fileTransfer.file != nil {
		c.webrtc.receiveTransfer.fileTransfer.file.Close()
	}
	
	// Close WebRTC connections
	if c.webrtc.peerConn != nil {
		// Log the connection state before closing
		c.ui.LogDebug(fmt.Sprintf("Closing peer connection (state: %s)", c.webrtc.peerConn.ConnectionState().String()))
		c.webrtc.peerConn.Close()
		c.webrtc.peerConn = nil
	}
	
	// Close control channel
	if c.webrtc.controlChannel != nil {
		// Log the channel state before closing
		c.ui.LogDebug(fmt.Sprintf("Closing control channel (state: %s)", c.webrtc.controlChannel.ReadyState().String()))
		c.webrtc.controlChannel.Close()
		c.webrtc.controlChannel = nil
	}
	
	// Close data channel
	if c.webrtc.dataChannel != nil {
		// Log the channel state before closing
		c.ui.LogDebug(fmt.Sprintf("Closing data channel (state: %s)", c.webrtc.dataChannel.ReadyState().String()))
		c.webrtc.dataChannel.Close()
		c.webrtc.dataChannel = nil
	}
	
	// Reset the WebRTC state with proper initialization
	c.webrtc = &WebRTCState{
		peerToken:      peerToken, // Keep the peer token for reference
		connected:      false,
		sendTransfer:   transferState{},
		receiveTransfer: transferState{},
	}
	
	c.ui.LogDebug("Peer connection cleaned up")
}