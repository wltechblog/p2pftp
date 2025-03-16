package main

import (
	"fmt"

	"github.com/gorilla/websocket"
	pionwebrtc "github.com/pion/webrtc/v3"

	"github.com/wltechblog/p2pftp/cli/transfer"
	ourwebrtc "github.com/wltechblog/p2pftp/cli/webrtc" // Our custom webrtc package
)

// Use constants from config.go

// Client represents the P2PFTP client
type Client struct {
	conn            *websocket.Conn
	token           string
	ui              UserInterface
	webrtcConn      *ClientWebRTCConnection
	webrtcSignaling *ourwebrtc.Signaling
	webrtcChannels  *ourwebrtc.Channels
	sender          *transfer.Sender
	receiver        *transfer.Receiver
}

// ClientWebRTCConnection extends the ourwebrtc.Connection with client-specific functionality
type ClientWebRTCConnection struct {
	*ourwebrtc.Connection
	client *Client
}

// OnChannelsReady is called when both channels are ready
func (c *ClientWebRTCConnection) OnChannelsReady() {
	// Make sure the channels are initialized
	if c.Connection.GetControlChannel() == nil || c.Connection.GetDataChannel() == nil {
		c.client.ui.LogDebug("Cannot set up channel handlers: channels not initialized")
		return
	}
	
	// Make sure the channels are in the open state
	if c.Connection.GetControlChannel().ReadyState() != pionwebrtc.DataChannelStateOpen ||
	   c.Connection.GetDataChannel().ReadyState() != pionwebrtc.DataChannelStateOpen {
		c.client.ui.LogDebug("Cannot set up channel handlers: channels not in open state")
		return
	}
	
	// Set up WebRTC channels
	c.client.webrtcChannels.SetupChannelHandlers()
	
	// Log that channels are ready
	c.client.ui.LogDebug("Channels are ready, handlers set up")
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
func (c *Client) SendSignalingMessage(msg ourwebrtc.SignalingMessage) error {
	// Convert to our Message type
	message := Message{
		Type:      msg.Type,
		Token:     msg.Token,
		PeerToken: msg.PeerToken,
		SDP:       msg.SDP,
		ICE:       msg.ICE,
	}
	
	// Log the message type
	c.ui.LogDebug(fmt.Sprintf("Sending signaling message: %s", msg.Type))
	
	// Send the message
	err := c.SendMessage(message)
	if err != nil {
		c.ui.LogDebug(fmt.Sprintf("Error sending signaling message: %v", err))
	}
	return err
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
		c.webrtcConn.Connection.Disconnect()
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
	c.webrtcConn = &ClientWebRTCConnection{
		Connection: ourwebrtc.NewConnection(
			c.ui,
			func() {
				c.ui.ShowConnectionAccepted("")
			},
			262144, // fixedChunkSize from config.go
			262144, // maxWebRTCMessageSize from config.go
		),
		client: c,
	}
	
	// Create WebRTC signaling
	c.webrtcSignaling = ourwebrtc.NewSignaling(
		c.webrtcConn.Connection,
		c,
		c.ui,
	)
	
	// Create sender and receiver
	c.sender = transfer.NewSender(
		c.webrtcConn.Connection.GetControlChannel(),
		c.webrtcConn.Connection.GetDataChannel(),
		c.ui,
		func(status string, direction string) {
			c.ui.UpdateTransferProgress(status, direction)
		},
		262144, // fixedChunkSize from config.go
		262144, // maxWebRTCMessageSize from config.go
	)
	
	c.receiver = transfer.NewReceiver(
		c.webrtcConn.Connection.GetControlChannel(),
		c.webrtcConn.Connection.GetDataChannel(),
		c.ui,
		func(status string, direction string) {
			c.ui.UpdateTransferProgress(status, direction)
		},
		262144, // fixedChunkSize from config.go
	)
	
	// Create WebRTC channels
	c.webrtcChannels = ourwebrtc.NewChannels(
		c.webrtcConn.Connection,
		c.ui,
		c.receiver,
		c.receiver,
	)
	
	// Set up WebRTC connection
	err := c.webrtcConn.Connection.SetupPeerConnection()
	if err != nil {
		c.ui.ShowError(fmt.Sprintf("Failed to setup peer connection: %v", err))
		return
	}
	
	// Set up WebRTC signaling
	c.webrtcSignaling.SetupICEHandlers()
	
	// We'll set up the channel handlers after the channels are created
	// This happens in the OnOpen callbacks in the Connection
	
	// If we're the initiator, create an offer
	if isInitiator {
		err := c.webrtcSignaling.CreateOffer()
		if err != nil {
			c.ui.ShowError(fmt.Sprintf("Failed to create offer: %v", err))
			return
		}
	}
}

// logMessage logs a debug message with a timestamp
func (c *Client) logMessage(format string, args ...interface{}) {
	c.ui.LogDebug(fmt.Sprintf(format, args...))
}

// handleMessages processes incoming WebSocket messages from the server
func (c *Client) handleMessages() {
	c.logMessage("Message handler started, waiting for server token...")
	for {
		var msg Message
		err := c.conn.ReadJSON(&msg)
		if err != nil {
			c.logMessage("Error reading message: %v", err)
			c.ui.ShowError("Connection error - please try again")
			return
		}

		// Log the received message type
		c.logMessage("Received message: %s", msg.Type)

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
			
			c.logMessage("Creating signaling message for offer")
			sigMsg := ourwebrtc.SignalingMessage{
				Type:      msg.Type,
				Token:     msg.Token,
				PeerToken: msg.PeerToken,
				SDP:       msg.SDP,
				ICE:       msg.ICE,
			}
			
			c.logMessage("Handling offer")
			err := c.webrtcSignaling.HandleOffer(sigMsg)
			if err != nil {
				c.logMessage("Error handling offer: %v", err)
				c.ui.ShowError(fmt.Sprintf("Failed to handle offer: %v", err))
				continue
			}
			
			c.logMessage("Offer handled successfully")

		case "answer":
			if c.webrtcSignaling == nil {
				c.ui.ShowError("No active connection attempt")
				continue
			}
			
			c.logMessage("Creating signaling message for answer")
			sigMsg := ourwebrtc.SignalingMessage{
				Type:      msg.Type,
				Token:     msg.Token,
				PeerToken: msg.PeerToken,
				SDP:       msg.SDP,
				ICE:       msg.ICE,
			}
			
			c.logMessage("Handling answer")
			err := c.webrtcSignaling.HandleAnswer(sigMsg)
			if err != nil {
				c.logMessage("Error handling answer: %v", err)
				c.ui.ShowError(fmt.Sprintf("Failed to handle answer: %v", err))
				continue
			}
			
			c.logMessage("Answer handled successfully")

		case "ice":
			if c.webrtcSignaling == nil {
				c.ui.ShowError("No active connection attempt")
				continue
			}
			
			c.logMessage("Creating signaling message for ICE")
			sigMsg := ourwebrtc.SignalingMessage{
				Type:      msg.Type,
				Token:     msg.Token,
				PeerToken: msg.PeerToken,
				SDP:       msg.SDP,
				ICE:       msg.ICE,
			}
			
			c.logMessage("Handling ICE")
			err := c.webrtcSignaling.HandleICE(sigMsg)
			if err != nil {
				c.logMessage("Error handling ICE: %v", err)
				c.ui.ShowError(fmt.Sprintf("Failed to handle ICE candidate: %v", err))
				continue
			}
			
			c.logMessage("ICE handled successfully")

		case "rejected":
			c.ui.ShowConnectionRejected(msg.Token)
			c.Disconnect()

		case "error":
			c.ui.ShowError(msg.SDP)
			c.Disconnect()
		}
	}
}