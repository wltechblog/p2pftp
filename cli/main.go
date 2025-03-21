package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
	pionwebrtc "github.com/pion/webrtc/v3"

	"github.com/wltechblog/p2pftp/cli/transfer"
	"github.com/wltechblog/p2pftp/cli/ui"
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
	hasCreatedOffer bool
}

// ClientWebRTCConnection extends the ourwebrtc.Connection with client-specific functionality
type ClientWebRTCConnection struct {
	*ourwebrtc.Connection
	client *Client
}

// Override the OnChannelsReady method from the base Connection
func (c *ClientWebRTCConnection) OnChannelsReady() {
	c.client.ui.LogDebug("ClientWebRTCConnection.OnChannelsReady called - this is the overridden method")
	
	// Make sure the channels are initialized
	if c.Connection.GetControlChannel() == nil || c.Connection.GetDataChannel() == nil {
		c.client.ui.LogDebug("Cannot set up channel handlers: channels not initialized")
		return
	}
	
	// Log channel states
	controlState := c.Connection.GetControlChannel().ReadyState()
	dataState := c.Connection.GetDataChannel().ReadyState()
	c.client.ui.LogDebug(fmt.Sprintf("Control channel state: %s", controlState.String()))
	c.client.ui.LogDebug(fmt.Sprintf("Data channel state: %s", dataState.String()))
	
	// Make sure the channels are in the open state
	if controlState != pionwebrtc.DataChannelStateOpen ||
	   dataState != pionwebrtc.DataChannelStateOpen {
		c.client.ui.LogDebug("Cannot set up channel handlers: channels not in open state")
		return
	}
	
	// Re-create the sender and receiver with the actual channels
	c.client.ui.LogDebug("Re-creating sender and receiver with actual channels")
	c.client.sender = transfer.NewSender(
		c.Connection.GetControlChannel(),
		c.Connection.GetDataChannel(),
		c.client.ui,
		func(status string, direction string) {
			c.client.ui.UpdateTransferProgress(status, direction)
		},
		262144, // fixedChunkSize from config.go
		262144, // maxWebRTCMessageSize from config.go
	)
	
	c.client.receiver = transfer.NewReceiver(
		c.Connection.GetControlChannel(),
		c.Connection.GetDataChannel(),
		c.client.ui,
		func(status string, direction string) {
			c.client.ui.UpdateTransferProgress(status, direction)
		},
		262144, // fixedChunkSize from config.go
	)
	
	// Re-create the WebRTC channels with the actual message and data handlers
	c.client.ui.LogDebug("Re-creating WebRTC channels with actual handlers")
	c.client.webrtcChannels = ourwebrtc.NewChannels(
		c.Connection,
		c.client.ui,
		c.client.receiver, // Message handler
		c.client.receiver, // Data handler
	)
	
	// Set up WebRTC channels
	c.client.ui.LogDebug("Setting up channel handlers")
	c.client.webrtcChannels.SetupChannelHandlers()
	
	// Send capabilities
	c.client.ui.LogDebug("Sending capabilities")
	err := c.client.webrtcChannels.SendCapabilities(262144) // fixedChunkSize from config.go
	if err != nil {
		c.client.ui.LogDebug(fmt.Sprintf("Error sending capabilities: %v", err))
	} else {
		c.client.ui.LogDebug("Capabilities sent successfully")
	}
	
	// Log that channels are ready
	c.client.ui.LogDebug("Channels are ready, handlers set up")
	
	// Verify that the components are properly initialized
	if c.client.sender == nil {
		c.client.ui.LogDebug("ERROR: sender is nil after setup")
	} else {
		c.client.ui.LogDebug("Sender is properly initialized")
	}
	
	if c.client.receiver == nil {
		c.client.ui.LogDebug("ERROR: receiver is nil after setup")
	} else {
		c.client.ui.LogDebug("Receiver is properly initialized")
	}
	
	if c.client.webrtcChannels == nil {
		c.client.ui.LogDebug("ERROR: webrtcChannels is nil after setup")
	} else {
		c.client.ui.LogDebug("WebRTC channels are properly initialized")
	}
}



// Using UserInterface and Message types from the main package

// NewClient creates a new client instance
func NewClient(conn *websocket.Conn) *Client {
	return &Client{
		conn:            conn,
		hasCreatedOffer: false,
	}
}

// SetUI sets the UI for the client
func (c *Client) SetUI(ui UserInterface) {
	c.ui = ui
}

// SendMessage sends a message to the server
func (c *Client) SendMessage(msg Message) error {
	// Log the message for debugging
	msgJSON, _ := json.Marshal(msg)
	c.logMessage("Sending message to server: %s", string(msgJSON))
	
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
	// Validate the token
	if peerToken == "" {
		c.logMessage("Error: Empty peer token")
		return fmt.Errorf("peer token cannot be empty")
	}
	
	// Check if the token is our own token
	if peerToken == c.token {
		c.logMessage("Error: Cannot connect to self")
		return fmt.Errorf("cannot connect to yourself")
	}
	
	// Log the connection attempt
	c.logMessage("Connecting to peer with token: '%s'", peerToken)
	c.logMessage("Our token is: '%s'", c.token)
	
	// Reset the hasCreatedOffer flag
	c.hasCreatedOffer = false
	
	// Initialize WebRTC components
	c.initWebRTC(peerToken, true)
	
	// Send connect message to server
	c.logMessage("Sending connect message to server with peer token: '%s'", peerToken)
	
	// Create the message - only include the PeerToken
	connectMsg := Message{
		Type:      "connect",
		PeerToken: peerToken,
	}
	
	// Log the message for debugging
	msgJSON, _ := json.Marshal(connectMsg)
	c.logMessage("Connect message JSON: %s", string(msgJSON))
	
	// Send the message
	err := c.SendMessage(connectMsg)
	
	if err != nil {
		c.logMessage("Error sending connect message: %v", err)
	} else {
		c.logMessage("Connect message sent successfully")
	}
	
	return err
}

// Accept accepts a connection request
func (c *Client) Accept(peerToken string) error {
	// Log the accept attempt
	c.logMessage("Accepting connection from peer with token: '%s'", peerToken)
	c.logMessage("Our token is: '%s'", c.token)
	
	// Reset the hasCreatedOffer flag
	c.hasCreatedOffer = false
	
	// Initialize WebRTC components
	c.initWebRTC(peerToken, false)
	
	// Send accept message to server
	c.logMessage("Sending accept message to server")
	
	// Create the message - only include the PeerToken
	acceptMsg := Message{
		Type:      "accept",
		PeerToken: peerToken,
	}
	
	// Log the message for debugging
	msgJSON, _ := json.Marshal(acceptMsg)
	c.logMessage("Accept message JSON: %s", string(msgJSON))
	
	// Send the message
	err := c.SendMessage(acceptMsg)
	
	if err != nil {
		c.logMessage("Error sending accept message: %v", err)
	} else {
		c.logMessage("Accept message sent successfully")
	}
	
	return err
}

// Reject rejects a connection request
func (c *Client) Reject(peerToken string) error {
	// Log the reject attempt
	c.logMessage("Rejecting connection from peer with token: '%s'", peerToken)
	c.logMessage("Our token is: '%s'", c.token)
	
	// Clean up any existing connection
	c.Disconnect()
	
	// Send reject message to server
	c.logMessage("Sending reject message to server")
	
	// Create the message - only include the PeerToken
	rejectMsg := Message{
		Type:      "reject",
		PeerToken: peerToken,
	}
	
	// Log the message for debugging
	msgJSON, _ := json.Marshal(rejectMsg)
	c.logMessage("Reject message JSON: %s", string(msgJSON))
	
	// Send the message
	err := c.SendMessage(rejectMsg)
	
	if err != nil {
		c.logMessage("Error sending reject message: %v", err)
	} else {
		c.logMessage("Reject message sent successfully")
	}
	
	return err
}

// SendChat sends a chat message
func (c *Client) SendChat(text string) error {
	c.logMessage("SendChat called with text: %s", text)
	
	if c.webrtcChannels == nil {
		c.logMessage("Error: webrtcChannels is nil")
		return fmt.Errorf("not connected to peer")
	}
	
	// Check if the connection is fully established
	if c.webrtcConn == nil || c.webrtcConn.Connection == nil {
		c.logMessage("Error: webrtcConn is nil or not initialized")
		return fmt.Errorf("connection not fully established, please wait or reconnect")
	}
	
	// Check if the control channel is initialized and open
	controlChannel := c.webrtcConn.Connection.GetControlChannel()
	if controlChannel == nil {
		c.logMessage("Error: control channel is nil")
		return fmt.Errorf("control channel not initialized, please wait or reconnect")
	}
	
	c.logMessage("Control channel state: %s", controlChannel.ReadyState().String())
	
	if controlChannel.ReadyState() != pionwebrtc.DataChannelStateOpen {
		c.logMessage("Error: control channel is not open (state: %s)", controlChannel.ReadyState().String())
		return fmt.Errorf("control channel is not ready (state: %s), please wait or reconnect",
			controlChannel.ReadyState().String())
	}
	
	// Now try to send the message
	c.logMessage("Sending chat message via webrtcChannels")
	err := c.webrtcChannels.SendChatMessage(text)
	if err != nil {
		c.logMessage("Error sending chat message: %v", err)
		return err
	}
	
	c.logMessage("Chat message sent successfully")
	return nil
}

// SendFile sends a file to the peer
func (c *Client) SendFile(path string) error {
	c.logMessage("SendFile called with path: %s", path)
	
	if c.sender == nil {
		c.logMessage("Error: sender is nil")
		return fmt.Errorf("not connected to peer")
	}
	
	// Check if the connection is fully established
	if c.webrtcConn == nil || c.webrtcConn.Connection == nil {
		c.logMessage("Error: webrtcConn is nil or not initialized")
		return fmt.Errorf("connection not fully established, please wait or reconnect")
	}
	
	// Check if the control channel is initialized and open
	controlChannel := c.webrtcConn.Connection.GetControlChannel()
	if controlChannel == nil {
		c.logMessage("Error: control channel is nil")
		return fmt.Errorf("control channel not initialized, please wait or reconnect")
	}
	
	c.logMessage("Control channel state: %s", controlChannel.ReadyState().String())
	
	if controlChannel.ReadyState() != pionwebrtc.DataChannelStateOpen {
		c.logMessage("Error: control channel is not open (state: %s)", controlChannel.ReadyState().String())
		return fmt.Errorf("control channel is not ready (state: %s), please wait or reconnect",
			controlChannel.ReadyState().String())
	}
	
	// Check if the data channel is initialized and open
	dataChannel := c.webrtcConn.Connection.GetDataChannel()
	if dataChannel == nil {
		c.logMessage("Error: data channel is nil")
		return fmt.Errorf("data channel not initialized, please wait or reconnect")
	}
	
	c.logMessage("Data channel state: %s", dataChannel.ReadyState().String())
	
	if dataChannel.ReadyState() != pionwebrtc.DataChannelStateOpen {
		c.logMessage("Error: data channel is not open (state: %s)", dataChannel.ReadyState().String())
		return fmt.Errorf("data channel is not ready (state: %s), please wait or reconnect",
			dataChannel.ReadyState().String())
	}
	
	// Now try to send the file
	c.logMessage("Sending file via sender")
	err := c.sender.SendFile(path)
	if err != nil {
		c.logMessage("Error sending file: %v", err)
		return err
	}
	
	c.logMessage("File sent successfully")
	return nil
}

// Disconnect disconnects from the peer
func (c *Client) Disconnect() error {
	c.logMessage("Disconnecting from peer")
	
	// Reset the hasCreatedOffer flag
	c.hasCreatedOffer = false
	
	if c.webrtcConn != nil {
		// Log the connection state before disconnecting
		if c.webrtcConn.Connection != nil {
			c.logMessage("Disconnecting WebRTC connection")
			c.webrtcConn.Connection.Disconnect()
		}
		
		// Reset the signaling state
		if c.webrtcSignaling != nil {
			c.logMessage("Resetting signaling state")
			c.webrtcSignaling.Reset()
		}
		
		// Clean up all references
		c.webrtcConn = nil
		c.webrtcSignaling = nil
		c.webrtcChannels = nil
		c.sender = nil
		c.receiver = nil
		
		c.logMessage("Disconnected successfully")
	} else {
		c.logMessage("No active connection to disconnect")
	}
	
	return nil
}

// initWebRTC initializes the WebRTC components
func (c *Client) initWebRTC(peerToken string, isInitiator bool) {
	c.logMessage("Initializing WebRTC components (isInitiator: %v)", isInitiator)
	
	// Store the peer token in the connection state
	c.logMessage("Setting peer token: '%s'", peerToken)
	
	// Create WebRTC connection
	c.logMessage("Creating WebRTC connection")
	c.webrtcConn = &ClientWebRTCConnection{
		Connection: ourwebrtc.NewConnection(
			c.ui,
			func() {
				c.logMessage("Connection setup callback called")
				c.ui.ShowConnectionAccepted("")
			},
			262144, // fixedChunkSize from config.go
			262144, // maxWebRTCMessageSize from config.go
		),
		client: c,
	}
	
	// Set the peer token in the connection state
	c.webrtcConn.Connection.SetPeerToken(peerToken)
	
	c.logMessage("WebRTC connection created")
	
	// Create WebRTC signaling
	c.webrtcSignaling = ourwebrtc.NewSignaling(
		c.webrtcConn.Connection,
		c,
		c.ui,
	)
	
	// We'll create the sender, receiver, and channels in the OnChannelsReady callback
	// after the connection is fully established
	c.logMessage("Sender, receiver, and channels will be created when the connection is established")
	
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
		// We'll wait for the "accepted" message before creating the offer
		c.logMessage("Waiting for accepted message before creating offer")
	}
}

// createOffer creates an SDP offer
func (c *Client) createOffer() error {
	// Check if we've already created an offer
	if c.hasCreatedOffer {
		c.logMessage("Already created an offer, ignoring")
		return nil
	}
	
	// Create the offer
	c.logMessage("Creating SDP offer")
	err := c.webrtcSignaling.CreateOffer()
	if err != nil {
		c.logMessage("Error creating offer: %v", err)
		return err
	}
	
	// Mark that we've created an offer
	c.hasCreatedOffer = true
	
	return nil
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

		// Log the received message type and content
		msgJSON, _ := json.Marshal(msg)
		c.logMessage("Received message: %s", string(msgJSON))

		switch msg.Type {
		case "token":
			c.token = msg.Token
			c.logMessage("Received token: '%s'", c.token)
			c.ui.SetToken(msg.Token)

		case "request":
			c.logMessage("Received connection request from peer with token: '%s'", msg.Token)
			c.ui.ShowConnectionRequest(msg.Token)

		case "accepted":
			if c.webrtcSignaling == nil {
				c.ui.ShowError("No active connection attempt")
				continue
			}
			
			c.logMessage("Connection accepted by peer with token: '%s'", msg.Token)
			
			// Create an offer if we haven't already
			if !c.hasCreatedOffer {
				err := c.createOffer()
				if err != nil {
					c.ui.ShowError(fmt.Sprintf("Failed to create offer: %v", err))
					continue
				}
			} else {
				c.logMessage("Already created an offer, ignoring accepted message")
			}

		case "offer":
			if c.webrtcSignaling == nil {
				c.ui.ShowError("No active connection attempt")
				continue
			}
			
			c.logMessage("Received offer from peer with token: '%s'", msg.Token)
			
			c.logMessage("Creating signaling message for offer")
			sigMsg := ourwebrtc.SignalingMessage{
				Type:      msg.Type,
				Token:     msg.Token,
				PeerToken: c.webrtcConn.Connection.GetPeerToken(),
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
			
			c.logMessage("Received answer from peer with token: '%s'", msg.Token)
			
			c.logMessage("Creating signaling message for answer")
			sigMsg := ourwebrtc.SignalingMessage{
				Type:      msg.Type,
				Token:     msg.Token,
				PeerToken: c.webrtcConn.Connection.GetPeerToken(),
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
			
			c.logMessage("Received ICE candidate from peer with token: '%s'", msg.Token)
			
			c.logMessage("Creating signaling message for ICE")
			sigMsg := ourwebrtc.SignalingMessage{
				Type:      msg.Type,
				Token:     msg.Token,
				PeerToken: c.webrtcConn.Connection.GetPeerToken(),
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
			c.logMessage("Connection rejected by peer with token: '%s'", msg.Token)
			c.ui.ShowConnectionRejected(msg.Token)
			c.Disconnect()

		case "error":
			// Extract the error message
			errorMsg := "Connection error"
			if msg.SDP != "" {
				errorMsg = msg.SDP
			}
			
			// Log the error message
			c.logMessage("Received error message: %s", errorMsg)
			
			// Check for specific error types
			if strings.Contains(errorMsg, "Peer not found") {
				c.logMessage("The peer token was not found on the server")
				c.ui.ShowError(fmt.Sprintf("Peer not found. Please check the token and try again."))
			} else {
				c.ui.ShowError(errorMsg)
			}
			
			// Disconnect
			c.Disconnect()
		}
	}
}

// Message represents a message exchanged with the server
type Message struct {
	Type      string `json:"type"`
	Token     string `json:"token,omitempty"`
	PeerToken string `json:"peerToken,omitempty"`
	SDP       string `json:"sdp,omitempty"`
	ICE       string `json:"ice,omitempty"`
}

// UserInterface defines the interface for the UI implementation
type UserInterface interface {
	ShowError(msg string)
	LogDebug(msg string)
	ShowChat(from string, msg string)
	AppendChat(msg string)
	ShowConnectionRequest(token string)
	ShowConnectionAccepted(msg string)
	ShowConnectionRejected(token string)
	SetToken(token string)
	UpdateTransferProgress(status string, direction string)
}

// main is the entry point for the CLI application
func main() {
    // Parse command line arguments
    addr := flag.String("addr", "localhost:8089", "server address")
    flag.Bool("secure", true, "use secure WebSocket connection (always true, kept for compatibility)")
    flag.Parse()

    // Create WebSocket URL - always use WSS as the server is behind an SSL proxy
    u := url.URL{Scheme: "wss", Host: *addr, Path: "/ws"}
    log.Printf("Connecting to %s...", u.String())

    // Connect to the server
    conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
    if err != nil {
        log.Fatal("WebSocket dial error:", err)
    }
    defer conn.Close()

    // Create client
    client := NewClient(conn)

    // Create UI with back-reference to client
    userInterface := ui.NewUI(client)
    client.SetUI(userInterface)

    // Start message handler
    go client.handleMessages()

    // Run UI (blocks until exit)
    if err := userInterface.Run(); err != nil {
        fmt.Printf("Error running UI: %v\n", err)
    }
}