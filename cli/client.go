package main

import (
	"github.com/gorilla/websocket"
)

// NewClient creates a new client instance
func NewClient(conn *websocket.Conn) *Client {
	return &Client{
		conn:   conn,
		webrtc: &WebRTCState{},
	}
}

// disconnectPeer cleans up resources when a peer connection is terminated
func (c *Client) disconnectPeer() {
	if c.webrtc.sendTransfer.fileTransfer != nil && c.webrtc.sendTransfer.fileTransfer.file != nil {
		c.webrtc.sendTransfer.fileTransfer.file.Close()
	}
	if c.webrtc.receiveTransfer.fileTransfer != nil && c.webrtc.receiveTransfer.fileTransfer.file != nil {
		c.webrtc.receiveTransfer.fileTransfer.file.Close()
	}
	if c.webrtc.peerConn != nil {
		c.webrtc.peerConn.Close()
		c.webrtc.peerConn = nil
	}
	if c.webrtc.dataChannel != nil {
		c.webrtc.dataChannel.Close()
		c.webrtc.dataChannel = nil
	}
	c.webrtc = &WebRTCState{}
}