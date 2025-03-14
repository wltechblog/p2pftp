package main

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

func NewClient(conn *websocket.Conn) *Client {
    return &Client{
        conn:   conn,
        webrtc: &WebRTCState{},
    }
}

// ChunkConfirm message for acknowledging received chunks
type ChunkConfirm struct {
    Type     string `json:"type"`
    Sequence int    `json:"sequence"`
}

// Complete the connection setup when both channels are open
func (c *Client) completeConnectionSetup() {
    c.webrtc.connected = true
    c.ui.LogDebug("Both channels ready for transfer")
    c.ui.ShowConnectionAccepted("")

    // Send capabilities message with our maximum supported chunk size
    capabilities := struct {
        Type         string `json:"type"`
        MaxChunkSize int    `json:"maxChunkSize"`
    }{
        Type:         "capabilities",
        MaxChunkSize: maxSupportedChunkSize,
    }

    capabilitiesJSON, err := json.Marshal(capabilities)
    if err == nil {
        c.webrtc.controlChannel.SendText(string(capabilitiesJSON))
        c.ui.LogDebug(fmt.Sprintf("Sent capabilities with max chunk size: %d", maxSupportedChunkSize))
    }
}

func (c *Client) handleChunkConfirm(sequence int) {
    if c.webrtc.sendTransfer.confirmHandler != nil {
        c.webrtc.sendTransfer.confirmHandler(sequence)
    }
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

    // Continue sending if we have more chunks to send
    c.trySendNextChunks()

    // Update progress
    totalSent := int64(c.webrtc.sendTransfer.lastAckedSequence + 1) * int64(maxChunkSize)
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

    // Try to send next chunks including retransmissions
    c.trySendNextChunks()
}

// Start the sliding window transfer
func (c *Client) startSlidingWindowTransfer() error {
    // Try to send initial chunks within the window
    return c.trySendNextChunks()
}

// Try to send next chunks within the window
func (c *Client) trySendNextChunks() error {
    if !c.webrtc.connected || !c.webrtc.sendTransfer.inProgress {
        return nil
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

    // Calculate offset and size for this chunk
    offset := int64(sequence) * int64(maxChunkSize)
    end := int64(math.Min(float64(offset + int64(maxChunkSize)), float64(c.webrtc.sendTransfer.fileTransfer.Size)))
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

    // Use a conservative chunk size for binary data
    dataSize := int(math.Min(float64(maxChunkSize), float64(32768))) // 32KB is safe for most implementations
    if n > dataSize {
        n = dataSize
    }

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

    // Send the chunk info on the control channel
    err = c.webrtc.controlChannel.SendText(string(chunkInfoJSON))
    if err != nil {
        return fmt.Errorf("failed to send chunk info: %v", err)
    }

    // Send the binary data on the data channel
    err = c.webrtc.dataChannel.Send(buf[:n])
    if err != nil {
        return fmt.Errorf("failed to send chunk: %v", err)
    }

    // Mark as unacknowledged and record timestamp
    c.webrtc.sendTransfer.unacknowledgedChunks[sequence] = false
    c.webrtc.sendTransfer.chunkTimestamps[sequence] = time.Now()

    return nil
}

func (c *Client) SendMessage(msg Message) error {
    err := c.conn.WriteJSON(msg)
    if err != nil {
        c.ui.ShowError("Send failed: " + err.Error())
        return err
    }
    return nil
}

func (c *Client) SendFile(path string) error {
    if !c.webrtc.connected {
        return fmt.Errorf("not connected to peer")
    }

    if c.webrtc.sendTransfer.inProgress {
        return fmt.Errorf("upload already in progress")
    }

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

    // Calculate total chunks
    totalChunks := int(math.Ceil(float64(info.Size()) / float64(maxChunkSize)))

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

    err = c.webrtc.controlChannel.SendText(string(infoJSON))
    if err != nil {
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
        time.Sleep(100 * time.Millisecond)
    }

    // Calculate final statistics
    avgSpeed := float64(info.Size()) / time.Since(c.webrtc.sendTransfer.startTime).Seconds()

    // Show completion message
    c.ui.UpdateTransferProgress(fmt.Sprintf("⬆ %s - Finishing transfer...", info.Name()), "send")

    // Send complete message
    complete := struct {
        Type string `json:"type"`
    }{
        Type: "file-complete",
    }

    completeJSON, err := json.Marshal(complete)
    if err != nil {
        close(done)
        return fmt.Errorf("failed to marshal complete message: %v", err)
    }

    err = c.webrtc.controlChannel.SendText(string(completeJSON))
    if err != nil {
        close(done)
        return fmt.Errorf("failed to send complete message: %v", err)
    }

    // Wait a moment for the message to be sent
    time.Sleep(100 * time.Millisecond)

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

    // Process the binary data directly
    c.processChunkData(sequence, total, size, data)
}

// Handle legacy text-based chunk data (for backward compatibility)
func (c *Client) handleChunkData(sequence int, total int, size int, data string) {
    if !c.webrtc.receiveTransfer.inProgress {
        c.ui.ShowError("Received chunk but no download in progress")
        return
    }

    binaryData, err := base64.StdEncoding.DecodeString(data)
    if err != nil {
        c.ui.ShowError(fmt.Sprintf("Failed to decode chunk data: %v", err))
        return
    }

    // Process the decoded data
    c.processChunkData(sequence, total, size, binaryData)
}

// Common function to process chunk data
func (c *Client) processChunkData(sequence int, total int, size int, data []byte) {
    if len(data) != size {
        c.ui.ShowError(fmt.Sprintf("Chunk size mismatch. Expected: %d, Got: %d",
            size, len(data)))
        return
    }

    // Store chunk and update size
    c.webrtc.receiveTransfer.chunks[sequence] = binaryData

    // Mark this chunk as received
    if c.webrtc.receiveTransfer.receivedChunks == nil {
        c.webrtc.receiveTransfer.receivedChunks = make(map[int]bool)
    }
    c.webrtc.receiveTransfer.receivedChunks[sequence] = true

    // If this was a missing chunk, remove it from missing chunks
    if c.webrtc.receiveTransfer.missingChunks != nil && c.webrtc.receiveTransfer.missingChunks[sequence] {
        delete(c.webrtc.receiveTransfer.missingChunks, sequence)
    }

    // Update the last received sequence
    c.updateLastReceivedSequence()

    // Recalculate total received size from scratch to ensure accuracy
    var totalReceived int64
    for _, chunk := range c.webrtc.receiveTransfer.chunks {
        if chunk != nil {
            totalReceived += int64(len(chunk))
        }
    }
    c.webrtc.receiveTransfer.receivedSize = totalReceived

    // Send confirmation back
    confirm := ChunkConfirm{
        Type:     "chunk-confirm",
        Sequence: sequence,
    }
    confirmJSON, err := json.Marshal(confirm)
    if err == nil {
        c.webrtc.dataChannel.SendText(string(confirmJSON))
    }

    // Update progress every 100ms or on significant changes
    now := time.Now()
    received := c.webrtc.receiveTransfer.receivedSize
    totalSize := c.webrtc.receiveTransfer.fileTransfer.Size
    percentage := int((float64(received) / float64(totalSize)) * 100)
    lastPercentage := int((float64(c.webrtc.receiveTransfer.lastUpdateSize) / float64(totalSize)) * 100)

    if time.Since(c.webrtc.receiveTransfer.lastUpdate) > 100*time.Millisecond ||
       percentage != lastPercentage {
        // Calculate speed based on data received since last update
        timeDiff := now.Sub(c.webrtc.receiveTransfer.lastUpdate).Seconds()
        if timeDiff > 0 {
            speed := float64(c.webrtc.receiveTransfer.receivedSize-c.webrtc.receiveTransfer.lastUpdateSize) / timeDiff
            c.ui.UpdateTransferProgress(fmt.Sprintf("⬇ %s [%d%%] (%.1f MB/s)",
                c.webrtc.receiveTransfer.fileTransfer.Name,
                percentage,
                speed/1024/1024),
                "receive")
        }
        c.webrtc.receiveTransfer.lastUpdate = now
        c.webrtc.receiveTransfer.lastUpdateSize = c.webrtc.receiveTransfer.receivedSize
    }

    // Check if file is complete
    c.checkIfComplete()
}

// Update the last received sequence number
func (c *Client) updateLastReceivedSequence() {
    nextExpected := c.webrtc.receiveTransfer.lastReceivedSequence + 1

    // Check if we have consecutive chunks
    for {
        if c.webrtc.receiveTransfer.receivedChunks[nextExpected] {
            c.webrtc.receiveTransfer.lastReceivedSequence = nextExpected
            nextExpected++
        } else {
            break
        }
    }
}

// Check if we have all chunks
func (c *Client) checkIfComplete() {
    if !c.webrtc.receiveTransfer.inProgress {
        return
    }

    // Count received chunks
    receivedCount := len(c.webrtc.receiveTransfer.receivedChunks)

    // If we have all chunks, complete the transfer
    if receivedCount == c.webrtc.receiveTransfer.totalChunks {
        c.ui.LogDebug("All chunks received, completing transfer")
        c.handleFileComplete()
    } else if c.webrtc.receiveTransfer.receivedSize >= c.webrtc.receiveTransfer.fileTransfer.Size {
        // Alternative check based on size
        c.handleFileComplete()
    }
}

// Check for missing chunks and request them
func (c *Client) checkForMissingChunks() {
    if !c.webrtc.receiveTransfer.inProgress {
        return
    }

    // Initialize missing chunks map if needed
    if c.webrtc.receiveTransfer.missingChunks == nil {
        c.webrtc.receiveTransfer.missingChunks = make(map[int]bool)
    }

    // Look for missing chunks in the already processed range
    lastReceivedSequence := c.webrtc.receiveTransfer.lastReceivedSequence

    // Calculate the window of chunks we expect to have received
    lookAheadWindow := 50 // Look ahead up to 50 chunks
    // We'll use this for future optimizations
    _ = int(math.Min(float64(c.webrtc.receiveTransfer.totalChunks-1), float64(lastReceivedSequence+lookAheadWindow)))

    // First check for holes in the sequence
    for i := 0; i <= lastReceivedSequence; i++ {
        if !c.webrtc.receiveTransfer.receivedChunks[i] {
            c.webrtc.receiveTransfer.missingChunks[i] = true
            c.ui.LogDebug(fmt.Sprintf("Detected missing chunk in sequence: %d", i))
        }
    }

    // Find the highest received chunk
    highestReceivedChunk := -1
    for seq := range c.webrtc.receiveTransfer.receivedChunks {
        if seq > highestReceivedChunk {
            highestReceivedChunk = seq
        }
    }

    // Check for gaps between last in-order and highest received
    if highestReceivedChunk > lastReceivedSequence {
        for i := lastReceivedSequence + 1; i < highestReceivedChunk; i++ {
            if !c.webrtc.receiveTransfer.receivedChunks[i] {
                c.webrtc.receiveTransfer.missingChunks[i] = true
                c.ui.LogDebug(fmt.Sprintf("Detected gap in received chunks: %d", i))
            }
        }
    }

    // Request missing chunks if we have any
    if len(c.webrtc.receiveTransfer.missingChunks) > 0 {
        c.requestMissingChunks()
    }
}

// Request missing chunks
func (c *Client) requestMissingChunks() {
    if len(c.webrtc.receiveTransfer.missingChunks) == 0 {
        return
    }

    // Convert map to slice for JSON serialization
    sequences := make([]int, 0, len(c.webrtc.receiveTransfer.missingChunks))
    for seq := range c.webrtc.receiveTransfer.missingChunks {
        sequences = append(sequences, seq)
    }

    // Limit the number of chunks to request at once
    maxToRequest := 50
    if len(sequences) > maxToRequest {
        sequences = sequences[:maxToRequest]
    }

    c.ui.LogDebug(fmt.Sprintf("Requesting missing chunks: %v", sequences))

    // Send request for missing chunks
    request := struct {
        Type      string `json:"type"`
        Sequences []int  `json:"sequences"`
    }{
        Type:      "request-chunks",
        Sequences: sequences,
    }

    requestJSON, err := json.Marshal(request)
    if err != nil {
        c.ui.ShowError(fmt.Sprintf("Failed to marshal chunk request: %v", err))
        return
    }

    err = c.webrtc.dataChannel.SendText(string(requestJSON))
    if err != nil {
        c.ui.ShowError(fmt.Sprintf("Failed to send chunk request: %v", err))
        return
    }
}

// Handle file info message and initialize sliding window parameters
// Handle file info update message (e.g., MD5 hash update)
func (c *Client) handleFileInfoUpdate(data map[string]interface{}) {
    if !c.webrtc.receiveTransfer.inProgress || c.webrtc.receiveTransfer.fileTransfer == nil {
        return
    }

    infoMap, ok := data["info"].(map[string]interface{})
    if !ok {
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

    // Write all chunks to file
    for i, chunk := range c.webrtc.receiveTransfer.chunks {
        if chunk == nil {
            c.ui.ShowError(fmt.Sprintf("Missing chunk %d", i))
            c.webrtc.receiveTransfer.fileTransfer.file.Close()
            c.webrtc.receiveTransfer = transferState{}
            return
        }

        _, err := c.webrtc.receiveTransfer.fileTransfer.file.Write(chunk)
        if err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to write chunk: %v", err))
            c.webrtc.receiveTransfer.fileTransfer.file.Close()
            c.webrtc.receiveTransfer = transferState{}
            return
        }
    }

    // Close file first
    c.webrtc.receiveTransfer.fileTransfer.file.Close()

    // Compute file hash and verify integrity
    fileHash, err := calculateMD5(c.webrtc.receiveTransfer.fileTransfer.filePath)
    if err != nil {
        c.ui.ShowError(fmt.Sprintf("Failed to calculate file hash: %v", err))
        c.webrtc.receiveTransfer = transferState{}
        return
    }
    
    // Verify against provided hash if available
    if c.webrtc.receiveTransfer.fileTransfer.MD5 != "" {
        if fileHash != c.webrtc.receiveTransfer.fileTransfer.MD5 {
            c.ui.ShowError(fmt.Sprintf("File integrity check failed:\nExpected MD5: %s\nActual MD5:   %s", 
                c.webrtc.receiveTransfer.fileTransfer.MD5, fileHash))
            c.webrtc.receiveTransfer = transferState{}
            return
        }
        c.ui.LogDebug(fmt.Sprintf("File integrity verified (MD5: %s)", fileHash))
    } else {
        c.ui.LogDebug(fmt.Sprintf("File received (MD5: %s)", fileHash))
    }

    // Calculate transfer statistics
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

func (c *Client) Connect(peerToken string) error {
    if c.webrtc.connected {
        return fmt.Errorf("already connected to a peer")
    }
    c.webrtc = &WebRTCState{
        peerToken:   peerToken,
        isInitiator: true,
    }
    return c.SendMessage(Message{Type: "connect", PeerToken: peerToken})
}

func (c *Client) Accept(peerToken string) error {
    if c.webrtc.connected {
        return fmt.Errorf("already connected to a peer")
    }
    c.webrtc = &WebRTCState{peerToken: peerToken, isInitiator: false}
    return c.SendMessage(Message{Type: "accept", PeerToken: peerToken})
}

func (c *Client) Reject(peerToken string) error {
    if c.webrtc.connected {
        c.disconnectPeer()
    }
    return c.SendMessage(Message{Type: "reject", PeerToken: peerToken})
}

func (c *Client) disconnectPeer() {
    if c.webrtc.sendTransfer.fileTransfer != nil && c.webrtc.sendTransfer.fileTransfer.file != nil {
        c.webrtc.sendTransfer.fileTransfer.file.Close()
    }
    if c.webrtc.receiveTransfer.fileTransfer != nil && c.webrtc.receiveTransfer.fileTransfer.file != nil {
        c.webrtc.receiveTransfer.fileTransfer.file.Close()
    }
    if c.webrtc.peerConn != nil {
        c.webrtc.peerConn.Close()
        c.webrtc.peerConn = nil
    }
    if c.webrtc.dataChannel != nil {
        c.webrtc.dataChannel.Close()
        c.webrtc.dataChannel = nil
    }
    c.webrtc = &WebRTCState{}
}

func (c *Client) SendChat(text string) error {
    if !c.webrtc.connected {
        return fmt.Errorf("not connected to peer")
    }

    msg := struct {
        Type    string `json:"type"`
        Content string `json:"content"`
    }{
        Type:    "message",
        Content: text,
    }

    msgJSON, err := json.Marshal(msg)
    if err != nil {
        return fmt.Errorf("failed to marshal message: %v", err)
    }

    err = c.webrtc.dataChannel.SendText(string(msgJSON))
    if err != nil {
        return fmt.Errorf("failed to send message: %v", err)
    }

    return nil
}

func (c *Client) handleMessages() {
    c.ui.LogDebug("Message handler started, waiting for server token...")
    for {
        var msg Message
        err := c.conn.ReadJSON(&msg)
        if err != nil {
            c.ui.LogDebug(fmt.Sprintf("Error reading message: %v", err))
            c.ui.ShowError("Connection error - please try again")
            return
        }

        switch msg.Type {
        case "token":
            c.token = msg.Token
            c.ui.SetToken(msg.Token)

        case "request":
            c.ui.ShowConnectionRequest(msg.Token)
            c.webrtc.peerToken = msg.Token
            c.webrtc.isInitiator = false

        case "accepted":
            if c.webrtc.peerToken == "" {
                c.ui.ShowError("No active connection attempt")
                continue
            }

            if err := c.setupPeerConnection(); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to setup peer connection: %v", err))
                continue
            }

            offer, err := c.webrtc.peerConn.CreateOffer(nil)
            if err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to create offer: %v", err))
                continue
            }

            if err := c.webrtc.peerConn.SetLocalDescription(offer); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to set local description: %v", err))
                continue
            }

            offerObj := struct {
                Type string `json:"type"`
                SDP  string `json:"sdp"`
            }{
                Type: offer.Type.String(),
                SDP:  offer.SDP,
            }
            offerJSON, err := json.Marshal(offerObj)
            if err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to marshal offer: %v", err))
                continue
            }
            
            err = c.SendMessage(Message{
                Type:      "offer",
                PeerToken: c.webrtc.peerToken,
                SDP:       string(offerJSON),
            })
            if err != nil {
                c.ui.ShowError("Failed to send offer")
                continue
            }

        case "offer", "answer":
            c.handleSDP(msg)

        case "ice":
            var candidate webrtc.ICECandidateInit
            if err := json.Unmarshal([]byte(msg.ICE), &candidate); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to parse ICE candidate: %v", err))
                continue
            }

            if err := c.webrtc.peerConn.AddICECandidate(candidate); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to add ICE candidate: %v", err))
                continue
            }

        case "rejected":
            c.ui.ShowConnectionRejected(msg.Token)
            c.disconnectPeer()

        case "error":
            c.ui.ShowError(msg.SDP)
            c.disconnectPeer()
        }
    }
}

func (c *Client) setupPeerConnection() error {
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

    peerConn, err := webrtc.NewPeerConnection(config)
    if err != nil {
        return fmt.Errorf("failed to create peer connection: %v", err)
    }

    peerConn.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
        c.ui.LogDebug(fmt.Sprintf("Connection state changed to: %s", state))
        switch state {
        case webrtc.PeerConnectionStateFailed:
            c.ui.ShowError("Connection failed - attempting ICE restart")
            if offer, err := peerConn.CreateOffer(&webrtc.OfferOptions{ICERestart: true}); err == nil {
                if err := peerConn.SetLocalDescription(offer); err == nil {
                    offerObj := struct {
                        Type string `json:"type"`
                        SDP  string `json:"sdp"`
                    }{
                        Type: offer.Type.String(),
                        SDP:  offer.SDP,
                    }
                    offerJSON, err := json.Marshal(offerObj)
                    if err == nil {
                        c.SendMessage(Message{
                            Type:      "offer",
                            PeerToken: c.webrtc.peerToken,
                            SDP:       string(offerJSON),
                        })
                    }
                }
            }
        case webrtc.PeerConnectionStateDisconnected:
            c.ui.LogDebug("Connection disconnected - waiting for reconnection")
        case webrtc.PeerConnectionStateClosed:
            c.ui.LogDebug("Connection closed")
            c.disconnectPeer()
        }
    })

    // Common parameters
    ordered := true
    negotiated := true
    maxRetransmits := uint16(30)

    // Create control channel for metadata (ID: 1)
    controlID := uint16(1)
    controlChannelConfig := &webrtc.DataChannelInit{
        Ordered:        &ordered,
        MaxRetransmits: &maxRetransmits,
        Negotiated:     &negotiated,
        ID:             &controlID,
    }

    controlChannel, err := peerConn.CreateDataChannel("p2pftp-control", controlChannelConfig)
    if err != nil {
        return fmt.Errorf("failed to create control channel: %v", err)
    }

    // Create binary data channel for file transfers (ID: 2)
    dataID := uint16(2)
    dataChannelConfig := &webrtc.DataChannelInit{
        Ordered:        &ordered,
        MaxRetransmits: &maxRetransmits,
        Negotiated:     &negotiated,
        ID:             &dataID,
    }

    dataChannel, err := peerConn.CreateDataChannel("p2pftp-data", dataChannelConfig)
    if err != nil {
        return fmt.Errorf("failed to create data channel: %v", err)
    }

    // Set up control channel handlers
    controlChannel.OnOpen(func() {
        c.ui.LogDebug("Control channel opened")

        // Store the control channel
        c.webrtc.controlChannel = controlChannel

        // Check if both channels are open
        if c.webrtc.dataChannel != nil && c.webrtc.dataChannel.ReadyState() == webrtc.DataChannelStateOpen {
            c.completeConnectionSetup()
        }
    })

    controlChannel.OnClose(func() {
        c.ui.LogDebug("Control channel closed")
        c.disconnectPeer()
    })

    // Handle control channel messages (JSON)
    controlChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
        if !msg.IsString {
            c.ui.ShowError("Unexpected binary message on control channel")
            return
        }

        var data map[string]interface{}
        if err := json.Unmarshal(msg.Data, &data); err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to parse message: %v", err))
            return
        }

        msgType, ok := data["type"].(string)
        if !ok {
            c.ui.ShowError("Missing message type")
            return
        }

        switch msgType {
        case "message":
            if content, ok := data["content"].(string); ok {
                c.ui.ShowChat(c.webrtc.peerToken, content)
            }
        case "capabilities":
            // Handle capabilities message for chunk size negotiation
            if peerMaxChunkSize, ok := data["maxChunkSize"].(float64); ok {
                c.ui.LogDebug(fmt.Sprintf("Received peer's max chunk size: %d", int(peerMaxChunkSize)))

                // Use the smaller of our max and peer's max
                negotiatedSize := int(math.Min(float64(maxSupportedChunkSize), peerMaxChunkSize))
                maxChunkSize = negotiatedSize
                c.ui.LogDebug(fmt.Sprintf("Negotiated chunk size: %d", negotiatedSize))

                // Send acknowledgment with the negotiated size
                ack := struct {
                    Type               string `json:"type"`
                    NegotiatedChunkSize int    `json:"negotiatedChunkSize"`
                }{
                    Type:               "capabilities-ack",
                    NegotiatedChunkSize: negotiatedSize,
                }

                ackJSON, err := json.Marshal(ack)
                if err == nil {
                    c.webrtc.controlChannel.SendText(string(ackJSON))
                }
            }
        case "capabilities-ack":
            // Handle capabilities acknowledgment
            if negotiatedSize, ok := data["negotiatedChunkSize"].(float64); ok {
                c.ui.LogDebug(fmt.Sprintf("Peer acknowledged chunk size: %d", int(negotiatedSize)))
                maxChunkSize = int(negotiatedSize)
            }
        case "file-info-update":
            // Handle file info updates (like MD5 hash)
            c.handleFileInfoUpdate(data)
        case "file-info":
            c.handleFileInfo(data)
        case "chunk-info":
            // Handle chunk info for upcoming binary data
            if sequence, ok := data["sequence"].(float64); ok {
                if totalChunks, ok := data["totalChunks"].(float64); ok {
                    if size, ok := data["size"].(float64); ok {
                        // Store expected chunk info
                        c.webrtc.receiveTransfer.expectedChunk = &ChunkInfo{
                            Sequence:    int(sequence),
                            TotalChunks: int(totalChunks),
                            Size:        int(size),
                        }
                        c.ui.LogDebug(fmt.Sprintf("Expecting chunk %d of size %d bytes", int(sequence), int(size)))
                    }
                }
            }
        case "chunk-confirm":
            if sequence, ok := data["sequence"].(float64); ok {
                c.handleChunkConfirm(int(sequence))
            }
        case "file-complete":
            c.handleFileComplete()
        }
    })

    // Set up data channel handlers
    dataChannel.OnOpen(func() {
        c.ui.LogDebug("Data channel opened")

        // Store the data channel
        c.webrtc.dataChannel = dataChannel

        // Check if both channels are open
        if c.webrtc.controlChannel != nil && c.webrtc.controlChannel.ReadyState() == webrtc.DataChannelStateOpen {
            c.completeConnectionSetup()
        }
    })

    dataChannel.OnClose(func() {
        c.ui.LogDebug("Data channel closed")
        c.disconnectPeer()
    })

    // Handle binary data channel messages
    dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
        // We expect binary data on this channel
        if msg.IsString {
            c.ui.ShowError("Unexpected text message on binary channel")
            return
        }

        // Check if we're expecting a chunk
        if c.webrtc.receiveTransfer.expectedChunk == nil {
            c.ui.ShowError("Received binary data but no chunk was expected")
            return
        }

        // Get the expected chunk info
        chunkInfo := c.webrtc.receiveTransfer.expectedChunk

        // Validate the size
        if len(msg.Data) != chunkInfo.Size {
            c.ui.ShowError(fmt.Sprintf("Chunk size mismatch. Expected: %d, Got: %d", chunkInfo.Size, len(msg.Data)))
            return
        }

        // Process the binary chunk
        c.handleBinaryChunkData(chunkInfo.Sequence, chunkInfo.TotalChunks, chunkInfo.Size, msg.Data)

        // Clear the expected chunk
        c.webrtc.receiveTransfer.expectedChunk = nil
    })

    // Store the peer connection
    c.webrtc.peerConn = peerConn

    peerConn.OnICECandidate(func(candidate *webrtc.ICECandidate) {
        if candidate == nil {
            return
        }

        candidateJSON, err := json.Marshal(candidate.ToJSON())
        if err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to marshal ICE candidate: %v", err))
            return
        }

        err = c.SendMessage(Message{
            Type:      "ice",
            PeerToken: c.webrtc.peerToken,
            ICE:       string(candidateJSON),
        })
        if err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to send ICE candidate: %v", err))
        }
    })

    return nil
}

func (c *Client) handleSDP(msg Message) {
    var sdpObj struct {
        Type string `json:"type"`
        SDP  string `json:"sdp"`
    }

    if err := json.Unmarshal([]byte(msg.SDP), &sdpObj); err != nil {
        c.ui.ShowError(fmt.Sprintf("Failed to parse SDP: %v", err))
        return
    }

    if msg.Type == "offer" {
        if err := c.setupPeerConnection(); err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to setup peer connection: %v", err))
            return
        }

        offer := webrtc.SessionDescription{
            Type: webrtc.SDPTypeOffer,
            SDP:  sdpObj.SDP,
        }

        if err := c.webrtc.peerConn.SetRemoteDescription(offer); err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to set remote description: %v", err))
            return
        }

        answer, err := c.webrtc.peerConn.CreateAnswer(nil)
        if err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to create answer: %v", err))
            return
        }

        if err := c.webrtc.peerConn.SetLocalDescription(answer); err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to set local description: %v", err))
            return
        }

        answerJSON, err := json.Marshal(struct {
            Type string `json:"type"`
            SDP  string `json:"sdp"`
        }{
            Type: answer.Type.String(),
            SDP:  answer.SDP,
        })
        if err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to marshal answer: %v", err))
            return
        }

        err = c.SendMessage(Message{
            Type:      "answer",
            PeerToken: c.webrtc.peerToken,
            SDP:       string(answerJSON),
        })
        if err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to send answer: %v", err))
        }
    } else if msg.Type == "answer" {
        answer := webrtc.SessionDescription{
            Type: webrtc.SDPTypeAnswer,
            SDP:  sdpObj.SDP,
        }

        if err := c.webrtc.peerConn.SetRemoteDescription(answer); err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to set remote description: %v", err))
        }
    }
}

// This is a duplicate of the handleFileInfo method defined earlier
// Removed to fix compilation error
