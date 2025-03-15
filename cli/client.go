package main

import (
	"fmt"

	"github.com/gorilla/websocket"

	"github.com/wltechblog/p2pftp/cli/transfer"
	"github.com/wltechblog/p2pftp/cli/webrtc"
)

// Use constants from config.go

// Client represents the P2PFTP client
type Client struct {
	conn            *websocket.Conn
	token           string
	ui              UserInterface
	webrtcConn      *webrtc.Connection
	webrtcSignaling *webrtc.Signaling
	webrtcChannels  *webrtc.Channels
	sender          *transfer.Sender
	receiver        *transfer.Receiver
}

// Using UserInterface and Message types from the main package

// NewClient creates a new client instance
func NewClient(conn *websocket.Conn) *Client {
	return &Client{
		conn: conn,
	}
}

// SetUI sets the UI for the client
func (c *Client) SetUI(ui UserInterface) {
	c.ui = ui
}

// SendMessage sends a message to the server
func (c *Client) SendMessage(msg Message) error {
	err := c.conn.WriteJSON(msg)
	if err != nil {
		c.ui.ShowError("Send failed: " + err.Error())
		return err
	}
	return nil
}

// SendSignalingMessage sends a signaling message to the server
func (c *Client) SendSignalingMessage(msg webrtc.SignalingMessage) error {
	// Convert to our Message type
	message := Message{
		Type:      msg.Type,
		Token:     msg.Token,
		PeerToken: msg.PeerToken,
		SDP:       msg.SDP,
		ICE:       msg.ICE,
	}
	
	// Send the message
	return c.SendMessage(message)
}

// Connect initiates a connection to a peer
func (c *Client) Connect(peerToken string) error {
	// Initialize WebRTC components
	c.initWebRTC(peerToken, true)
	
	// Send connect message to server
	return c.SendMessage(Message{
		Type:      "connect",
		PeerToken: peerToken,
	})
}

// Accept accepts a connection request
func (c *Client) Accept(peerToken string) error {
	// Initialize WebRTC components
	c.initWebRTC(peerToken, false)
	
	// Send accept message to server
	return c.SendMessage(Message{
		Type:      "accept",
		PeerToken: peerToken,
	})
}

// Reject rejects a connection request
func (c *Client) Reject(peerToken string) error {
	// Clean up any existing connection
	c.Disconnect()
	
	// Send reject message to server
	return c.SendMessage(Message{
		Type:      "reject",
		PeerToken: peerToken,
	})
}

// SendChat sends a chat message
func (c *Client) SendChat(text string) error {
	if c.webrtcChannels == nil {
		return fmt.Errorf("not connected to peer")
	}
	
	return c.webrtcChannels.SendChatMessage(text)
}

// SendFile sends a file to the peer
func (c *Client) SendFile(path string) error {
	if c.sender == nil {
		return fmt.Errorf("not connected to peer")
	}
	
	return c.sender.SendFile(path)
}

// Disconnect disconnects from the peer
func (c *Client) Disconnect() error {
	if c.webrtcConn != nil {
		c.webrtcConn.Disconnect()
		c.webrtcConn = nil
		c.webrtcSignaling = nil
		c.webrtcChannels = nil
		c.sender = nil
		c.receiver = nil
	}
	
	return nil
}

// initWebRTC initializes the WebRTC components
func (c *Client) initWebRTC(peerToken string, isInitiator bool) {
	// Create WebRTC connection
	c.webrtcConn = webrtc.NewConnection(
		c.ui,
		func() {
			c.ui.ShowConnectionAccepted("")
		},
		262144, // fixedChunkSize from config.go
		262144, // maxWebRTCMessageSize from config.go
	)
	
	// Create WebRTC signaling
	c.webrtcSignaling = webrtc.NewSignaling(
		c.webrtcConn,
		c,
		c.ui,
	)
	
	// Create sender and receiver
	c.sender = transfer.NewSender(
		c.webrtcConn.GetControlChannel(),
		c.webrtcConn.GetDataChannel(),
		c.ui,
		func(status string, direction string) {
			c.ui.UpdateTransferProgress(status, direction)
		},
		262144, // fixedChunkSize from config.go
		262144, // maxWebRTCMessageSize from config.go
	)
	
	c.receiver = transfer.NewReceiver(
		c.webrtcConn.GetControlChannel(),
		c.webrtcConn.GetDataChannel(),
		c.ui,
		func(status string, direction string) {
			c.ui.UpdateTransferProgress(status, direction)
		},
		262144, // fixedChunkSize from config.go
	)
	
	// Create WebRTC channels
	c.webrtcChannels = webrtc.NewChannels(
		c.webrtcConn,
		c.ui,
		c.receiver,
		c.receiver,
	)
	
	// Set up WebRTC connection
	err := c.webrtcConn.SetupPeerConnection()
	if err != nil {
		c.ui.ShowError(fmt.Sprintf("Failed to setup peer connection: %v", err))
		return
	}
	
	// Set up WebRTC signaling
	c.webrtcSignaling.SetupICEHandlers()
	
	// Set up WebRTC channels
	c.webrtcChannels.SetupChannelHandlers()
	
	// If we're the initiator, create an offer
	if isInitiator {
		err := c.webrtcSignaling.CreateOffer()
		if err != nil {
			c.ui.ShowError(fmt.Sprintf("Failed to create offer: %v", err))
			return
		}
	}
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

		case "accepted":
			if c.webrtcSignaling == nil {
				c.ui.ShowError("No active connection attempt")
				continue
			}
			
			err := c.webrtcSignaling.CreateOffer()
			if err != nil {
				c.ui.ShowError(fmt.Sprintf("Failed to create offer: %v", err))
				continue
			}

		case "offer":
			if c.webrtcSignaling == nil {
				c.ui.ShowError("No active connection attempt")
				continue
			}
			
			err := c.webrtcSignaling.HandleOffer(webrtc.SignalingMessage{
				Type:      msg.Type,
				Token:     msg.Token,
				PeerToken: msg.PeerToken,
				SDP:       msg.SDP,
				ICE:       msg.ICE,
			})
			if err != nil {
				c.ui.ShowError(fmt.Sprintf("Failed to handle offer: %v", err))
				continue
			}

		case "answer":
			if c.webrtcSignaling == nil {
				c.ui.ShowError("No active connection attempt")
				continue
			}
			
			err := c.webrtcSignaling.HandleAnswer(webrtc.SignalingMessage{
				Type:      msg.Type,
				Token:     msg.Token,
				PeerToken: msg.PeerToken,
				SDP:       msg.SDP,
				ICE:       msg.ICE,
			})
			if err != nil {
				c.ui.ShowError(fmt.Sprintf("Failed to handle answer: %v", err))
				continue
			}

		case "ice":
			if c.webrtcSignaling == nil {
				c.ui.ShowError("No active connection attempt")
				continue
			}
			
			err := c.webrtcSignaling.HandleICE(webrtc.SignalingMessage{
				Type:      msg.Type,
				Token:     msg.Token,
				PeerToken: msg.PeerToken,
				SDP:       msg.SDP,
				ICE:       msg.ICE,
			})
			if err != nil {
				c.ui.ShowError(fmt.Sprintf("Failed to handle ICE candidate: %v", err))
				continue
			}

		case "rejected":
			c.ui.ShowConnectionRejected(msg.Token)
			c.Disconnect()

		case "error":
			c.ui.ShowError(msg.SDP)
			c.Disconnect()
		}
	}
}