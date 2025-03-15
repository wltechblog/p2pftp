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
}

// NewReceiver creates a new file receiver
func NewReceiver(
	controlChannel *webrtc.DataChannel,
	dataChannel *webrtc.DataChannel,
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

// HandleFileInfo handles a file info message
func (r *Receiver) HandleFileInfo(data map[string]interface{}) error {
	if r.state.inProgress {
		return fmt.Errorf("download already in progress")
	}

	infoMap, ok := data["info"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid file info format")
	}

	name, _ := infoMap["name"].(string)
	size, _ := infoMap["size"].(float64)
	md5, _ := infoMap["md5"].(string)

	// Create a unique filename to avoid overwriting existing files
	baseName := filepath.Base(name)
	ext := filepath.Ext(baseName)
	nameWithoutExt := baseName[:len(baseName)-len(ext)]

	filePath := name
	counter := 1
	for {
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			break
		}
		filePath = fmt.Sprintf("%s-%d%s", nameWithoutExt, counter, ext)
		counter++
	}

	// Create file
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %v", err)
	}

	// Calculate total chunks
	totalChunks := int(math.Ceil(float64(size) / float64(r.chunkSize)))

	// Initialize transfer state
	r.state = &TransferState{
		inProgress: true,
		startTime:  time.Now(),
		lastUpdate: time.Now(),
		fileTransfer: &FileTransfer{
			FileInfo: &FileInfo{
				Name: name,
				Size: int64(size),
				MD5:  md5,
			},
			file:     file,
			filePath: filePath,
		},
		chunks:              make([][]byte, totalChunks),
		totalChunks:         totalChunks,
		lastReceivedSequence: -1,
		receivedChunks:      make(map[int]bool),
		missingChunks:       make(map[int]bool),
	}

	// Start a timer to check for missing chunks
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for r.state.inProgress {
			<-ticker.C
			r.checkForMissingChunks()
		}
	}()

	r.logger.LogDebug(fmt.Sprintf("Receiving file: %s (%d bytes, %d chunks)", name, int64(size), totalChunks))
	return nil
}

// HandleFileInfoUpdate handles a file info update message
func (r *Receiver) HandleFileInfoUpdate(data map[string]interface{}) error {
	if !r.state.inProgress {
		return fmt.Errorf("no download in progress")
	}

	infoMap, ok := data["info"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid file info update format")
	}

	// Update MD5 hash if provided
	if md5, ok := infoMap["md5"].(string); ok && md5 != "" {
		r.state.fileTransfer.MD5 = md5
		r.logger.LogDebug(fmt.Sprintf("Updated file MD5 hash: %s", md5))
	}

	return nil
}

// HandleBinaryChunkData handles binary chunk data
func (r *Receiver) HandleBinaryChunkData(sequence int, total int, size int, data []byte) error {
	if !r.state.inProgress {
		return fmt.Errorf("received chunk but no download in progress")
	}

	// Store the chunk in memory
	if sequence >= len(r.state.chunks) {
		return fmt.Errorf("received chunk with sequence %d beyond expected total %d", 
			sequence, len(r.state.chunks))
	}

	// Store the chunk data
	r.state.chunks[sequence] = data
	r.state.receivedChunks[sequence] = true

	// Remove from missing chunks if it was there
	delete(r.state.missingChunks, sequence)

	// Send confirmation
	confirm := ChunkConfirm{
		Type:     "chunk-confirm",
		Sequence: sequence,
	}

	confirmJSON, err := json.Marshal(confirm)
	if err != nil {
		return fmt.Errorf("failed to marshal chunk confirmation: %v", err)
	}

	// Check if control channel is still valid
	if r.controlChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("control channel is not in open state (current state: %s)",
			r.controlChannel.ReadyState().String())
	}

	err = r.controlChannel.SendText(string(confirmJSON))
	if err != nil {
		r.logger.LogDebug(fmt.Sprintf("Error sending chunk confirmation: %v", err))
		return fmt.Errorf("failed to send chunk confirmation: %v", err)
	}

	// Update in-order sequence tracking
	if sequence == r.state.lastReceivedSequence+1 {
		r.state.lastReceivedSequence = sequence

		// Check for any consecutive chunks we've already received
		nextSeq := sequence + 1
		for {
			if _, ok := r.state.receivedChunks[nextSeq]; ok {
				r.state.lastReceivedSequence = nextSeq
				nextSeq++
			} else {
				break
			}
		}
	}

	// Update progress
	r.state.receivedSize += int64(size)
	now := time.Now()
	if time.Since(r.state.lastUpdate) > 100*time.Millisecond {
		timeDiff := now.Sub(r.state.lastUpdate).Seconds()
		if timeDiff > 0 {
			speed := float64(r.state.receivedSize-r.state.lastUpdateSize) / timeDiff
			percentage := int((float64(r.state.receivedSize) / float64(r.state.fileTransfer.Size)) * 100)
			r.progressCallback(fmt.Sprintf("⬇ %s [%d%%] (%.1f MB/s)",
				r.state.fileTransfer.Name,
				percentage,
				speed/1024/1024),
				"receive")
			r.state.lastUpdate = now
			r.state.lastUpdateSize = r.state.receivedSize
		}
	}

	// Check if we've received all chunks
	if r.state.lastReceivedSequence == total-1 {
		// Check if channels are still valid before completing
		if r.controlChannel.ReadyState() != webrtc.DataChannelStateOpen || 
		   r.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
			r.logger.LogDebug(fmt.Sprintf("Channels not in open state, but all chunks received - completing anyway (control: %s, data: %s)",
				r.controlChannel.ReadyState().String(), r.dataChannel.ReadyState().String()))
		}
		
		r.handleFileComplete()
	}

	return nil
}

// checkForMissingChunks checks for missing chunks and requests them
func (r *Receiver) checkForMissingChunks() {
	if !r.state.inProgress {
		return
	}
	
	// Check if channels are still valid
	if r.controlChannel.ReadyState() != webrtc.DataChannelStateOpen || 
	   r.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		r.logger.LogDebug(fmt.Sprintf("Cannot check for missing chunks: channels not in open state (control: %s, data: %s)",
			r.controlChannel.ReadyState().String(), r.dataChannel.ReadyState().String()))
		return
	}

	// Check for missing chunks
	for i := 0; i <= r.state.lastReceivedSequence; i++ {
		if _, ok := r.state.receivedChunks[i]; !ok {
			r.state.missingChunks[i] = true
		}
	}

	// Request missing chunks
	for seq := range r.state.missingChunks {
		request := struct {
			Type     string `json:"type"`
			Sequence int    `json:"sequence"`
		}{
			Type:     "chunk-request",
			Sequence: seq,
		}

		requestJSON, err := json.Marshal(request)
		if err != nil {
			r.logger.ShowError(fmt.Sprintf("Failed to marshal chunk request: %v", err))
			continue
		}

		// Check if control channel is still valid
		if r.controlChannel.ReadyState() != webrtc.DataChannelStateOpen {
			r.logger.ShowError(fmt.Sprintf("Control channel is not in open state (current state: %s)",
				r.controlChannel.ReadyState().String()))
			continue
		}

		err = r.controlChannel.SendText(string(requestJSON))
		if err != nil {
			r.logger.LogDebug(fmt.Sprintf("Error sending chunk request: %v", err))
			r.logger.ShowError(fmt.Sprintf("Failed to send chunk request: %v", err))
			continue
		}

		r.logger.LogDebug(fmt.Sprintf("Requested missing chunk %d", seq))
	}
}

// handleFileComplete handles file completion
func (r *Receiver) handleFileComplete() error {
	if !r.state.inProgress {
		return fmt.Errorf("no download in progress")
	}
	
	// Check if file transfer is still valid
	if r.state.fileTransfer == nil || r.state.fileTransfer.file == nil {
		r.logger.LogDebug("Cannot complete file transfer: file handle is not valid")
		r.state = NewTransferState()
		return fmt.Errorf("file handle is not valid")
	}

	// Reopen the file for writing from the beginning
	file, err := os.Create(r.state.fileTransfer.filePath)
	if err != nil {
		r.logger.ShowError(fmt.Sprintf("Failed to reopen file for writing: %v", err))
		r.state = NewTransferState()
		return fmt.Errorf("failed to reopen file for writing: %v", err)
	}
	defer file.Close()

	// Calculate the total expected file size
	expectedSize := r.state.fileTransfer.Size

	// Write all chunks to the file in order
	var totalWritten int64
	for i := 0; i < r.state.totalChunks; i++ {
		if i >= len(r.state.chunks) || r.state.chunks[i] == nil {
			r.logger.ShowError(fmt.Sprintf("Missing chunk %d when finalizing file", i))
			r.state = NewTransferState()
			return fmt.Errorf("missing chunk %d when finalizing file", i)
		}

		n, err := file.Write(r.state.chunks[i])
		if err != nil {
			r.logger.ShowError(fmt.Sprintf("Failed to write chunk %d: %v", i, err))
			r.state = NewTransferState()
			return fmt.Errorf("failed to write chunk %d: %v", i, err)
		}

		totalWritten += int64(n)
	}

	// Verify the file size
	if totalWritten != expectedSize {
		r.logger.ShowError(fmt.Sprintf("File size mismatch: expected %d bytes, got %d bytes", 
			expectedSize, totalWritten))
	}

	// Calculate MD5 hash of the received file
	receivedHash, err := CalculateMD5(r.state.fileTransfer.filePath)
	if err != nil {
		r.logger.ShowError(fmt.Sprintf("Failed to calculate received file hash: %v", err))
	} else if r.state.fileTransfer.MD5 != "" && 
		receivedHash != r.state.fileTransfer.MD5 {
		r.logger.ShowError(fmt.Sprintf("File hash mismatch: expected %s, got %s", 
			r.state.fileTransfer.MD5, receivedHash))
	}

	// Show completion message
	avgSpeed := float64(r.state.fileTransfer.Size) / time.Since(r.state.startTime).Seconds()
	r.progressCallback(fmt.Sprintf("⬇ %s - Complete (avg: %.1f MB/s)",
		r.state.fileTransfer.Name,
		avgSpeed/1024/1024),
		"receive")

	// Reset transfer state
	r.state = NewTransferState()

	return nil
}

// HandleFileComplete handles a file complete message
func (r *Receiver) HandleFileComplete() error {
	return r.handleFileComplete()
}

// HandleChunkConfirm handles a chunk confirmation
func (r *Receiver) HandleChunkConfirm(sequence int) error {
	// Receiver doesn't need to handle chunk confirmations
	return nil
}