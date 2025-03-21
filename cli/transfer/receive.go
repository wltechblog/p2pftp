package transfer

import (
	"encoding/json"
	"fmt"

	pionwebrtc "github.com/pion/webrtc/v3"
)

// Receiver handles incoming file transfers and messages
type Receiver struct {
state            *TransferState
controlChannel   *pionwebrtc.DataChannel
dataChannel      *pionwebrtc.DataChannel
logger           Logger
progressCallback ProgressCallback
chunkSize        int
}

// NewReceiver creates a new receiver instance
func NewReceiver(
controlChannel *pionwebrtc.DataChannel,
dataChannel *pionwebrtc.DataChannel,
logger Logger,
progressCallback ProgressCallback,
chunkSize int,
) *Receiver {
return &Receiver{
state:            NewTransferState(),
controlChannel:   controlChannel,
dataChannel:      dataChannel,
logger:           logger,
progressCallback: progressCallback,
chunkSize:        chunkSize,
}
}

// handleFileInfo handles the file-info message
func (r *Receiver) handleFileInfo(message map[string]interface{}) {
r.logger.LogDebug("Handling file-info message")
// TODO: Implement file info handling
}

// handleChunkInfo handles the chunk-info message
func (r *Receiver) handleChunkInfo(message map[string]interface{}) {
r.logger.LogDebug("Handling chunk-info message")
// TODO: Implement chunk info handling
}

// handleFileComplete handles the file-complete message
func (r *Receiver) handleFileComplete() {
r.logger.LogDebug("Handling file-complete message")
// TODO: Implement file complete handling
}

// handleCapabilities handles the capabilities message
func (r *Receiver) handleCapabilities(message map[string]interface{}) {
r.logger.LogDebug("Handling capabilities message")
// TODO: Implement capabilities handling
}

// handleCapabilitiesAck handles the capabilities-ack message
func (r *Receiver) handleCapabilitiesAck(message map[string]interface{}) {
r.logger.LogDebug("Handling capabilities-ack message")
// TODO: Implement capabilities ack handling
}

// handleChunkAck handles the chunk-ack message
func (r *Receiver) handleChunkAck(message map[string]interface{}) {
r.logger.LogDebug("Handling chunk-ack message")
// TODO: Implement chunk ack handling
}

// handleChunkRequest handles the chunk-request message
func (r *Receiver) handleChunkRequest(message map[string]interface{}) {
r.logger.LogDebug("Handling chunk-request message")
// TODO: Implement chunk request handling
}

// HandleControlMessage handles control channel messages
// Implements MessageHandler interface from webrtc package
func (r *Receiver) HandleControlMessage(msg []byte) error {
// Log the raw message
r.logger.LogDebug(fmt.Sprintf("HandleControlMessage received raw message: %s", string(msg)))

// Parse the message in a separate goroutine to avoid blocking the WebRTC thread
go func(msgData []byte) {
r.logger.LogDebug("Processing control message in goroutine")

// Parse the message
var message map[string]interface{}
err := json.Unmarshal(msgData, &message)
if err != nil {
r.logger.LogDebug(fmt.Sprintf("Failed to parse control message: %v", err))
return
}

// Log the parsed message
r.logger.LogDebug(fmt.Sprintf("Parsed message: %+v", message))

// Get the message type
msgType, ok := message["type"].(string)
if !ok {
r.logger.LogDebug("Invalid message format: missing type")
return
}

r.logger.LogDebug(fmt.Sprintf("Message type: %s", msgType))

// Handle different message types
switch msgType {
case "file-info":
r.handleFileInfo(message)
case "chunk-info":
r.handleChunkInfo(message)
case "file-complete":
r.handleFileComplete()
case "message":
// Handle chat message
r.logger.LogDebug("CHAT MESSAGE RECEIVED IN HANDLER")

content, ok := message["content"].(string)
if !ok {
r.logger.LogDebug("ERROR: Invalid message format: missing content")
return
}

r.logger.LogDebug(fmt.Sprintf("Chat message content: '%s'", content))

// Display the chat message
r.logger.LogDebug("Calling AppendChat with formatted message")
formattedMsg := fmt.Sprintf("[yellow]Peer[white] %s", content)
r.logger.LogDebug(fmt.Sprintf("Formatted message: '%s'", formattedMsg))

r.logger.AppendChat(formattedMsg)
r.logger.LogDebug("AppendChat called successfully")

case "capabilities":
r.handleCapabilities(message)
case "capabilities-ack":
r.handleCapabilitiesAck(message)
case "chunk-ack":
r.handleChunkAck(message)
case "chunk-request":
r.handleChunkRequest(message)
default:
r.logger.LogDebug(fmt.Sprintf("Unknown message type: %s", msgType))
}
}(msg)

return nil
}

// HandleDataChunk handles receiving data chunks
// Implements DataHandler interface from webrtc package
func (r *Receiver) HandleDataChunk(data []byte) error {
r.logger.LogDebug(fmt.Sprintf("Received data chunk: %d bytes", len(data)))

// Extract sequence number from framed data
sequence := int(data[0])<<24 | int(data[1])<<16 | int(data[2])<<8 | int(data[3])

// Extract chunk size from framed data
chunkSize := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])

// Extract actual chunk data
chunkData := data[8:]

if len(chunkData) != chunkSize {
r.logger.LogDebug(fmt.Sprintf("ERROR: Chunk size mismatch. Expected: %d, Got: %d", chunkSize, len(chunkData)))
return fmt.Errorf("chunk size mismatch")
}

r.logger.LogDebug(fmt.Sprintf("Processing chunk %d (%d bytes)", sequence, chunkSize))
// TODO: Process the chunk data

return nil
}
