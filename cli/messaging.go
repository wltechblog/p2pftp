package main

import (
	"encoding/json"
	"fmt"

	"github.com/pion/webrtc/v3"
)

// SendChat sends a chat message to the connected peer
func (c *Client) SendChat(text string) error {
	if !c.webrtc.connected {
		return fmt.Errorf("not connected to peer")
	}

	msg := struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}{
		Type:    "message",
		Content: text,
	}

	msgJSON, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	err = c.webrtc.dataChannel.SendText(string(msgJSON))
	if err != nil {
		return fmt.Errorf("failed to send message: %v", err)
	}

	return nil
}

// Connect initiates a connection to a peer using their token
func (c *Client) Connect(peerToken string) error {
	if c.webrtc.connected {
		return fmt.Errorf("already connected to a peer")
	}
	c.webrtc = &WebRTCState{
		peerToken:   peerToken,
		isInitiator: true,
	}
	return c.SendMessage(Message{Type: "connect", PeerToken: peerToken})
}

// Accept accepts a connection request from a peer
func (c *Client) Accept(peerToken string) error {
	if c.webrtc.connected {
		return fmt.Errorf("already connected to a peer")
	}
	c.webrtc = &WebRTCState{peerToken: peerToken, isInitiator: false}
	return c.SendMessage(Message{Type: "accept", PeerToken: peerToken})
}

// Reject rejects a connection request from a peer
func (c *Client) Reject(peerToken string) error {
	if c.webrtc.connected {
		c.disconnectPeer()
	}
	return c.SendMessage(Message{Type: "reject", PeerToken: peerToken})
}

// SendMessage sends a message to the server over the WebSocket connection
func (c *Client) SendMessage(msg Message) error {
	err := c.conn.WriteJSON(msg)
	if err != nil {
		c.ui.ShowError("Send failed: " + err.Error())
		return err
	}
	return nil
}

// handleMessages processes incoming WebSocket messages from the server
func (c *Client) handleMessages() {
	c.ui.LogDebug("Message handler started, waiting for server token...")
	for {
		var msg Message
		err := c.conn.ReadJSON(&msg)
		if err != nil {
			c.ui.LogDebug(fmt.Sprintf("Error reading message: %v", err))
			c.ui.ShowError("Connection error - please try again")
			return
		}

		switch msg.Type {
		case "token":
			c.token = msg.Token
			c.ui.SetToken(msg.Token)

		case "request":
			c.ui.ShowConnectionRequest(msg.Token)
			c.webrtc.peerToken = msg.Token
			c.webrtc.isInitiator = false

		case "accepted":
			if c.webrtc.peerToken == "" {
				c.ui.ShowError("No active connection attempt")
				continue
			}

			if err := c.setupPeerConnection(); err != nil {
				c.ui.ShowError(fmt.Sprintf("Failed to setup peer connection: %v", err))
				continue
			}

			offer, err := c.webrtc.peerConn.CreateOffer(nil)
			if err != nil {
				c.ui.ShowError(fmt.Sprintf("Failed to create offer: %v", err))
				continue
			}

			if err := c.webrtc.peerConn.SetLocalDescription(offer); err != nil {
				c.ui.ShowError(fmt.Sprintf("Failed to set local description: %v", err))
				continue
			}

			offerObj := struct {
				Type string `json:"type"`
				SDP  string `json:"sdp"`
			}{
				Type: offer.Type.String(),
				SDP:  offer.SDP,
			}
			offerJSON, err := json.Marshal(offerObj)
			if err != nil {
				c.ui.ShowError(fmt.Sprintf("Failed to marshal offer: %v", err))
				continue
			}
			
			err = c.SendMessage(Message{
				Type:      "offer",
				PeerToken: c.webrtc.peerToken,
				SDP:       string(offerJSON),
			})
			if err != nil {
				c.ui.ShowError("Failed to send offer")
				continue
			}

		case "offer", "answer":
			c.handleSDP(msg)

		case "ice":
			var candidate webrtc.ICECandidateInit
			if err := json.Unmarshal([]byte(msg.ICE), &candidate); err != nil {
				c.ui.ShowError(fmt.Sprintf("Failed to parse ICE candidate: %v", err))
				continue
			}

			if err := c.webrtc.peerConn.AddICECandidate(candidate); err != nil {
				c.ui.ShowError(fmt.Sprintf("Failed to add ICE candidate: %v", err))
				continue
			}

		case "rejected":
			c.ui.ShowConnectionRejected(msg.Token)
			c.disconnectPeer()

		case "error":
			c.ui.ShowError(msg.SDP)
			c.disconnectPeer()
		}
	}
}