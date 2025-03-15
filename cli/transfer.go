package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/pion/webrtc/v3"
)

// ChunkConfirm message for acknowledging received chunks
type ChunkConfirm struct {
	Type     string `json:"type"`
	Sequence int    `json:"sequence"`
}

// SendFile initiates a file transfer to the connected peer
func (c *Client) SendFile(path string) error {
	// Create a mutex to protect access to the WebRTC state during the file transfer
	transferMutex := &sync.Mutex{}
	
	// First check if we're connected and not already transferring
	transferMutex.Lock()
	if !c.webrtc.connected {
		transferMutex.Unlock()
		return fmt.Errorf("not connected to peer")
	}

	if c.webrtc.sendTransfer.inProgress {
		transferMutex.Unlock()
		return fmt.Errorf("upload already in progress")
	}
	transferMutex.Unlock()
	
	// Make a local copy of the channels to prevent nil pointer issues if they change during transfer
	transferMutex.Lock()
	controlChannel := c.webrtc.controlChannel
	dataChannel := c.webrtc.dataChannel
	transferMutex.Unlock()
	
	// Ensure both data channels are initialized
	if controlChannel == nil || dataChannel == nil {
		return fmt.Errorf("connection not fully established, please wait or reconnect")
	}
	
	// Check that both channels are in the open state
	if controlChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("control channel is not ready (state: %s), please wait or reconnect",
			controlChannel.ReadyState().String())
	}
	
	if dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("data channel is not ready (state: %s), please wait or reconnect",
			dataChannel.ReadyState().String())
	}
	
	// Log the current state of the connection
	c.ui.LogDebug(fmt.Sprintf("Starting file transfer with control channel state: %s, data channel state: %s",
		controlChannel.ReadyState().String(), dataChannel.ReadyState().String()))

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %v", err)
	}

	// Calculate file hash
	fileHash, err := calculateMD5(path)
	if err != nil {
		return fmt.Errorf("failed to calculate file hash: %v", err)
	}

	// Calculate total chunks using fixedChunkSize for consistency
	totalChunks := int(math.Ceil(float64(info.Size()) / float64(fixedChunkSize)))

	// Initialize sliding window parameters
	c.webrtc.sendTransfer = transferState{
		inProgress: true,
		startTime:  time.Now(),
		fileTransfer: &FileTransfer{
			FileInfo: &FileInfo{
				Name: info.Name(),
				Size: info.Size(),
				MD5:  fileHash,
			},
			file: file,
		},
		windowSize:          64,  // Default window size
		nextSequenceToSend:  0,
		lastAckedSequence:   -1,
		unacknowledgedChunks: make(map[int]bool),
		retransmissionQueue: make([]int, 0),
		chunkTimestamps:     make(map[int]time.Time),
		congestionWindow:    64,  // Start with full window
		totalChunks:         totalChunks,
	}

	// Send file info to peer
	fileInfo := struct {
		Type string   `json:"type"`
		Info FileInfo `json:"info"`
	}{
		Type: "file-info",
		Info: FileInfo{
			Name: info.Name(),
			Size: info.Size(),
			MD5:  fileHash,
		},
	}

	infoJSON, err := json.Marshal(fileInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal file info: %v", err)
	}

	// Check if control channel is still valid
	if controlChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("control channel is not in open state (current state: %s)",
			controlChannel.ReadyState().String())
	}

	err = controlChannel.SendText(string(infoJSON))
	if err != nil {
		c.ui.LogDebug(fmt.Sprintf("Error sending file info: %v", err))
		return fmt.Errorf("failed to send file info: %v", err)
	}

	// Show initial status
	c.ui.UpdateTransferProgress(fmt.Sprintf("⬆ %s [0%%] (0/s)", info.Name()), "send")

	// Setup confirmation channel for chunk acknowledgments
	chunkConfirms := make(chan int, totalChunks)

	// Setup confirmation handler
	c.webrtc.sendTransfer.confirmHandler = func(sequence int) {
		chunkConfirms <- sequence
	}

	// Setup retransmission timer
	retransmitTicker := time.NewTicker(1 * time.Second)
	defer retransmitTicker.Stop()

	// Create a done channel to signal completion
	done := make(chan bool)

	// Start a goroutine to handle chunk confirmations
	go func() {
		for {
			select {
			case sequence := <-chunkConfirms:
				c.handleChunkConfirmation(sequence)
			case <-done:
				return
			}
		}
	}()

	// Start a goroutine to handle retransmissions
	go func() {
		for {
			select {
			case <-retransmitTicker.C:
				c.checkForRetransmissions()
			case <-done:
				return
			}
		}
	}()

	// Start the sliding window transfer
	err = c.startSlidingWindowTransfer()
	if err != nil {
		close(done)
		return err
	}

	// Wait for all chunks to be acknowledged
	for c.webrtc.sendTransfer.inProgress {
		if c.webrtc.sendTransfer.lastAckedSequence == totalChunks-1 {
			// All chunks acknowledged, we're done
			break
		}
		
		// Check if connection is still valid - use the local variables to avoid nil pointer issues
		// Also check the channel states in a safe way
		channelsValid := true
		var controlState, dataState webrtc.DataChannelState
		
		// Use a function to safely check channel state
		checkChannelState := func(channel *webrtc.DataChannel) (webrtc.DataChannelState, bool) {
			defer func() {
				if r := recover(); r != nil {
					c.ui.LogDebug(fmt.Sprintf("Recovered from panic while checking channel state: %v", r))
				}
			}()
			
			if channel == nil {
				return webrtc.DataChannelStateClosed, false
			}
			
			return channel.ReadyState(), true
		}
		
		// Check control channel
		var controlValid bool
		controlState, controlValid = checkChannelState(controlChannel)
		if !controlValid || controlState != webrtc.DataChannelStateOpen {
			channelsValid = false
		}
		
		// Check data channel
		var dataValid bool
		dataState, dataValid = checkChannelState(dataChannel)
		if !dataValid || dataState != webrtc.DataChannelStateOpen {
			channelsValid = false
		}
		
		if !channelsValid {
			c.ui.LogDebug(fmt.Sprintf("Connection issue during file transfer - control: %s, data: %s",
				controlState.String(), dataState.String()))
			close(done)
			
			// Show completion message with warning
			c.ui.UpdateTransferProgress(fmt.Sprintf("⬆ %s - Incomplete (connection issue)",
				info.Name()),
				"send")
				
			// Reset transfer state
			c.webrtc.sendTransfer = transferState{}
			return fmt.Errorf("connection issue during file transfer")
		}
		
		time.Sleep(100 * time.Millisecond)
	}

	// Calculate final statistics
	avgSpeed := float64(info.Size()) / time.Since(c.webrtc.sendTransfer.startTime).Seconds()

	// Show completion message
	c.ui.UpdateTransferProgress(fmt.Sprintf("⬆ %s - Finishing transfer...", info.Name()), "send")

	// Try to send complete message if the control channel is still open
	// Use a defer to recover from any panics that might occur
	defer func() {
		if r := recover(); r != nil {
			c.ui.LogDebug(fmt.Sprintf("Recovered from panic in SendFile: %v", r))
		}
	}()
	
	// Check if control channel is still valid and open
	if controlChannel != nil {
		// Check the channel state in a safe way
		channelState := controlChannel.ReadyState()
		if channelState == webrtc.DataChannelStateOpen {
			complete := struct {
				Type string `json:"type"`
			}{
				Type: "file-complete",
			}

			completeJSON, err := json.Marshal(complete)
			if err == nil {
				// Try to send the message, but don't crash if it fails
				err = func() (sendErr error) {
					// Recover from any panics during send
					defer func() {
						if r := recover(); r != nil {
							c.ui.LogDebug(fmt.Sprintf("Recovered from panic while sending complete message: %v", r))
							sendErr = fmt.Errorf("panic during send: %v", r)
						}
					}()
					
					return controlChannel.SendText(string(completeJSON))
				}()
				
				if err != nil {
					c.ui.LogDebug(fmt.Sprintf("Error sending complete message: %v", err))
					// Continue anyway, as the file transfer is complete
				} else {
					c.ui.LogDebug("Sent file-complete message successfully")
					// Wait a moment for the message to be sent
					time.Sleep(100 * time.Millisecond)
				}
			}
		} else {
			c.ui.LogDebug(fmt.Sprintf("Cannot send file-complete message: control channel not in open state (state: %s)",
				channelState.String()))
		}
	} else {
		c.ui.LogDebug("Cannot send file-complete message: control channel is nil")
	}

	// Show final completion message
	c.ui.UpdateTransferProgress(fmt.Sprintf("⬆ %s - Complete (avg: %.1f MB/s)",
		info.Name(),
		avgSpeed/1024/1024),
		"send")

	// Signal goroutines to exit
	close(done)

	// Reset transfer state
	c.webrtc.sendTransfer = transferState{}

	return nil
}

// Handle binary chunk data
func (c *Client) handleBinaryChunkData(sequence int, total int, size int, data []byte) {
	if !c.webrtc.receiveTransfer.inProgress {
		c.ui.ShowError("Received chunk but no download in progress")
		return
	}

	// Store the chunk in memory
	if sequence >= len(c.webrtc.receiveTransfer.chunks) {
		c.ui.ShowError(fmt.Sprintf("Received chunk with sequence %d beyond expected total %d", 
			sequence, len(c.webrtc.receiveTransfer.chunks)))
		return
	}

	// Store the chunk data
	c.webrtc.receiveTransfer.chunks[sequence] = data
	c.webrtc.receiveTransfer.receivedChunks[sequence] = true

	// Remove from missing chunks if it was there
	delete(c.webrtc.receiveTransfer.missingChunks, sequence)

	// Send confirmation
	confirm := ChunkConfirm{
		Type:     "chunk-confirm",
		Sequence: sequence,
	}

	confirmJSON, err := json.Marshal(confirm)
	if err != nil {
		c.ui.ShowError(fmt.Sprintf("Failed to marshal chunk confirmation: %v", err))
		return
	}

	// Check if control channel is initialized and open
	if c.webrtc.controlChannel == nil {
		c.ui.ShowError("Control channel not initialized, connection may not be fully established")
		return
	}
	
	// Check control channel state
	if c.webrtc.controlChannel.ReadyState() != webrtc.DataChannelStateOpen {
		c.ui.ShowError(fmt.Sprintf("Control channel is not in open state (current state: %s)", c.webrtc.controlChannel.ReadyState().String()))
		return
	}

	err = c.webrtc.controlChannel.SendText(string(confirmJSON))
	if err != nil {
		c.ui.LogDebug(fmt.Sprintf("Error sending chunk confirmation: %v", err))
		c.ui.ShowError(fmt.Sprintf("Failed to send chunk confirmation: %v", err))
		return
	}

	// Update in-order sequence tracking
	if sequence == c.webrtc.receiveTransfer.lastReceivedSequence+1 {
		c.webrtc.receiveTransfer.lastReceivedSequence = sequence

		// Check for any consecutive chunks we've already received
		nextSeq := sequence + 1
		for {
			if _, ok := c.webrtc.receiveTransfer.receivedChunks[nextSeq]; ok {
				c.webrtc.receiveTransfer.lastReceivedSequence = nextSeq
				nextSeq++
			} else {
				break
			}
		}
	}

	// Update progress
	c.webrtc.receiveTransfer.receivedSize += int64(size)
	now := time.Now()
	if time.Since(c.webrtc.receiveTransfer.lastUpdate) > 100*time.Millisecond {
		timeDiff := now.Sub(c.webrtc.receiveTransfer.lastUpdate).Seconds()
		if timeDiff > 0 {
			speed := float64(c.webrtc.receiveTransfer.receivedSize-c.webrtc.receiveTransfer.lastUpdateSize) / timeDiff
			percentage := int((float64(c.webrtc.receiveTransfer.receivedSize) / float64(c.webrtc.receiveTransfer.fileTransfer.Size)) * 100)
			c.ui.UpdateTransferProgress(fmt.Sprintf("⬇ %s [%d%%] (%.1f MB/s)",
				c.webrtc.receiveTransfer.fileTransfer.Name,
				percentage,
				speed/1024/1024),
				"receive")
			c.webrtc.receiveTransfer.lastUpdate = now
			c.webrtc.receiveTransfer.lastUpdateSize = c.webrtc.receiveTransfer.receivedSize
		}
	}

	// Check if we've received all chunks
	if c.webrtc.receiveTransfer.lastReceivedSequence == total-1 {
		// Check if channels are still valid before completing
		if c.webrtc.controlChannel == nil || c.webrtc.dataChannel == nil {
			c.ui.LogDebug("Connection lost during file transfer, but all chunks received - completing anyway")
		} else if c.webrtc.controlChannel.ReadyState() != webrtc.DataChannelStateOpen ||
		          c.webrtc.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
			c.ui.LogDebug(fmt.Sprintf("Channels not in open state, but all chunks received - completing anyway (control: %s, data: %s)",
				c.webrtc.controlChannel.ReadyState().String(), c.webrtc.dataChannel.ReadyState().String()))
		}
		
		c.handleFileComplete()
	}
}

// Check for missing chunks and request them
func (c *Client) checkForMissingChunks() {
	if !c.webrtc.receiveTransfer.inProgress {
		return
	}
	
	// Check if connection is still valid
	if c.webrtc.controlChannel == nil || c.webrtc.dataChannel == nil {
		c.ui.LogDebug("Cannot check for missing chunks: connection lost")
		return
	}
	
	// Check if channels are in open state
	if c.webrtc.controlChannel.ReadyState() != webrtc.DataChannelStateOpen ||
	   c.webrtc.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		c.ui.LogDebug(fmt.Sprintf("Cannot check for missing chunks: channels not in open state (control: %s, data: %s)",
			c.webrtc.controlChannel.ReadyState().String(), c.webrtc.dataChannel.ReadyState().String()))
		return
	}

	// Check for missing chunks
	for i := 0; i <= c.webrtc.receiveTransfer.lastReceivedSequence; i++ {
		if _, ok := c.webrtc.receiveTransfer.receivedChunks[i]; !ok {
			c.webrtc.receiveTransfer.missingChunks[i] = true
		}
	}

	// Request missing chunks
	for seq := range c.webrtc.receiveTransfer.missingChunks {
		request := struct {
			Type     string `json:"type"`
			Sequence int    `json:"sequence"`
		}{
			Type:     "chunk-request",
			Sequence: seq,
		}

		requestJSON, err := json.Marshal(request)
		if err != nil {
			c.ui.ShowError(fmt.Sprintf("Failed to marshal chunk request: %v", err))
			continue
		}

		// Check if control channel is initialized and open
		if c.webrtc.controlChannel == nil {
			c.ui.ShowError("Control channel not initialized, connection may not be fully established")
			continue
		}
		
		// Check control channel state
		if c.webrtc.controlChannel.ReadyState() != webrtc.DataChannelStateOpen {
			c.ui.ShowError(fmt.Sprintf("Control channel is not in open state (current state: %s)", c.webrtc.controlChannel.ReadyState().String()))
			continue
		}

		err = c.webrtc.controlChannel.SendText(string(requestJSON))
		if err != nil {
			c.ui.LogDebug(fmt.Sprintf("Error sending chunk request: %v", err))
			c.ui.ShowError(fmt.Sprintf("Failed to send chunk request: %v", err))
			continue
		}

		c.ui.LogDebug(fmt.Sprintf("Requested missing chunk %d", seq))
	}
}

func (c *Client) handleFileInfoUpdate(data map[string]interface{}) {
	if !c.webrtc.receiveTransfer.inProgress {
		return
	}

	infoMap, ok := data["info"].(map[string]interface{})
	if !ok {
		c.ui.ShowError("Invalid file info update format")
		return
	}

	// Update MD5 hash if provided
	if md5, ok := infoMap["md5"].(string); ok && md5 != "" {
		c.webrtc.receiveTransfer.fileTransfer.MD5 = md5
		c.ui.LogDebug(fmt.Sprintf("Updated file MD5 hash: %s", md5))
	}
}

func (c *Client) handleFileInfo(data map[string]interface{}) {
	if c.webrtc.receiveTransfer.inProgress {
		c.ui.ShowError("Cannot receive file: Download already in progress")
		return
	}

	infoMap, ok := data["info"].(map[string]interface{})
	if !ok {
		c.ui.ShowError("Invalid file info format")
		return
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
		c.ui.ShowError(fmt.Sprintf("Failed to create file: %v", err))
		return
	}

	// Calculate total chunks
	totalChunks := int(math.Ceil(float64(size) / float64(maxChunkSize)))

	// Initialize transfer state with sliding window parameters
	c.webrtc.receiveTransfer = transferState{
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

		for c.webrtc.receiveTransfer.inProgress {
			<-ticker.C
			c.checkForMissingChunks()
		}
	}()

	c.ui.LogDebug(fmt.Sprintf("Receiving file: %s (%d bytes, %d chunks)", name, int64(size), totalChunks))
}

func (c *Client) handleFileComplete() {
	if !c.webrtc.receiveTransfer.inProgress {
		return
	}
	
	// Check if file transfer is still valid
	if c.webrtc.receiveTransfer.fileTransfer == nil || c.webrtc.receiveTransfer.fileTransfer.file == nil {
		c.ui.LogDebug("Cannot complete file transfer: file handle is not valid")
		c.webrtc.receiveTransfer = transferState{}
		return
	}

	// Reopen the file for writing from the beginning
	file, err := os.Create(c.webrtc.receiveTransfer.fileTransfer.filePath)
	if err != nil {
		c.ui.ShowError(fmt.Sprintf("Failed to reopen file for writing: %v", err))
		c.webrtc.receiveTransfer = transferState{}
		return
	}
	defer file.Close()

	// Calculate the total expected file size
	expectedSize := c.webrtc.receiveTransfer.fileTransfer.Size

	// Write all chunks to the file in order
	var totalWritten int64
	for i := 0; i < c.webrtc.receiveTransfer.totalChunks; i++ {
		if i >= len(c.webrtc.receiveTransfer.chunks) || c.webrtc.receiveTransfer.chunks[i] == nil {
			c.ui.ShowError(fmt.Sprintf("Missing chunk %d when finalizing file", i))
			c.webrtc.receiveTransfer = transferState{}
			return
		}

		n, err := file.Write(c.webrtc.receiveTransfer.chunks[i])
		if err != nil {
			c.ui.ShowError(fmt.Sprintf("Failed to write chunk %d: %v", i, err))
			c.webrtc.receiveTransfer = transferState{}
			return
		}

		totalWritten += int64(n)
	}

	// Verify the file size
	if totalWritten != expectedSize {
		c.ui.ShowError(fmt.Sprintf("File size mismatch: expected %d bytes, got %d bytes", 
			expectedSize, totalWritten))
	}

	// Calculate MD5 hash of the received file
	receivedHash, err := calculateMD5(c.webrtc.receiveTransfer.fileTransfer.filePath)
	if err != nil {
		c.ui.ShowError(fmt.Sprintf("Failed to calculate received file hash: %v", err))
	} else if c.webrtc.receiveTransfer.fileTransfer.MD5 != "" && 
		receivedHash != c.webrtc.receiveTransfer.fileTransfer.MD5 {
		c.ui.ShowError(fmt.Sprintf("File hash mismatch: expected %s, got %s", 
			c.webrtc.receiveTransfer.fileTransfer.MD5, receivedHash))
	}

	// Show completion message
	avgSpeed := float64(c.webrtc.receiveTransfer.fileTransfer.Size) / time.Since(c.webrtc.receiveTransfer.startTime).Seconds()
	c.ui.UpdateTransferProgress(fmt.Sprintf("⬇ %s - Complete (avg: %.1f MB/s)",
		c.webrtc.receiveTransfer.fileTransfer.Name,
		avgSpeed/1024/1024),
		"receive")

	// Reset transfer state
	c.webrtc.receiveTransfer = transferState{}
}

func calculateMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	hash := md5.New()
	buf := make([]byte, 32768)

	for {
		n, err := file.Read(buf)
		if n > 0 {
			hash.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Handle chunk confirmation and update sliding window
func (c *Client) handleChunkConfirmation(sequence int) {
	// Update the last acknowledged sequence if this is the next expected one
	if sequence == c.webrtc.sendTransfer.lastAckedSequence + 1 {
		c.webrtc.sendTransfer.lastAckedSequence = sequence

		// Check for any consecutive acknowledged chunks
		nextSeq := sequence + 1
		for {
			if _, ok := c.webrtc.sendTransfer.unacknowledgedChunks[nextSeq]; ok {
				if c.webrtc.sendTransfer.unacknowledgedChunks[nextSeq] {
					c.webrtc.sendTransfer.lastAckedSequence = nextSeq
					delete(c.webrtc.sendTransfer.unacknowledgedChunks, nextSeq)
					delete(c.webrtc.sendTransfer.chunkTimestamps, nextSeq)
					nextSeq++
				} else {
					break
				}
			} else {
				break
			}
		}
	} else if sequence > c.webrtc.sendTransfer.lastAckedSequence {
		// Mark this chunk as acknowledged but don't update lastAckedSequence yet
		c.webrtc.sendTransfer.unacknowledgedChunks[sequence] = true
	}

	// Remove from unacknowledged chunks and timestamps
	if _, ok := c.webrtc.sendTransfer.unacknowledgedChunks[sequence]; ok {
		delete(c.webrtc.sendTransfer.unacknowledgedChunks, sequence)
		delete(c.webrtc.sendTransfer.chunkTimestamps, sequence)
	}

	// Increase congestion window on successful ACK (TCP-like slow start/congestion avoidance)
	if c.webrtc.sendTransfer.congestionWindow < c.webrtc.sendTransfer.windowSize {
		if c.webrtc.sendTransfer.congestionWindow < 32 {
			// Slow start - exponential growth
			c.webrtc.sendTransfer.congestionWindow = int(math.Min(float64(c.webrtc.sendTransfer.windowSize), float64(c.webrtc.sendTransfer.congestionWindow + 1)))
		} else {
			// Congestion avoidance - additive increase
			c.webrtc.sendTransfer.congestionWindow = int(math.Min(
				float64(c.webrtc.sendTransfer.windowSize),
				float64(c.webrtc.sendTransfer.congestionWindow) + (1.0 / float64(c.webrtc.sendTransfer.congestionWindow)),
			))
		}
	}

	// Reset consecutive timeouts counter on successful ACK
	c.webrtc.sendTransfer.consecutiveTimeouts = 0

	// Continue sending if we have more chunks to send and connection is still valid
	// Use a function to safely check channel state
	checkChannelState := func(channel *webrtc.DataChannel) (webrtc.DataChannelState, bool) {
		defer func() {
			if r := recover(); r != nil {
				c.ui.LogDebug(fmt.Sprintf("Recovered from panic while checking channel state: %v", r))
			}
		}()
		
		if channel == nil {
			return webrtc.DataChannelStateClosed, false
		}
		
		return channel.ReadyState(), true
	}
	
	// Check control channel
	controlValid := false
	var controlState webrtc.DataChannelState
	if c.webrtc.controlChannel != nil {
		controlState, controlValid = checkChannelState(c.webrtc.controlChannel)
	}
	
	// Check data channel
	dataValid := false
	var dataState webrtc.DataChannelState
	if c.webrtc.dataChannel != nil {
		dataState, dataValid = checkChannelState(c.webrtc.dataChannel)
	}
	
	if controlValid && dataValid &&
	   controlState == webrtc.DataChannelStateOpen &&
	   dataState == webrtc.DataChannelStateOpen {
		// Use a defer to recover from any panics
		func() {
			defer func() {
				if r := recover(); r != nil {
					c.ui.LogDebug(fmt.Sprintf("Recovered from panic in trySendNextChunks: %v", r))
				}
			}()
			c.trySendNextChunks()
		}()
	} else {
		c.ui.LogDebug(fmt.Sprintf("Cannot send more chunks: channels not in open state (control: %s, data: %s)",
			controlState.String(), dataState.String()))
	}

	// Update progress using fixedChunkSize for consistency
	totalSent := int64(c.webrtc.sendTransfer.lastAckedSequence + 1) * int64(fixedChunkSize)
	if totalSent > c.webrtc.sendTransfer.fileTransfer.Size {
		totalSent = c.webrtc.sendTransfer.fileTransfer.Size
	}

	now := time.Now()
	if time.Since(c.webrtc.sendTransfer.lastUpdate) > 100*time.Millisecond {
		timeDiff := now.Sub(c.webrtc.sendTransfer.lastUpdate).Seconds()
		if timeDiff > 0 {
			speed := float64(totalSent-c.webrtc.sendTransfer.lastUpdateSize) / timeDiff
			percentage := int((float64(totalSent) / float64(c.webrtc.sendTransfer.fileTransfer.Size)) * 100)
			c.ui.UpdateTransferProgress(fmt.Sprintf("⬆ %s [%d%%] (%.1f MB/s)",
				c.webrtc.sendTransfer.fileTransfer.Name,
				percentage,
				speed/1024/1024),
				"send")
			c.webrtc.sendTransfer.lastUpdate = now
			c.webrtc.sendTransfer.lastUpdateSize = totalSent
		}
	}
}

// Check for chunks that need retransmission
func (c *Client) checkForRetransmissions() {
	if !c.webrtc.sendTransfer.inProgress {
		return
	}

	// Check for unacknowledged chunks that might need retransmission
	now := time.Now()
	var timeoutsDetected int

	// Add old unacknowledged chunks to retransmission queue
	for sequence, timestamp := range c.webrtc.sendTransfer.chunkTimestamps {
		if sequence <= c.webrtc.sendTransfer.lastAckedSequence {
			// This was already ACKed, remove it
			delete(c.webrtc.sendTransfer.unacknowledgedChunks, sequence)
			delete(c.webrtc.sendTransfer.chunkTimestamps, sequence)
		} else {
			// Check if chunk has timed out (3 seconds)
			if now.Sub(timestamp) > 3*time.Second {
				// Add to retransmission queue if not already there
				alreadyQueued := false
				for _, seq := range c.webrtc.sendTransfer.retransmissionQueue {
					if seq == sequence {
						alreadyQueued = true
						break
					}
				}

				if !alreadyQueued {
					c.webrtc.sendTransfer.retransmissionQueue = append(c.webrtc.sendTransfer.retransmissionQueue, sequence)
					timeoutsDetected++
					c.ui.LogDebug(fmt.Sprintf("Queuing chunk %d for retransmission (timeout)", sequence))
				}
			}
		}
	}

	// Implement congestion control
	if timeoutsDetected > 0 {
		c.webrtc.sendTransfer.consecutiveTimeouts++

		// Reduce window size on timeouts (TCP-like congestion avoidance)
		if c.webrtc.sendTransfer.consecutiveTimeouts > 1 {
			// Multiplicative decrease
			c.webrtc.sendTransfer.congestionWindow = int(math.Max(8, math.Floor(float64(c.webrtc.sendTransfer.congestionWindow) * 0.7)))
			c.ui.LogDebug(fmt.Sprintf("Reducing congestion window to %d due to timeouts", c.webrtc.sendTransfer.congestionWindow))
		}
	} else {
		c.webrtc.sendTransfer.consecutiveTimeouts = 0

		// Additive increase if no timeouts
		if c.webrtc.sendTransfer.congestionWindow < c.webrtc.sendTransfer.windowSize {
			c.webrtc.sendTransfer.congestionWindow = int(math.Min(
				float64(c.webrtc.sendTransfer.windowSize),
				float64(c.webrtc.sendTransfer.congestionWindow + 1),
			))
		}
	}

	// Sort retransmission queue by sequence number
	sort.Ints(c.webrtc.sendTransfer.retransmissionQueue)

	// Try to send next chunks including retransmissions if connection is still valid
	// Use a function to safely check channel state
	checkChannelState := func(channel *webrtc.DataChannel) (webrtc.DataChannelState, bool) {
		defer func() {
			if r := recover(); r != nil {
				c.ui.LogDebug(fmt.Sprintf("Recovered from panic while checking channel state: %v", r))
			}
		}()
		
		if channel == nil {
			return webrtc.DataChannelStateClosed, false
		}
		
		return channel.ReadyState(), true
	}
	
	// Check control channel
	controlValid := false
	var controlState webrtc.DataChannelState
	if c.webrtc.controlChannel != nil {
		controlState, controlValid = checkChannelState(c.webrtc.controlChannel)
	}
	
	// Check data channel
	dataValid := false
	var dataState webrtc.DataChannelState
	if c.webrtc.dataChannel != nil {
		dataState, dataValid = checkChannelState(c.webrtc.dataChannel)
	}
	
	if controlValid && dataValid &&
	   controlState == webrtc.DataChannelStateOpen &&
	   dataState == webrtc.DataChannelStateOpen {
		// Use a defer to recover from any panics
		func() {
			defer func() {
				if r := recover(); r != nil {
					c.ui.LogDebug(fmt.Sprintf("Recovered from panic in trySendNextChunks: %v", r))
				}
			}()
			c.trySendNextChunks()
		}()
	} else {
		c.ui.LogDebug(fmt.Sprintf("Cannot send retransmissions: channels not in open state (control: %s, data: %s)",
			controlState.String(), dataState.String()))
	}
}

// Start the sliding window transfer
func (c *Client) startSlidingWindowTransfer() error {
	// Try to send initial chunks within the window
	return c.trySendNextChunks()
}

// Try to send next chunks within the window
func (c *Client) trySendNextChunks() error {
	// Use a defer to recover from any panics
	defer func() {
		if r := recover(); r != nil {
			c.ui.LogDebug(fmt.Sprintf("Recovered from panic in trySendNextChunks: %v", r))
		}
	}()
	
	if !c.webrtc.connected || !c.webrtc.sendTransfer.inProgress {
		return nil
	}
	
	// Use a function to safely check channel state
	checkChannelState := func(channel *webrtc.DataChannel) (webrtc.DataChannelState, bool) {
		defer func() {
			if r := recover(); r != nil {
				c.ui.LogDebug(fmt.Sprintf("Recovered from panic while checking channel state: %v", r))
			}
		}()
		
		if channel == nil {
			return webrtc.DataChannelStateClosed, false
		}
		
		return channel.ReadyState(), true
	}
	
	// Check control channel
	controlValid := false
	var controlState webrtc.DataChannelState
	if c.webrtc.controlChannel != nil {
		controlState, controlValid = checkChannelState(c.webrtc.controlChannel)
	}
	
	// Check data channel
	dataValid := false
	var dataState webrtc.DataChannelState
	if c.webrtc.dataChannel != nil {
		dataState, dataValid = checkChannelState(c.webrtc.dataChannel)
	}
	
	if !controlValid || !dataValid {
		c.ui.LogDebug("Cannot send chunks: channels not initialized")
		return fmt.Errorf("channels not initialized")
	}
	
	if controlState != webrtc.DataChannelStateOpen || dataState != webrtc.DataChannelStateOpen {
		c.ui.LogDebug(fmt.Sprintf("Cannot send chunks: channels not in open state (control: %s, data: %s)",
			controlState.String(), dataState.String()))
		return fmt.Errorf("channels not in open state")
	}

	// Calculate effective window size (min of congestion window and configured window size)
	effectiveWindowSize := int(math.Min(float64(c.webrtc.sendTransfer.congestionWindow), float64(c.webrtc.sendTransfer.windowSize)))

	// First handle any retransmissions (prioritize them)
	for len(c.webrtc.sendTransfer.retransmissionQueue) > 0 {
		sequence := c.webrtc.sendTransfer.retransmissionQueue[0]
		c.webrtc.sendTransfer.retransmissionQueue = c.webrtc.sendTransfer.retransmissionQueue[1:]

		// Skip if this chunk has already been acknowledged
		if sequence <= c.webrtc.sendTransfer.lastAckedSequence {
			continue
		}

		// Send the chunk
		err := c.sendChunkBySequence(sequence)
		if err != nil {
			return err
		}
	}

	// Then send new chunks within the window
	for c.webrtc.sendTransfer.nextSequenceToSend < c.webrtc.sendTransfer.totalChunks &&
		c.webrtc.sendTransfer.nextSequenceToSend <= c.webrtc.sendTransfer.lastAckedSequence + effectiveWindowSize {

		// Send the chunk
		sequence := c.webrtc.sendTransfer.nextSequenceToSend
		err := c.sendChunkBySequence(sequence)
		if err != nil {
			return err
		}

		c.webrtc.sendTransfer.nextSequenceToSend++
	}

	return nil
}

// Send a specific chunk by sequence number
func (c *Client) sendChunkBySequence(sequence int) error {
	if sequence >= c.webrtc.sendTransfer.totalChunks {
		return nil
	}

	// Calculate offset and size for this chunk using fixedChunkSize for consistency
	offset := int64(sequence) * int64(fixedChunkSize)
	end := int64(math.Min(float64(offset + int64(fixedChunkSize)), float64(c.webrtc.sendTransfer.fileTransfer.Size)))
	size := int(end - offset)

	// Seek to the correct position in the file
	_, err := c.webrtc.sendTransfer.fileTransfer.file.Seek(offset, 0)
	if err != nil {
		return fmt.Errorf("failed to seek in file: %v", err)
	}

	// Read the chunk
	buf := make([]byte, size)
	n, err := c.webrtc.sendTransfer.fileTransfer.file.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read file: %v", err)
	}

	if n != size {
		return fmt.Errorf("failed to read complete chunk: expected %d bytes, got %d", size, n)
	}

	// Use fixedChunkSize for consistency
	// We don't need to limit n here since we've already read the correct amount from the file
	// and the framing overhead is accounted for in fixedChunkSize

	// Create chunk info for the control channel
	chunkInfo := struct {
		Type        string `json:"type"`
		Sequence    int    `json:"sequence"`
		TotalChunks int    `json:"totalChunks"`
		Size        int    `json:"size"`
	}{
		Type:        "chunk-info",
		Sequence:    sequence,
		TotalChunks: c.webrtc.sendTransfer.totalChunks,
		Size:        n,
	}

	// Marshal the chunk info
	chunkInfoJSON, err := json.Marshal(chunkInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal chunk info: %v", err)
	}

	// Use a function to safely check channel state
	checkChannelState := func(channel *webrtc.DataChannel) (webrtc.DataChannelState, bool) {
		defer func() {
			if r := recover(); r != nil {
				c.ui.LogDebug(fmt.Sprintf("Recovered from panic while checking channel state: %v", r))
			}
		}()
		
		if channel == nil {
			return webrtc.DataChannelStateClosed, false
		}
		
		return channel.ReadyState(), true
	}
	
	// Check control channel
	controlValid := false
	var controlState webrtc.DataChannelState
	if c.webrtc.controlChannel != nil {
		controlState, controlValid = checkChannelState(c.webrtc.controlChannel)
	}
	
	if !controlValid {
		return fmt.Errorf("control channel not initialized, connection may not be fully established")
	}
	
	if controlState != webrtc.DataChannelStateOpen {
		return fmt.Errorf("control channel is not in open state (current state: %s)", controlState.String())
	}

	// Send the chunk info on the control channel - use a function to safely send
	sendErr := func() (sendErr error) {
		defer func() {
			if r := recover(); r != nil {
				c.ui.LogDebug(fmt.Sprintf("Recovered from panic while sending chunk info: %v", r))
				sendErr = fmt.Errorf("panic during send: %v", r)
			}
		}()
		
		return c.webrtc.controlChannel.SendText(string(chunkInfoJSON))
	}()
	
	if sendErr != nil {
		c.ui.LogDebug(fmt.Sprintf("Error sending chunk info: %v", sendErr))
		return fmt.Errorf("failed to send chunk info: %v", sendErr)
	}

	// Wait for a short time to ensure the control message is processed
	time.Sleep(5 * time.Millisecond)

	// Create a framed buffer with metadata
	// Format: [4 bytes sequence][4 bytes length][data bytes]
	framedData := make([]byte, 8+n)

	// Write sequence number (big endian)
	framedData[0] = byte(sequence >> 24)
	framedData[1] = byte(sequence >> 16)
	framedData[2] = byte(sequence >> 8)
	framedData[3] = byte(sequence)

	// Write data length (big endian)
	framedData[4] = byte(n >> 24)
	framedData[5] = byte(n >> 16)
	framedData[6] = byte(n >> 8)
	framedData[7] = byte(n)

	// Copy the actual data (make sure we only copy exactly n bytes)
	if n > 0 {
		copy(framedData[8:8+n], buf[:n])
	}

	// Log the exact size of the framed data
	c.ui.LogDebug(fmt.Sprintf("Created framed data for chunk %d: %d bytes header + %d bytes data = %d bytes total",
		sequence, 8, n, len(framedData)))

	// Check if the framed data is too large
	if len(framedData) > maxWebRTCMessageSize {
		return fmt.Errorf("framed data too large: %d bytes (limit: %d)", len(framedData), maxWebRTCMessageSize)
	}

	// Check data channel
	dataValid := false
	var dataState webrtc.DataChannelState
	if c.webrtc.dataChannel != nil {
		dataState, dataValid = checkChannelState(c.webrtc.dataChannel)
	}
	
	if !dataValid {
		return fmt.Errorf("data channel not initialized, connection may not be fully established")
	}
	
	if dataState != webrtc.DataChannelStateOpen {
		return fmt.Errorf("data channel is not in open state (current state: %s)", dataState.String())
	}

	// Send the framed binary data - use a function to safely send
	sendErr := func() (sendErr error) {
		defer func() {
			if r := recover(); r != nil {
				c.ui.LogDebug(fmt.Sprintf("Recovered from panic while sending chunk data: %v", r))
				sendErr = fmt.Errorf("panic during send: %v", r)
			}
		}()
		
		return c.webrtc.dataChannel.Send(framedData)
	}()
	
	if sendErr != nil {
		c.ui.LogDebug(fmt.Sprintf("Error sending chunk: %v", sendErr))
		return fmt.Errorf("failed to send chunk: %v", sendErr)
	}

	// Log success for debugging
	c.ui.LogDebug(fmt.Sprintf("Sent framed binary chunk %d (%d bytes data, %d bytes total)",
		sequence, n, len(framedData)))

	// Mark as unacknowledged and record timestamp
	// Store a reference to the chunk data for potential retransmission
	c.webrtc.sendTransfer.unacknowledgedChunks[sequence] = false
	c.webrtc.sendTransfer.chunkTimestamps[sequence] = time.Now()

	// Add a small delay to ensure the data channel has time to process the send
	time.Sleep(1 * time.Millisecond)

	return nil
}

func (c *Client) handleChunkConfirm(sequence int) {
	if c.webrtc.sendTransfer.confirmHandler != nil {
		c.webrtc.sendTransfer.confirmHandler(sequence)
	}
}