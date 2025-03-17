package webrtc

import (
	"encoding/json"
	"fmt"

	"github.com/pion/webrtc/v3"
)

// ConnectionState represents the state of a WebRTC connection
type ConnectionState struct {
	PeerConn       *webrtc.PeerConnection
	ControlChannel *webrtc.DataChannel
	DataChannel    *webrtc.DataChannel
	Connected      bool
	IsInitiator    bool
	PeerToken      string
	SendTransfer   *TransferState
	ReceiveTransfer *TransferState
}

// TransferState contains the state of a file transfer
type TransferState struct {
	InProgress          bool
	FileTransfer        *FileTransfer
	Chunks              [][]byte
	TotalChunks         int
	LastReceivedSequence int
	ReceivedChunks      map[int]bool
	MissingChunks       map[int]bool
	ReceivedSize        int64
	LastUpdateSize      int64
	StartTime           string
	LastUpdate          string
	WindowSize          int
	NextSequenceToSend  int
	LastAckedSequence   int
	UnacknowledgedChunks map[int]bool
	RetransmissionQueue []int
	ChunkTimestamps     map[int]string
	CongestionWindow    int
	ConsecutiveTimeouts int
	ConfirmHandler      func(int)
}

// FileTransfer contains information about a file transfer
type FileTransfer struct {
	FileInfo *FileInfo
	File     interface{}
	FilePath string
}

// FileInfo contains metadata about a file being transferred
type FileInfo struct {
	Name string
	Size int64
	MD5  string
}

// Logger interface for logging
type Logger interface {
	LogDebug(msg string)
	ShowError(msg string)
}

// ConnectionCallback is called when the connection state changes
type ConnectionCallback func()

// Connection manages a WebRTC connection
type Connection struct {
	state            *ConnectionState
	logger           Logger
	connectionSetup  ConnectionCallback
	maxChunkSize     int
	maxMessageSize   int
}

// NewConnection creates a new WebRTC connection
func NewConnection(
	logger Logger,
	connectionSetup ConnectionCallback,
	maxChunkSize int,
	maxMessageSize int,
) *Connection {
	return &Connection{
		state: &ConnectionState{
			Connected:      false,
			SendTransfer:   &TransferState{},
			ReceiveTransfer: &TransferState{},
		},
		logger:           logger,
		connectionSetup:  connectionSetup,
		maxChunkSize:     maxChunkSize,
		maxMessageSize:   maxMessageSize,
	}
}

// SetupPeerConnection creates and configures a new WebRTC peer connection
func (c *Connection) SetupPeerConnection() error {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{
					"stun:stun.l.google.com:19302",
					"stun:stun1.l.google.com:19302",
				},
			},
		},
	}

	// Create a new RTCPeerConnection
	peerConn, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return fmt.Errorf("failed to create peer connection: %v", err)
	}

	// Create a data channel for control messages
	controlChannel, err := peerConn.CreateDataChannel("control", &webrtc.DataChannelInit{
		Ordered: &[]bool{true}[0], // Ordered delivery
	})
	if err != nil {
		peerConn.Close()
		return fmt.Errorf("failed to create control channel: %v", err)
	}

	// Create a data channel for binary data
	dataChannel, err := peerConn.CreateDataChannel("data", &webrtc.DataChannelInit{
		Ordered: &[]bool{true}[0], // Ordered delivery
	})
	if err != nil {
		peerConn.Close()
		return fmt.Errorf("failed to create data channel: %v", err)
	}

	// Set binary type for the data channel
	dataChannel.SetBufferedAmountLowThreshold(262144) // 256KB

	// Set up control channel handlers
	controlChannel.OnOpen(func() {
		c.logger.LogDebug("Control channel opened")

		// Store the control channel
		c.state.ControlChannel = controlChannel

		// Check if both channels are open
		if c.state.DataChannel != nil && c.state.DataChannel.ReadyState() == webrtc.DataChannelStateOpen {
			c.completeConnectionSetup()
		}
	})

	controlChannel.OnClose(func() {
		c.logger.LogDebug("Control channel closed")
		c.Disconnect()
	})
	
	// Handle control channel state changes
	controlChannel.OnBufferedAmountLow(func() {
		c.logger.LogDebug("Control channel buffer amount low")
	})
	
	// Add state change handler
	controlChannel.OnError(func(err error) {
		c.logger.LogDebug(fmt.Sprintf("Control channel error: %v", err))
		
		// If we're in the middle of a transfer, log it but don't disconnect
		if c.state.SendTransfer.InProgress || c.state.ReceiveTransfer.InProgress {
			c.logger.LogDebug("Control channel error during transfer - continuing with best effort")
		} else {
			// If we're not transferring, it's safer to disconnect
			c.logger.LogDebug("Control channel error - disconnecting")
			c.Disconnect()
		}
	})

	// Set up data channel handlers
	dataChannel.OnOpen(func() {
		c.logger.LogDebug("Data channel opened")

		// Store the data channel
		c.state.DataChannel = dataChannel

		// Check if both channels are open
		if c.state.ControlChannel != nil && c.state.ControlChannel.ReadyState() == webrtc.DataChannelStateOpen {
			c.completeConnectionSetup()
		}
	})

	dataChannel.OnClose(func() {
		c.logger.LogDebug("Data channel closed")
		c.Disconnect()
	})
	
	// Handle data channel state changes
	dataChannel.OnBufferedAmountLow(func() {
		c.logger.LogDebug("Data channel buffer amount low")
	})
	
	// Add state change handler
	dataChannel.OnError(func(err error) {
		c.logger.LogDebug(fmt.Sprintf("Data channel error: %v", err))
		
		// If we're in the middle of a transfer, log it but don't disconnect
		if c.state.SendTransfer.InProgress || c.state.ReceiveTransfer.InProgress {
			c.logger.LogDebug("Data channel error during transfer - continuing with best effort")
		} else {
			// If we're not transferring, it's safer to disconnect
			c.logger.LogDebug("Data channel error - disconnecting")
			c.Disconnect()
		}
	})

	// Store the peer connection
	c.state.PeerConn = peerConn
	
	// Log the initial connection state
	c.logger.LogDebug(fmt.Sprintf("Initial peer connection state: %s", peerConn.ConnectionState().String()))

	return nil
}

// completeConnectionSetup is called when both channels are open
func (c *Connection) completeConnectionSetup() {
	// Double-check that both channels are actually initialized
	if c.state.ControlChannel == nil || c.state.DataChannel == nil {
		c.logger.LogDebug("Cannot complete connection setup: one or both channels are not initialized")
		return
	}
	
	// Check that both channels are in the open state
	if c.state.ControlChannel.ReadyState() != webrtc.DataChannelStateOpen {
		c.logger.LogDebug(fmt.Sprintf("Cannot complete connection setup: control channel is not open (state: %s)",
			c.state.ControlChannel.ReadyState().String()))
		return
	}
	
	if c.state.DataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		c.logger.LogDebug(fmt.Sprintf("Cannot complete connection setup: data channel is not open (state: %s)",
			c.state.DataChannel.ReadyState().String()))
		return
	}
	
	// Check peer connection state
	if c.state.PeerConn != nil && c.state.PeerConn.ConnectionState() != webrtc.PeerConnectionStateConnected {
		c.logger.LogDebug(fmt.Sprintf("Warning: Peer connection is not in connected state (state: %s)",
			c.state.PeerConn.ConnectionState().String()))
	}

	c.state.Connected = true
	c.logger.LogDebug(fmt.Sprintf("Both channels ready for transfer. Control: %s, Data: %s",
		c.state.ControlChannel.ReadyState().String(), c.state.DataChannel.ReadyState().String()))
	
	// Call the connection setup callback
	if c.connectionSetup != nil {
		c.connectionSetup()
	}
	
	// Notify the client that the channels are ready
	c.OnChannelsReady()

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
			if c.state.ControlChannel.ReadyState() == webrtc.DataChannelStateOpen {
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

// Disconnect closes the WebRTC connection
func (c *Connection) Disconnect() {
	// Close WebRTC connections
	if c.state.PeerConn != nil {
		// Log the connection state before closing
		c.logger.LogDebug(fmt.Sprintf("Closing peer connection (state: %s)", c.state.PeerConn.ConnectionState().String()))
		c.state.PeerConn.Close()
		c.state.PeerConn = nil
	}
	
	// Close control channel
	if c.state.ControlChannel != nil {
		// Log the channel state before closing
		c.logger.LogDebug(fmt.Sprintf("Closing control channel (state: %s)", c.state.ControlChannel.ReadyState().String()))
		c.state.ControlChannel.Close()
		c.state.ControlChannel = nil
	}
	
	// Close data channel
	if c.state.DataChannel != nil {
		// Log the channel state before closing
		c.logger.LogDebug(fmt.Sprintf("Closing data channel (state: %s)", c.state.DataChannel.ReadyState().String()))
		c.state.DataChannel.Close()
		c.state.DataChannel = nil
	}
	
	// Reset the connection state
	c.state.Connected = false
	c.state.SendTransfer = &TransferState{}
	c.state.ReceiveTransfer = &TransferState{}
	
	c.logger.LogDebug("Peer connection cleaned up")
}

// GetControlChannel returns the control channel
func (c *Connection) GetControlChannel() *webrtc.DataChannel {
	return c.state.ControlChannel
}

// GetDataChannel returns the data channel
func (c *Connection) GetDataChannel() *webrtc.DataChannel {
	return c.state.DataChannel
}

// OnChannelsReady is called when both channels are ready
func (c *Connection) OnChannelsReady() {
	// This is a hook for clients to implement
	c.logger.LogDebug("Channels are ready")
}

// SetPeerToken sets the peer token in the connection state
func (c *Connection) SetPeerToken(token string) {
	c.logger.LogDebug(fmt.Sprintf("Setting peer token: %s", token))
	c.state.PeerToken = token
}

// GetPeerToken gets the peer token from the connection state
func (c *Connection) GetPeerToken() string {
	return c.state.PeerToken
}