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

// Connection manages a WebRTC peer connection
type Connection struct {
state            *ConnectionState
logger           Logger
connectionSetup  func()
channelsReady    func()
maxChunkSize     int
maxMessageSize   int
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

// SetupPeerConnection initializes the WebRTC peer connection
func (c *Connection) SetupPeerConnection() error {
// Create a new RTCPeerConnection
config := pionwebrtc.Configuration{
ICEServers: []pionwebrtc.ICEServer{
{
URLs: []string{"stun:stun.l.google.com:19302"},
},
},
}

peerConn, err := pionwebrtc.NewPeerConnection(config)
if err != nil {
return fmt.Errorf("failed to create peer connection: %v", err)
}

c.state.PeerConn = peerConn

// Set up OnDataChannel handler for answering peer
peerConn.OnDataChannel(func(channel *pionwebrtc.DataChannel) {
    c.logger.LogDebug(fmt.Sprintf("Received data channel: %s", channel.Label()))
    
    if channel.Label() == "control" {
        c.state.ControlChannel = channel
        channel.OnOpen(func() {
            c.logger.LogDebug("Control channel opened (answering peer)")
            if c.state.DataChannel != nil && c.state.DataChannel.ReadyState() == pionwebrtc.DataChannelStateOpen {
                c.completeConnectionSetup()
            }
        })
    } else if channel.Label() == "data" {
        c.state.DataChannel = channel
        channel.OnOpen(func() {
            c.logger.LogDebug("Data channel opened (answering peer)")
            if c.state.ControlChannel != nil && c.state.ControlChannel.ReadyState() == pionwebrtc.DataChannelStateOpen {
                c.completeConnectionSetup()
            }
        })
    }
})

// Set up data channels for offering peer
controlChannel, err := peerConn.CreateDataChannel("control", nil)
if err != nil {
return fmt.Errorf("failed to create control channel: %v", err)
}

dataChannel, err := peerConn.CreateDataChannel("data", nil)
if err != nil {
return fmt.Errorf("failed to create data channel: %v", err)
}

c.state.ControlChannel = controlChannel
c.state.DataChannel = dataChannel

// Set up channel open handlers
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
