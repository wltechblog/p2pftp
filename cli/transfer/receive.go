package transfer

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/pion/webrtc/v3"
)

// Receiver handles receiving files
type Receiver struct {
	state            *TransferState
	controlChannel   *webrtc.DataChannel
	dataChannel      *webrtc.DataChannel
	logger           Logger
	progressCallback ProgressCallback
	chunkSize        int
	downloadDir      string
}

// MessageHandler is an interface for handling control messages
type MessageHandler interface {
	HandleControlMessage(msg []byte) error
}

// ChunkHandler is an interface for handling data chunks
type ChunkHandler interface {
	HandleDataChunk(data []byte) error
}

// NewReceiver creates a new file receiver
func NewReceiver(
	controlChannel *webrtc.DataChannel,
	dataChannel *webrtc.DataChannel,
	logger Logger,
	progressCallback ProgressCallback,
	chunkSize int,
) *Receiver {
	// Default to current directory for downloads
	downloadDir, err := os.Getwd()
	if err != nil {
		downloadDir = "."
	}

	return &Receiver{
		state:            NewTransferState(),
		controlChannel:   controlChannel,
		dataChannel:      dataChannel,
		logger:           logger,
		progressCallback: progressCallback,
		chunkSize:        chunkSize,
		downloadDir:      downloadDir,
	}
}

// SetDownloadDirectory sets the directory where files will be saved
func (r *Receiver) SetDownloadDirectory(dir string) {
	r.downloadDir = dir
}

// HandleControlMessage handles control channel messages
func (r *Receiver) HandleControlMessage(msg []byte) error {
	// Log the raw message
	r.logger.LogDebug(fmt.Sprintf("HandleControlMessage received raw message: %s", string(msg)))
	
	// Parse the message
	var message map[string]interface{}
	err := json.Unmarshal(msg, &message)
	if err != nil {
		r.logger.LogDebug(fmt.Sprintf("Failed to parse control message: %v", err))
		return fmt.Errorf("failed to parse control message: %v", err)
	}

	// Log the parsed message
	r.logger.LogDebug(fmt.Sprintf("Parsed message: %+v", message))

	// Get the message type
	msgType, ok := message["type"].(string)
	if !ok {
		r.logger.LogDebug("Invalid message format: missing type")
		return fmt.Errorf("invalid message format: missing type")
	}
	
	r.logger.LogDebug(fmt.Sprintf("Message type: %s", msgType))

	// Handle different message types
	switch msgType {
	case "file-info":
		return r.handleFileInfo(message)
	case "chunk-info":
		return r.handleChunkInfo(message)
	case "file-complete":
		return r.handleFileComplete()
	case "message":
		// Handle chat message
		r.logger.LogDebug("CHAT MESSAGE RECEIVED IN HANDLER")
		
		content, ok := message["content"].(string)
		if !ok {
			r.logger.LogDebug("ERROR: Invalid message format: missing content")
			return fmt.Errorf("invalid message format: missing content")
		}
		
		r.logger.LogDebug(fmt.Sprintf("Chat message content: '%s'", content))
		
		// Process the chat message in a separate goroutine to avoid blocking the WebRTC thread
		go func(content string) {
			// Display the chat message
			r.logger.LogDebug("Calling AppendChat with formatted message from goroutine")
			formattedMsg := fmt.Sprintf("[yellow]Peer[white] %s", content)
			r.logger.LogDebug(fmt.Sprintf("Formatted message: '%s'", formattedMsg))
			
			r.logger.AppendChat(formattedMsg)
			r.logger.LogDebug("AppendChat called successfully from goroutine")
			
			// Double-check that the logger implements the AppendChat method
			if _, ok := r.logger.(interface{ AppendChat(string) }); !ok {
				r.logger.LogDebug("WARNING: logger does not implement AppendChat method")
			} else {
				r.logger.LogDebug("Logger implements AppendChat method")
			}
		}(content)
		
		return nil
	case "capabilities":
		return r.handleCapabilities(message)
	case "capabilities-ack":
		return r.handleCapabilitiesAck(message)
	case "chunk-ack":
		return r.handleChunkAck(message)
	case "chunk-request":
		return r.handleChunkRequest(message)
	default:
		r.logger.LogDebug(fmt.Sprintf("Unknown message type: %s", msgType))
		return fmt.Errorf("unknown message type: %s", msgType)
	}
}

// HandleDataChunk handles data chunks
func (r *Receiver) HandleDataChunk(data []byte) error {
	// Check if we're in a transfer
	if !r.state.inProgress {
		return fmt.Errorf("received data chunk but no transfer is in progress")
	}

	// Process the chunk
	return r.processChunk(data)
}

// handleFileInfo handles file info messages
func (r *Receiver) handleFileInfo(message map[string]interface{}) error {
	// Extract file info
	infoMap, ok := message["info"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid file info: missing info map")
	}

	name, ok := infoMap["name"].(string)
	if !ok {
		return fmt.Errorf("invalid file info: missing name")
	}

	size, ok := infoMap["size"].(float64)
	if !ok {
		return fmt.Errorf("invalid file info: missing size")
	}

	md5, ok := infoMap["md5"].(string)
	if !ok {
		return fmt.Errorf("invalid file info: missing md5")
	}

	// Create file info
	fileInfo := &FileInfo{
		Name: name,
		Size: int64(size),
		MD5:  md5,
	}

	// Create file path
	filePath := filepath.Join(r.downloadDir, name)

	// Create file transfer
	fileTransfer := &FileTransfer{
		FileInfo: fileInfo,
		filePath: filePath,
	}

	// Create file
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %v", err)
	}

	// Store file
	fileTransfer.file = file

	// Store file transfer
	r.state.fileTransfer = fileTransfer

	// Mark transfer as in progress
	r.state.inProgress = true

	// Initialize transfer state
	r.state.receivedChunks = make(map[int]bool)
	r.state.missingChunks = make(map[int]bool)
	r.state.receivedSize = 0
	r.state.lastUpdateSize = 0
	r.state.startTime = time.Now()
	r.state.lastUpdate = time.Now()

	// Log file info
	r.logger.LogDebug(fmt.Sprintf("Receiving file: %s (%d bytes)", name, int64(size)))

	// Update progress
	r.updateProgress()

	// Send acknowledgement
	ack := map[string]interface{}{
		"type": "file-info-ack",
		"name": name,
	}

	ackJSON, err := json.Marshal(ack)
	if err != nil {
		return fmt.Errorf("failed to marshal ack: %v", err)
	}

	err = r.controlChannel.SendText(string(ackJSON))
	if err != nil {
		return fmt.Errorf("failed to send ack: %v", err)
	}

	return nil
}

// handleChunkInfo handles chunk info messages
func (r *Receiver) handleChunkInfo(message map[string]interface{}) error {
	// Extract chunk info
	sequence, ok := message["sequence"].(float64)
	if !ok {
		return fmt.Errorf("invalid chunk info: missing sequence")
	}

	size, ok := message["size"].(float64)
	if !ok {
		return fmt.Errorf("invalid chunk info: missing size")
	}

	// Store chunk info
	r.state.lastReceivedSequence = int(sequence)

	// Log chunk info
	r.logger.LogDebug(fmt.Sprintf("Receiving chunk %d (%d bytes)", int(sequence), int(size)))

	return nil
}

// handleFileComplete handles file complete messages
func (r *Receiver) handleFileComplete() error {
	// Check if we're in a transfer
	if !r.state.inProgress {
		return fmt.Errorf("received file complete but no transfer is in progress")
	}

	// Close file
	if r.state.fileTransfer.file != nil {
		err := r.state.fileTransfer.file.Close()
		if err != nil {
			return fmt.Errorf("failed to close file: %v", err)
		}
	}

	// Mark transfer as complete
	r.state.inProgress = false

	// Log completion
	r.logger.LogDebug(fmt.Sprintf("File transfer complete: %s", r.state.fileTransfer.FileInfo.Name))

	// Update progress
	r.progressCallback(fmt.Sprintf("⬇ %s - Complete", r.state.fileTransfer.FileInfo.Name), "receive")

	// Send acknowledgement
	ack := map[string]interface{}{
		"type": "file-complete-ack",
		"name": r.state.fileTransfer.FileInfo.Name,
	}

	ackJSON, err := json.Marshal(ack)
	if err != nil {
		return fmt.Errorf("failed to marshal ack: %v", err)
	}

	err = r.controlChannel.SendText(string(ackJSON))
	if err != nil {
		return fmt.Errorf("failed to send ack: %v", err)
	}

	return nil
}

// handleCapabilities handles capabilities messages
func (r *Receiver) handleCapabilities(message map[string]interface{}) error {
	// Extract capabilities
	maxChunkSize, ok := message["maxChunkSize"].(float64)
	if !ok {
		return fmt.Errorf("invalid capabilities: missing maxChunkSize")
	}

	// Store capabilities
	r.chunkSize = int(math.Min(float64(r.chunkSize), maxChunkSize))

	// Log capabilities
	r.logger.LogDebug(fmt.Sprintf("Peer capabilities: maxChunkSize=%d", int(maxChunkSize)))
	r.logger.LogDebug(fmt.Sprintf("Using chunk size: %d", r.chunkSize))

	// Send acknowledgement
	ack := map[string]interface{}{
		"type":               "capabilities-ack",
		"negotiatedChunkSize": r.chunkSize,
	}

	ackJSON, err := json.Marshal(ack)
	if err != nil {
		return fmt.Errorf("failed to marshal ack: %v", err)
	}

	err = r.controlChannel.SendText(string(ackJSON))
	if err != nil {
		return fmt.Errorf("failed to send ack: %v", err)
	}

	return nil
}

// handleCapabilitiesAck handles capabilities acknowledgement messages
func (r *Receiver) handleCapabilitiesAck(message map[string]interface{}) error {
	// Extract capabilities
	negotiatedChunkSize, ok := message["negotiatedChunkSize"].(float64)
	if !ok {
		return fmt.Errorf("invalid capabilities ack: missing negotiatedChunkSize")
	}

	// Store capabilities
	r.chunkSize = int(negotiatedChunkSize)

	// Log capabilities
	r.logger.LogDebug(fmt.Sprintf("Peer acknowledged capabilities: negotiatedChunkSize=%d", int(negotiatedChunkSize)))
	r.logger.LogDebug(fmt.Sprintf("Using chunk size: %d", r.chunkSize))

	return nil
}

// handleChunkAck handles chunk acknowledgement messages
func (r *Receiver) handleChunkAck(message map[string]interface{}) error {
	// Extract chunk info
	sequence, ok := message["sequence"].(float64)
	if !ok {
		return fmt.Errorf("invalid chunk ack: missing sequence")
	}

	// Log chunk ack
	r.logger.LogDebug(fmt.Sprintf("Peer acknowledged chunk %d", int(sequence)))

	return nil
}

// handleChunkRequest handles chunk request messages
func (r *Receiver) handleChunkRequest(message map[string]interface{}) error {
	// Extract chunk info
	sequence, ok := message["sequence"].(float64)
	if !ok {
		return fmt.Errorf("invalid chunk request: missing sequence")
	}

	// Log chunk request
	r.logger.LogDebug(fmt.Sprintf("Peer requested chunk %d", int(sequence)))

	return nil
}

// processChunk processes a data chunk
func (r *Receiver) processChunk(data []byte) error {
	// Check if we're in a transfer
	if !r.state.inProgress {
		return fmt.Errorf("received data chunk but no transfer is in progress")
	}

	// Check if file is available
	if r.state.fileTransfer.file == nil {
		return fmt.Errorf("file not initialized")
	}

	// Write chunk to file
	_, err := r.state.fileTransfer.file.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write chunk: %v", err)
	}

	// Update received size
	r.state.receivedSize += int64(len(data))

	// Mark chunk as received
	r.state.receivedChunks[r.state.lastReceivedSequence] = true

	// Update progress
	r.updateProgress()

	// Send acknowledgement
	ack := map[string]interface{}{
		"type":     "chunk-confirm",
		"sequence": r.state.lastReceivedSequence,
	}

	ackJSON, err := json.Marshal(ack)
	if err != nil {
		return fmt.Errorf("failed to marshal ack: %v", err)
	}

	err = r.controlChannel.SendText(string(ackJSON))
	if err != nil {
		return fmt.Errorf("failed to send ack: %v", err)
	}

	return nil
}

// updateProgress updates the transfer progress
func (r *Receiver) updateProgress() {
	// Check if we're in a transfer
	if !r.state.inProgress {
		return
	}

	// Calculate progress
	progress := float64(r.state.receivedSize) / float64(r.state.fileTransfer.FileInfo.Size) * 100

	// Update progress callback
	r.progressCallback(fmt.Sprintf("⬇ %s - %.1f%%", r.state.fileTransfer.FileInfo.Name, progress), "receive")

	// Update last update size
	r.state.lastUpdateSize = r.state.receivedSize

	// Update last update time
	r.state.lastUpdate = time.Now()
}