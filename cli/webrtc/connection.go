package webrtc

import (
	"encoding/json"
	"fmt"

	pionwebrtc "github.com/pion/webrtc/v3"
)

// Logger interface for logging and UI updates
type Logger interface {
LogDebug(msg string)
ShowError(msg string)
AppendChat(msg string)
}

// ConnectionState contains the current state of a WebRTC connection
type ConnectionState struct {
PeerConn       *pionwebrtc.PeerConnection
ControlChannel *pionwebrtc.DataChannel
DataChannel    *pionwebrtc.DataChannel
PeerToken      string
Connected      bool
}

// Connection manages a WebRTC connection
type Connection struct {
state            *ConnectionState
logger           Logger
connectionSetup  func()
channelsReady    func()
maxChunkSize     int
maxMessageSize   int
handleControl    func([]byte)
handleData      func([]byte)
}

// NewConnection creates a new WebRTC connection
func NewConnection(
    logger Logger,
    connectionSetup func(),
    channelsReady func(),
    maxChunkSize int,
    maxMessageSize int,
) *Connection {
    return &Connection{
        state:            &ConnectionState{},
        logger:           logger,
        connectionSetup:  connectionSetup,
        channelsReady:    channelsReady,
        maxChunkSize:     maxChunkSize,
        maxMessageSize:   maxMessageSize,
    }
}

// SetMessageHandlers sets the handlers for control and data messages
func (c *Connection) SetMessageHandlers(handleControl func([]byte), handleData func([]byte)) {
    c.handleControl = handleControl
    c.handleData = handleData
}

// SetPeerToken sets the peer token for this connection
func (c *Connection) SetPeerToken(token string) {
    c.state.PeerToken = token
}

// GetPeerToken gets the peer token for this connection
func (c *Connection) GetPeerToken() string {
    return c.state.PeerToken
}

// GetControlChannel gets the control data channel
func (c *Connection) GetControlChannel() *pionwebrtc.DataChannel {
    return c.state.ControlChannel
}

// GetDataChannel gets the data transfer channel
func (c *Connection) GetDataChannel() *pionwebrtc.DataChannel {
    return c.state.DataChannel
}

// SetupDataChannel configures message handling for a data channel
func (c *Connection) setupDataChannel(channel *pionwebrtc.DataChannel, isControl bool) {
    // Set the message handler based on channel type
    channel.OnMessage(func(msg pionwebrtc.DataChannelMessage) {
        if isControl && c.handleControl != nil {
            c.handleControl(msg.Data)
        } else if !isControl && c.handleData != nil {
            c.handleData(msg.Data)
        }
    })

    // Set error handler
    channel.OnError(func(err error) {
        c.logger.LogDebug(fmt.Sprintf("Channel error: %v", err))
    })

    // Set close handler
    channel.OnClose(func() {
        c.logger.LogDebug(fmt.Sprintf("Channel closed: %s", channel.Label()))
    })
}

// SetupPeerConnection initializes the WebRTC peer connection
func (c *Connection) SetupPeerConnection() error {
    // Create a new RTCPeerConnection
    config := pionwebrtc.Configuration{
        ICEServers: []pionwebrtc.ICEServer{
            {
                URLs: []string{
                    "stun:stun.l.google.com:19302",
                    "stun:stun1.l.google.com:19302",
                },
            },
            {
                URLs: []string{
                    "turn:openrelay.metered.ca:80",
                    "turn:openrelay.metered.ca:443",
                    "turn:openrelay.metered.ca:443?transport=tcp",
                },
                Username:   "openrelayproject",
                Credential: "openrelayproject",
            },
        },
    }

    peerConn, err := pionwebrtc.NewPeerConnection(config)
    if err != nil {
        return fmt.Errorf("failed to create peer connection: %v", err)
    }

    c.state.PeerConn = peerConn

    // Set up data channels with ordered delivery, negotiated IDs and correct labels
    ordered := true
    negotiated := true
    controlID := uint16(1)
    dataID := uint16(2)

    protocol := "json"
    controlConfig := &pionwebrtc.DataChannelInit{
        ID:         &controlID,
        Ordered:    &ordered,
        Negotiated: &negotiated,
        Protocol:   &protocol,
    }

    dataConfig := &pionwebrtc.DataChannelInit{
        ID:         &dataID,
        Ordered:    &ordered,
        Negotiated: &negotiated,
    }

    // Create the data channels immediately since they are negotiated
    controlChannel, err := peerConn.CreateDataChannel("p2pftp-control", controlConfig)
    if err != nil {
        return fmt.Errorf("failed to create control channel: %v", err)
    }

    dataChannel, err := peerConn.CreateDataChannel("p2pftp-data", dataConfig)
    if err != nil {
        return fmt.Errorf("failed to create data channel: %v", err)
    }

    c.state.ControlChannel = controlChannel
    c.state.DataChannel = dataChannel

    // Set up message handlers for both channels
    c.setupDataChannel(controlChannel, true)
    c.setupDataChannel(dataChannel, false)

    // Set up channel open handlers (once for each channel)
    controlChannel.OnOpen(func() {
        c.logger.LogDebug("Control channel opened")
        if c.state.DataChannel.ReadyState() == pionwebrtc.DataChannelStateOpen {
            c.completeConnectionSetup()
        }
    })

    dataChannel.OnOpen(func() {
        c.logger.LogDebug("Data channel opened")
        if c.state.ControlChannel.ReadyState() == pionwebrtc.DataChannelStateOpen {
            c.completeConnectionSetup()
        }
    })

    // Monitor connection state changes
    peerConn.OnConnectionStateChange(func(state pionwebrtc.PeerConnectionState) {
        c.logger.LogDebug(fmt.Sprintf("Peer connection state changed to %s", state.String()))

        switch state {
        case pionwebrtc.PeerConnectionStateFailed:
            // On failure, just log it - the application layer should handle recovery
            c.logger.ShowError("WebRTC connection failed")
        case pionwebrtc.PeerConnectionStateDisconnected:
            c.logger.ShowError("Connection lost. Attempting to reconnect...")
        }
    })

    return nil
}

// Disconnect closes the peer connection
func (c *Connection) Disconnect() {
    if c.state.PeerConn != nil {
        c.state.PeerConn.Close()
    }
    c.state = &ConnectionState{}
}

// completeConnectionSetup is called when both channels are open
func (c *Connection) completeConnectionSetup() {
    // Double-check that both channels are actually initialized
    if c.state.ControlChannel == nil || c.state.DataChannel == nil {
        c.logger.LogDebug("Cannot complete connection setup: one or both channels are not initialized")
        return
    }

    // Check that both channels are in the open state
    if c.state.ControlChannel.ReadyState() != pionwebrtc.DataChannelStateOpen {
        c.logger.LogDebug(fmt.Sprintf("Cannot complete connection setup: control channel is not open (state: %s)",
            c.state.ControlChannel.ReadyState().String()))
        return
    }

    if c.state.DataChannel.ReadyState() != pionwebrtc.DataChannelStateOpen {
        c.logger.LogDebug(fmt.Sprintf("Cannot complete connection setup: data channel is not open (state: %s)",
            c.state.DataChannel.ReadyState().String()))
        return
    }

    // Check peer connection state
    if c.state.PeerConn != nil && c.state.PeerConn.ConnectionState() != pionwebrtc.PeerConnectionStateConnected {
        c.logger.LogDebug(fmt.Sprintf("Warning: Peer connection is not in connected state (state: %s)",
            c.state.PeerConn.ConnectionState().String()))
    }

    c.state.Connected = true
    c.logger.LogDebug(fmt.Sprintf("Both channels ready for transfer. Control: %s, Data: %s",
        c.state.ControlChannel.ReadyState().String(), c.state.DataChannel.ReadyState().String()))

    // Call the connection setup callback in a goroutine to avoid deadlock
    if c.connectionSetup != nil {
        go func() {
            c.logger.LogDebug("Calling connectionSetup callback from goroutine")
            c.connectionSetup()
        }()
    }

    // Call the channels ready callback in a goroutine to avoid deadlock
    if c.channelsReady != nil {
        go func() {
            c.logger.LogDebug("Calling channelsReady callback from goroutine")
            c.channelsReady()
        }()
    } else {
        c.logger.LogDebug("No channelsReady callback registered")
    }

    // Send capabilities message with our maximum supported chunk size
    capabilities := struct {
        Type         string `json:"type"`
        MaxChunkSize int    `json:"maxChunkSize"`
    }{
        Type:         "capabilities",
        MaxChunkSize: c.maxChunkSize,
    }

    capabilitiesJSON, err := json.Marshal(capabilities)
    if err == nil {
        // Double-check that control channel is still initialized and open
        if c.state.ControlChannel != nil {
            if c.state.ControlChannel.ReadyState() == pionwebrtc.DataChannelStateOpen {
                err := c.state.ControlChannel.SendText(string(capabilitiesJSON))
                if err != nil {
                    c.logger.LogDebug(fmt.Sprintf("Error sending capabilities: %v", err))
                } else {
                    c.logger.LogDebug(fmt.Sprintf("Sent capabilities with max chunk size: %d", c.maxChunkSize))
                }
            } else {
                c.logger.LogDebug(fmt.Sprintf("Cannot send capabilities: control channel is not open (state: %s)",
                    c.state.ControlChannel.ReadyState().String()))
            }
        } else {
            c.logger.LogDebug("Cannot send capabilities: control channel is not initialized")
        }
    }
}
