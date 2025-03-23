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

// setupDataChannel configures logging handlers for a data channel
func (c *Connection) setupDataChannel(channel *pionwebrtc.DataChannel, isControl bool) {
    // Log message receipt
    channel.OnMessage(func(msg pionwebrtc.DataChannelMessage) {
        msgType := "binary"
        if msg.IsString {
            msgType = "string"
        }
        c.logger.LogDebug(fmt.Sprintf("[%s] Message received on %s channel: %d bytes", 
            msgType,
            channel.Label(),
            len(msg.Data)))
    })

    // Enhanced error logging
    channel.OnError(func(err error) {
        c.logger.LogDebug(fmt.Sprintf("[%s] Channel error: %v", channel.Label(), err))
        c.logger.ShowError(fmt.Sprintf("%s channel error: %v", channel.Label(), err))
    })

    // Enhanced close logging
    channel.OnClose(func() {
        c.logger.LogDebug(fmt.Sprintf("[%s] Channel closed", channel.Label()))
        c.logger.ShowError(fmt.Sprintf("%s channel closed unexpectedly", channel.Label()))
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
    // Set up data channel configurations with retransmission settings
    maxRetransmits := uint16(0) // No additional retransmits for control channel
    
    controlConfig := &pionwebrtc.DataChannelInit{
        ID:              &controlID,
        Ordered:         &ordered,
        Negotiated:      &negotiated,
        Protocol:        &protocol,
        MaxRetransmits:  &maxRetransmits,
    }

    dataConfig := &pionwebrtc.DataChannelInit{
        ID:              &dataID,
        Ordered:         &ordered,
        Negotiated:      &negotiated,
    }

    // Create and configure the control channel
    controlChannel, err := peerConn.CreateDataChannel("p2pftp-control", controlConfig)
    if err != nil {
        return fmt.Errorf("failed to create control channel: %v", err)
    }
    // Configure control channel buffer
    controlChannel.SetBufferedAmountLowThreshold(uint64(controlBufferSize))
    c.logger.LogDebug(fmt.Sprintf("Control channel created with buffer threshold: %d bytes", controlBufferSize))

    // Create and configure the data channel
    dataChannel, err := peerConn.CreateDataChannel("p2pftp-data", dataConfig)
    if err != nil {
        return fmt.Errorf("failed to create data channel: %v", err)
    }
    
    // Use default chunk size from config for initial buffering
    dataChannel.SetBufferedAmountLowThreshold(uint64(defaultChunkSize))
    c.logger.LogDebug(fmt.Sprintf("Data channel created with initial buffer threshold: %d bytes", defaultChunkSize))

    c.state.ControlChannel = controlChannel
    c.state.DataChannel = dataChannel

    // Log initial channel states and buffer configurations
    c.logger.LogDebug("Initial channel configuration:")
    c.logger.LogDebug(fmt.Sprintf("- Control channel: state=%s, buffer=%d, threshold=%d", 
        controlChannel.ReadyState().String(),
        controlChannel.BufferedAmount(),
        controlChannel.BufferedAmountLowThreshold()))
    c.logger.LogDebug(fmt.Sprintf("- Data channel: state=%s, buffer=%d, threshold=%d", 
        dataChannel.ReadyState().String(),
        dataChannel.BufferedAmount(),
        dataChannel.BufferedAmountLowThreshold()))

    // Set up message handlers for control and data channels
    controlChannel.OnMessage(func(msg pionwebrtc.DataChannelMessage) {
        msgSize := len(msg.Data)
        c.logger.LogDebug(fmt.Sprintf("[Control] Message [%s]: %d bytes", 
            map[bool]string{true: "string", false: "binary"}[msg.IsString],
            msgSize))

        if !msg.IsString {
            c.logger.LogDebug("[Control] WARNING: Unexpected binary data")
            return
        }
        if c.handleControl != nil {
            c.handleControl([]byte(msg.Data))
        }
    })

    // Log and handle data channel messages
    dataChannel.OnMessage(func(msg pionwebrtc.DataChannelMessage) {
        msgSize := len(msg.Data)
        c.logger.LogDebug(fmt.Sprintf("[Data] Message [%s]: %d bytes", 
            map[bool]string{true: "string", false: "binary"}[msg.IsString],
            msgSize))

        if msg.IsString {
            // Parse the message to check if it's a chat message
            var message struct {
                Type    string `json:"type"`
                Content string `json:"content"`
            }
            if err := json.Unmarshal(msg.Data, &message); err == nil && message.Type == "message" {
                // Handle chat message
                if c.handleControl != nil {
                    c.handleControl(msg.Data)
                }
            } else {
                c.logger.LogDebug("[Data] WARNING: Unexpected string message")
            }
        } else if msgSize >= 8 && c.handleData != nil {
            // Handle binary data for file transfers
            c.handleData(msg.Data)
        } else {
            c.logger.LogDebug("[Data] WARNING: Invalid binary message size")
        }
    })

    // Set up error and close handlers with detailed state information
    controlChannel.OnError(func(err error) {
        c.logger.LogDebug(fmt.Sprintf("[Control] Channel error: %v (buffer=%d, threshold=%d)", 
            err, controlChannel.BufferedAmount(), controlChannel.BufferedAmountLowThreshold()))
        c.logger.ShowError(fmt.Sprintf("Control channel error: %v", err))
    })

    dataChannel.OnError(func(err error) {
        c.logger.LogDebug(fmt.Sprintf("[Data] Channel error: %v (buffer=%d, threshold=%d)", 
            err, dataChannel.BufferedAmount(), dataChannel.BufferedAmountLowThreshold()))
        c.logger.ShowError(fmt.Sprintf("Data channel error: %v", err))
    })

    controlChannel.OnClose(func() {
        c.logger.LogDebug(fmt.Sprintf("[Control] Channel closed (final buffer=%d)", controlChannel.BufferedAmount()))
        c.logger.ShowError("Control channel closed unexpectedly")
    })

    dataChannel.OnClose(func() {
        c.logger.LogDebug(fmt.Sprintf("[Data] Channel closed (final buffer=%d)", dataChannel.BufferedAmount()))
        c.logger.ShowError("Data channel closed unexpectedly")
    })

    // Configure buffer monitoring for flow control
    dataChannel.OnBufferedAmountLow(func() {
        c.logger.LogDebug(fmt.Sprintf("[Data] Buffer amount: %d bytes (below threshold: %d)", 
            dataChannel.BufferedAmount(),
            dataChannel.BufferedAmountLowThreshold()))
    })
    controlChannel.OnBufferedAmountLow(func() {
        c.logger.LogDebug(fmt.Sprintf("[Control] Buffer amount: %d bytes (below threshold: %d)", 
            controlChannel.BufferedAmount(),
            controlChannel.BufferedAmountLowThreshold()))
        
        // When buffer is low, check if we need to update the threshold based on negotiated chunk size
        if c.maxChunkSize > defaultChunkSize {
            newThreshold := uint64(c.maxChunkSize)
            if newThreshold > dataChannel.BufferedAmountLowThreshold() {
                c.logger.LogDebug(fmt.Sprintf("[Data] Updating buffer threshold to %d bytes", newThreshold))
                dataChannel.SetBufferedAmountLowThreshold(newThreshold)
            }
        }
    })

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
        MaxChunkSize: min(c.maxChunkSize, maxMessageSize-8), // Ensure we don't exceed WebRTC message size limit
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
