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
	ourwebrtc "github.com/wltechblog/p2pftp/cli/webrtc"
)

const (
    defaultChunkSize = 16384  // 16KB
    maxMessageSize   = 65536  // 64KB
)

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
    ShowError(msg string)    // Show error message (always shown)
    ShowInfo(msg string)     // Show important info (always shown)
    LogDebug(msg string)     // Show debug message (only with -debug flag)
    AppendChat(msg string)   // Show chat message without sender info
    ShowChat(from, msg string) // Show chat message with sender info
    ShowConnectionRequest(token string)
    ShowConnectionAccepted(msg string)
    ShowConnectionRejected(token string)
    SetToken(token string)
    UpdateTransferProgress(status string, direction string)
}

// Client represents the P2PFTP client and implements ui.Client interface
type Client struct {
    conn              *websocket.Conn
    token            string
    ui               UserInterface
    webrtcConn       *ClientWebRTCConnection
    webrtcSignaling  *ourwebrtc.Signaling
    webrtcChannels   *ourwebrtc.Channels
    sender           *transfer.Sender
    receiver         *transfer.Receiver
    hasCreatedOffer  bool
    lastRequestToken string
    debugMode        bool
}

// ClientWebRTCConnection extends the ourwebrtc.Connection with client-specific functionality
type ClientWebRTCConnection struct {
    *ourwebrtc.Connection
    client *Client
}

// NewClient creates a new client instance
func NewClient(conn *websocket.Conn, debug bool) *Client {
    return &Client{
        conn:            conn,
        hasCreatedOffer: false,
        debugMode:       debug,
    }
}

// SetUI sets the UI for the client
func (c *Client) SetUI(ui UserInterface) {
    c.ui = ui
}

// logMessage logs a debug message with a timestamp
func (c *Client) logMessage(format string, args ...interface{}) {
    if c.debugMode {
        c.ui.LogDebug(fmt.Sprintf(format, args...))
    }
}

// SendSignalingMessage sends a WebRTC signaling message to the peer
func (c *Client) SendSignalingMessage(msg ourwebrtc.SignalingMessage) error {
    // Convert from SignalingMessage to our Message type
    serverMsg := Message{
        Type:      msg.Type,
        PeerToken: msg.PeerToken,
        SDP:       msg.SDP,
        ICE:       msg.ICE,
    }

    return c.SendMessage(serverMsg)
}

// SendMessage sends a message to the server
func (c *Client) SendMessage(msg Message) error {
    if c.debugMode {
        msgJSON, _ := json.Marshal(msg)
        c.logMessage("Sending message: %s", string(msgJSON))
    }
    
    err := c.conn.WriteJSON(msg)
    if err != nil {
        c.ui.ShowError("Send failed: " + err.Error())
    }
    return err
}

// Connect initiates a connection to a peer
func (c *Client) Connect(peerToken string) error {
    if peerToken == "" {
        return fmt.Errorf("peer token cannot be empty")
    }
    if peerToken == c.token {
        return fmt.Errorf("cannot connect to yourself")
    }

    c.logMessage("Connecting to peer: %s", peerToken)
    c.initWebRTC(peerToken, true)

    return c.SendMessage(Message{
        Type:      "connect",
        PeerToken: peerToken,
    })
}

// Accept accepts a connection request
func (c *Client) Accept(peerToken string) error {
    // If no token provided, use the last request token
    if peerToken == "" {
        if c.lastRequestToken == "" {
            return fmt.Errorf("no recent connection requests")
        }
        peerToken = c.lastRequestToken
        c.ui.ShowInfo(fmt.Sprintf("Accepting last connection request from: %s", peerToken))
    }

    c.logMessage("Accepting connection from: %s", peerToken)
    c.initWebRTC(peerToken, false)
    
    return c.SendMessage(Message{
        Type:      "accept",
        PeerToken: peerToken,
    })
}

// handleMessages processes incoming WebSocket messages
func (c *Client) handleMessages() {
    for {
        var msg Message
        if err := c.conn.ReadJSON(&msg); err != nil {
            c.ui.ShowError("Connection error - please try again")
            return
        }

        c.logMessage("Received message type: %s", msg.Type)
        
        switch msg.Type {
        case "token":
            c.token = msg.Token
            c.ui.SetToken(msg.Token)
            
        case "request":
            c.lastRequestToken = msg.Token
            c.ui.ShowInfo(fmt.Sprintf("Connection request from: %s", msg.Token))
            c.ui.ShowConnectionRequest(msg.Token)
            
        case "message":
            // Extract chat message content
            type chatMessage struct {
                Type    string `json:"type"`
                Content string `json:"content"`
            }
            var chat chatMessage
            if err := json.Unmarshal([]byte(msg.SDP), &chat); err == nil && chat.Type == "message" {
                from := msg.Token
                if from == "" {
                    from = msg.PeerToken
                }
                c.ui.ShowChat(from, chat.Content)
            }
            
        case "rejected":
            c.ui.ShowConnectionRejected(msg.Token)
            c.Disconnect()
            
        case "error":
            if strings.Contains(msg.SDP, "Peer not found") {
                c.ui.ShowError("Peer not found. Please check the token and try again.")
            } else {
                c.ui.ShowError(msg.SDP)
            }
            c.Disconnect()

        case "offer":
            if c.webrtcSignaling == nil {
                c.ui.ShowError("No active connection attempt")
                continue
            }
            sigMsg := ourwebrtc.SignalingMessage{
                Type:      msg.Type,
                Token:     msg.Token,
                PeerToken: msg.PeerToken,
                SDP:       msg.SDP,
                ICE:       msg.ICE,
            }
            if err := c.webrtcSignaling.HandleOffer(sigMsg); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to handle offer: %v", err))
            }

        case "answer":
            if c.webrtcSignaling == nil {
                c.ui.ShowError("No active connection attempt")
                continue
            }
            sigMsg := ourwebrtc.SignalingMessage{
                Type:      msg.Type,
                Token:     msg.Token,
                PeerToken: msg.PeerToken,
                SDP:       msg.SDP,
                ICE:       msg.ICE,
            }
            if err := c.webrtcSignaling.HandleAnswer(sigMsg); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to handle answer: %v", err))
            }

        case "ice":
            if c.webrtcSignaling == nil {
                c.ui.ShowError("No active connection attempt")
                continue
            }
            sigMsg := ourwebrtc.SignalingMessage{
                Type:      msg.Type,
                Token:     msg.Token,
                PeerToken: msg.PeerToken,
                SDP:       msg.SDP,
                ICE:       msg.ICE,
            }
            if err := c.webrtcSignaling.HandleICE(sigMsg); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to handle ICE candidate: %v", err))
            }
        }
    }
}

// Disconnect closes the peer connection and resets state
func (c *Client) Disconnect() error {
    c.logMessage("Disconnecting from peer")
    
    if c.webrtcConn != nil && c.webrtcConn.Connection != nil {
        c.webrtcConn.Connection.Disconnect()
    }
    
    // Reset state
    c.webrtcConn = nil
    c.webrtcSignaling = nil
    c.webrtcChannels = nil
    c.sender = nil
    c.receiver = nil
    c.hasCreatedOffer = false
    
    c.logMessage("Disconnected successfully")
    return nil
}

// SendChat sends a chat message to the peer
func (c *Client) SendChat(text string) error {
    c.logMessage("SendChat called with text: %s", text)

    if c.webrtcChannels == nil {
        return fmt.Errorf("not connected to peer")
    }

    if c.webrtcConn == nil || c.webrtcConn.Connection == nil {
        return fmt.Errorf("connection not established")
    }

    dataChannel := c.webrtcConn.Connection.GetDataChannel()
    if dataChannel == nil || dataChannel.ReadyState() != pionwebrtc.DataChannelStateOpen {
        return fmt.Errorf("data channel not ready, please wait or reconnect")
    }

    return c.webrtcChannels.SendChatMessage(text)
}

// SendFile sends a file to the peer
func (c *Client) SendFile(path string) error {
    c.logMessage("SendFile called with path: %s", path)

    if c.sender == nil {
        return fmt.Errorf("not connected to peer")
    }

    if c.webrtcConn == nil || c.webrtcConn.Connection == nil {
        return fmt.Errorf("connection not established")
    }

    return c.sender.SendFile(path)
}

func (c *Client) channelsReady() {
    c.logMessage("WebRTC channels ready, setting up handlers")

    if c.webrtcConn == nil || c.webrtcConn.Connection == nil {
        c.logMessage("ERROR: Connection not initialized in channelsReady")
        return
    }

    // Create sender and receiver
    c.sender = transfer.NewSender(
        c.webrtcConn.Connection.GetControlChannel(),
        c.webrtcConn.Connection.GetDataChannel(),
        c.ui,
        func(status string, direction string) {
            c.ui.UpdateTransferProgress(status, direction)
        },
        defaultChunkSize,
        maxMessageSize,
    )

    c.receiver = transfer.NewReceiver(
        c.webrtcConn.Connection.GetControlChannel(),
        c.webrtcConn.Connection.GetDataChannel(),
        c.ui,
        func(status string, direction string) {
            c.ui.UpdateTransferProgress(status, direction)
        },
        defaultChunkSize,
    )

    // Create WebRTC channels after sender/receiver are ready
    c.webrtcChannels = ourwebrtc.NewChannels(
        c.webrtcConn.Connection,
        c.ui,
        c.receiver,
        c.receiver,
    )

    c.webrtcChannels.SetupChannelHandlers()
    c.logMessage("Channel handlers setup complete")

    c.ui.ShowInfo("Connection established - ready for chat and file transfer")
}

// initWebRTC initializes WebRTC components
func (c *Client) initWebRTC(peerToken string, isInitiator bool) {
    c.logMessage("Initializing WebRTC (initiator: %v)", isInitiator)

    // Create and setup WebRTC connection with channels callback
    c.webrtcConn = &ClientWebRTCConnection{
        Connection: ourwebrtc.NewConnection(
            c.ui,
            func() { c.ui.ShowConnectionAccepted("") },
            c.channelsReady,
            defaultChunkSize,
            maxMessageSize,
        ),
        client: c,
    }
    
    c.webrtcConn.Connection.SetPeerToken(peerToken)

    // Create signaling handler
    c.webrtcSignaling = ourwebrtc.NewSignaling(
        c.webrtcConn.Connection,
        c,
        c.ui,
    )

    // Setup the peer connection and ICE handlers
    if err := c.webrtcConn.Connection.SetupPeerConnection(); err != nil {
        c.ui.ShowError(fmt.Sprintf("Failed to setup peer connection: %v", err))
        return
    }
    c.webrtcSignaling.SetupICEHandlers()
    c.logMessage("WebRTC peer connection and ICE handlers setup complete")
    
    // If we're initiating, create an offer after a short delay
    // to allow the peer to set up their connection
    if isInitiator {
        c.logMessage("Creating offer as initiator")
        if err := c.webrtcSignaling.CreateOffer(); err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to create offer: %v", err))
        }
    }
}

// main is the entry point for the CLI application
func main() {
    // Parse command line arguments
    addr := flag.String("addr", "localhost:8089", "server address")
    debug := flag.Bool("debug", false, "enable debug logging")
    flag.Bool("secure", true, "use secure WebSocket connection")
    flag.Parse()

    // Create WebSocket URL
    u := url.URL{Scheme: "wss", Host: *addr, Path: "/ws"}
    log.Printf("Connecting to %s...", u.String())

    // Connect to the server
    conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
    if err != nil {
        log.Fatal("WebSocket dial error:", err)
    }
    defer conn.Close()

    // Create client
    client := NewClient(conn, *debug)

    // Create UI with back-reference to client
    userInterface := ui.NewUI(client)
    client.SetUI(userInterface)

    // Start message handler
    go client.handleMessages()

    // Run UI (blocks until exit)
    // Enable debug logging for UI if flag is set
    userInterface.SetDebug(*debug)
    
    // Show greeting in debug mode
    if *debug {
        fmt.Printf("Debug logging enabled\n")
    }
    
    if err := userInterface.Run(); err != nil {
        fmt.Printf("Error running UI: %v\n", err)
    }
}
