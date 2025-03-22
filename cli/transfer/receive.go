package transfer

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"

	pionwebrtc "github.com/pion/webrtc/v3"
)

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
        dataChannel:     dataChannel,
        logger:          logger,
        progressCallback: progressCallback,
        chunkSize:       chunkSize,
    }
}

// Receiver handles incoming file transfers and messages
type Receiver struct {
    state            *TransferState
    controlChannel   *pionwebrtc.DataChannel
    dataChannel      *pionwebrtc.DataChannel
    logger           Logger
    progressCallback ProgressCallback
    chunkSize        int
}

// HandleControlMessage handles control channel messages
func (r *Receiver) HandleControlMessage(msg []byte) error {
    // Log the raw message
    r.logger.LogDebug(fmt.Sprintf("HandleControlMessage received raw message: %s", string(msg)))

    // Parse the message
    var message map[string]interface{}
    if err := json.Unmarshal(msg, &message); err != nil {
        r.logger.LogDebug(fmt.Sprintf("Failed to parse control message: %v", err))
        return fmt.Errorf("failed to parse control message: %v", err)
    }

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
        if err := r.handleFileInfo(message); err != nil {
            return fmt.Errorf("file-info handling error: %v", err)
        }
    case "chunk-info":
        if err := r.handleChunkInfo(message); err != nil {
            return fmt.Errorf("chunk-info handling error: %v", err)
        }
    case "file-complete":
        if err := r.handleFileComplete(); err != nil {
            return fmt.Errorf("file-complete handling error: %v", err)
        }
    case "message":
        // Handle chat message
        content, ok := message["content"].(string)
        if !ok {
            r.logger.LogDebug("Invalid message format: missing content")
            return fmt.Errorf("invalid message format: missing content")
        }
        r.logger.LogDebug(fmt.Sprintf("Chat message: %s", content))
        r.logger.LogDebug("Displaying chat message")
        r.logger.ShowChat("peer", content)
        
    case "capabilities":
        if err := r.handleCapabilities(message); err != nil {
            return fmt.Errorf("capabilities handling error: %v", err)
        }
    case "capabilities-ack":
        if err := r.handleCapabilitiesAck(message); err != nil {
            return fmt.Errorf("capabilities-ack handling error: %v", err)
        }
    case "chunk-ack":
        if err := r.handleChunkAck(message); err != nil {
            return fmt.Errorf("chunk-ack handling error: %v", err)
        }
    case "chunk-request":
        if err := r.handleChunkRequest(message); err != nil {
            return fmt.Errorf("chunk-request handling error: %v", err)
        }
    default:
        r.logger.LogDebug(fmt.Sprintf("Unknown message type: %s", msgType))
        return fmt.Errorf("unknown message type: %s", msgType)
    }

    return nil
}

// handleFileInfo handles the file-info message
func (r *Receiver) handleFileInfo(message map[string]interface{}) error {
    r.logger.LogDebug("Handling file-info message")
    
    // Extract file info from message
    fileInfoMap, ok := message["info"].(map[string]interface{})
    if !ok {
        r.logger.LogDebug("Invalid file-info message: missing or invalid info field")
        return fmt.Errorf("invalid file info")
    }
    
    // Extract individual fields
    name, ok := fileInfoMap["name"].(string)
    if !ok {
        r.logger.LogDebug("Invalid file info: missing or invalid name")
        return fmt.Errorf("invalid file name")
    }
    
    size, ok := fileInfoMap["size"].(float64)
    if !ok {
        r.logger.LogDebug("Invalid file info: missing or invalid size")
        return fmt.Errorf("invalid file size")
    }
    
    md5, ok := fileInfoMap["md5"].(string)
    if !ok {
        r.logger.LogDebug("Invalid file info: missing or invalid md5")
        return fmt.Errorf("invalid file md5")
    }
    
    // Create FileInfo struct
    fileInfo := &FileInfo{
        Name: name,
        Size: int64(size),
        MD5:  md5,
    }
    
    // Reset transfer state
    r.state = NewTransferState()
    r.state.inProgress = true
    r.state.startTime = time.Now()
    r.state.lastUpdate = time.Now()
    
    // Calculate total chunks
    totalChunks := int(math.Ceil(float64(size) / float64(r.chunkSize)))
    r.state.totalChunks = totalChunks
    
    // Initialize file transfer
    r.state.fileTransfer = NewFileTransfer(fileInfo, nil, name)
    
    // Show initial status
    r.progressCallback(fmt.Sprintf("⬇ %s [0%%] (0/s)", name), "receive")
    
    r.logger.LogDebug(fmt.Sprintf("Prepared to receive file: %s (%d bytes, %d chunks)", name, int64(size), totalChunks))
    return nil
}

// handleChunkInfo handles the chunk-info message
func (r *Receiver) handleChunkInfo(message map[string]interface{}) error {
    r.logger.LogDebug("Handling chunk-info message")
    
    // Extract fields
    sequence, ok := message["sequence"].(float64)
    if !ok {
        r.logger.LogDebug("Invalid chunk-info: missing or invalid sequence")
        return fmt.Errorf("invalid sequence")
    }
    
    totalChunks, ok := message["totalChunks"].(float64)
    if !ok {
        r.logger.LogDebug("Invalid chunk-info: missing or invalid totalChunks")
        return fmt.Errorf("invalid total chunks")
    }
    
    chunkSize, ok := message["size"].(float64)
    if !ok {
        r.logger.LogDebug("Invalid chunk-info: missing or invalid size")
        return fmt.Errorf("invalid chunk size")
    }
    
    // Store expected chunk info
    r.state.expectedChunk = &ChunkInfo{
        Sequence:    int(sequence),
        TotalChunks: int(totalChunks),
        Size:        int(chunkSize),
    }
    
    r.logger.LogDebug(fmt.Sprintf("Expecting chunk %d of %d (size: %d bytes)", 
        int(sequence), int(totalChunks), int(chunkSize)))
    return nil
}

// handleFileComplete handles the file-complete message
func (r *Receiver) handleFileComplete() error {
    r.logger.LogDebug("Handling file-complete message")
    
    // Check if we have all chunks
    for i := 0; i < r.state.totalChunks; i++ {
        if !r.state.receivedChunks[i] {
            r.logger.LogDebug(fmt.Sprintf("Missing chunk %d, requesting retransmission", i))
            
            // Request missing chunk
            request := map[string]interface{}{
                "type": "request-chunks",
                "sequences": []int{i},
            }
            
            requestJSON, err := json.Marshal(request)
            if err != nil {
                r.logger.LogDebug(fmt.Sprintf("Failed to marshal chunk request: %v", err))
                return err
            }
            
            err = r.controlChannel.SendText(string(requestJSON))
            if err != nil {
                r.logger.LogDebug(fmt.Sprintf("Failed to send chunk request: %v", err))
                return err
            }
            
            return nil // Wait for missing chunks
        }
    }
    
    // Create the file
    file, err := os.Create(r.state.fileTransfer.Name)
    if err != nil {
        r.logger.LogDebug(fmt.Sprintf("Failed to create file: %v", err))
        return err
    }
    defer file.Close()
    
    // Write chunks in order
    for i := 0; i < r.state.totalChunks; i++ {
        _, err := file.Write(r.state.chunks[i])
        if err != nil {
            r.logger.LogDebug(fmt.Sprintf("Failed to write chunk %d: %v", i, err))
            return err
        }
    }
    
    // Verify MD5 hash
    hash, err := CalculateMD5(r.state.fileTransfer.Name)
    if err != nil {
        r.logger.LogDebug(fmt.Sprintf("Failed to calculate MD5: %v", err))
        return err
    }
    
    if hash != r.state.fileTransfer.MD5 {
        r.logger.LogDebug(fmt.Sprintf("MD5 mismatch. Expected: %s, Got: %s", 
            r.state.fileTransfer.MD5, hash))
        return fmt.Errorf("md5 mismatch")
    }
    
    // Calculate final statistics
    avgSpeed := float64(r.state.fileTransfer.Size) / time.Since(r.state.startTime).Seconds()
    
    // Show completion message
    r.progressCallback(fmt.Sprintf("⬇ %s - Complete (avg: %.1f MB/s)",
        r.state.fileTransfer.Name,
        avgSpeed/1024/1024),
        "receive")
    
    r.logger.LogDebug(fmt.Sprintf("File transfer complete: %s", r.state.fileTransfer.Name))
    
    // Reset transfer state
    r.state = NewTransferState()
    return nil
}

// handleCapabilities handles the capabilities message
func (r *Receiver) handleCapabilities(message map[string]interface{}) error {
    r.logger.LogDebug("Handling capabilities message")
    
    // Extract max chunk size from message
    maxChunkSize, ok := message["maxChunkSize"].(float64)
    if !ok {
        r.logger.LogDebug("Invalid capabilities message: missing or invalid maxChunkSize")
        return fmt.Errorf("invalid max chunk size")
    }
    
    // Store the sender's max chunk size
    r.state.SetPeerMaxChunkSize(int(maxChunkSize))
    
    // Send capabilities acknowledgment
    ack := map[string]interface{}{
        "type": "capabilities-ack",
        "maxChunkSize": r.chunkSize,
    }
    
    // Marshal the ack message
    ackJSON, err := json.Marshal(ack)
    if err != nil {
        r.logger.LogDebug(fmt.Sprintf("Failed to marshal capabilities-ack: %v", err))
        return err
    }
    
    // Send the ack message
    err = r.controlChannel.SendText(string(ackJSON))
    if err != nil {
        r.logger.LogDebug(fmt.Sprintf("Failed to send capabilities-ack: %v", err))
        return err
    }
    
    r.logger.LogDebug("Sent capabilities acknowledgment")
    return nil
}

// handleCapabilitiesAck handles the capabilities-ack message
func (r *Receiver) handleCapabilitiesAck(message map[string]interface{}) error {
    r.logger.LogDebug("Handling capabilities-ack message")
    
    // Extract peer's max chunk size
    maxChunkSize, ok := message["maxChunkSize"].(float64)
    if !ok {
        r.logger.LogDebug("Invalid capabilities-ack message: missing or invalid maxChunkSize")
        return fmt.Errorf("invalid max chunk size")
    }
    
    // Store the peer's max chunk size
    r.state.SetPeerMaxChunkSize(int(maxChunkSize))
    
    r.logger.LogDebug(fmt.Sprintf("Stored peer's max chunk size: %d", int(maxChunkSize)))
    return nil
}

// handleChunkRequest handles the chunk-request message
func (r *Receiver) handleChunkRequest(message map[string]interface{}) error {
    r.logger.LogDebug("Handling chunk-request message")
    
    // Extract sequences array
    sequencesInterface, ok := message["sequences"].([]interface{})
    if !ok {
        r.logger.LogDebug("Invalid chunk-request message: missing or invalid sequences")
        return fmt.Errorf("invalid sequences")
    }
    
    // Convert to []int
    sequences := make([]int, len(sequencesInterface))
    for i, seq := range sequencesInterface {
        seqFloat, ok := seq.(float64)
        if !ok {
            r.logger.LogDebug("Invalid sequence format in chunk-request message")
            return fmt.Errorf("invalid sequence format")
        }
        sequences[i] = int(seqFloat)
    }
    
    r.logger.LogDebug(fmt.Sprintf("Received request for %d chunks: %v", len(sequences), sequences))
    
    // As a receiver, we don't need to handle chunk requests
    r.logger.LogDebug("Ignoring chunk-request as we are the receiver")
    return nil
}

// handleChunkAck handles the chunk-ack message
func (r *Receiver) handleChunkAck(message map[string]interface{}) error {
    r.logger.LogDebug("Handling chunk-ack message")
    
    // As a receiver, we don't need to handle chunk acks
    r.logger.LogDebug("Ignoring chunk-ack as we are the receiver")
    return nil
}

// HandleDataChunk handles receiving data chunks
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

    // Verify expected chunk
    if r.state.expectedChunk == nil {
        r.logger.LogDebug("ERROR: No chunk info received before data")
        return fmt.Errorf("no chunk info received")
    }

    if sequence != r.state.expectedChunk.Sequence {
        r.logger.LogDebug(fmt.Sprintf("ERROR: Unexpected sequence. Got: %d, Expected: %d", 
            sequence, r.state.expectedChunk.Sequence))
        return fmt.Errorf("unexpected sequence")
    }

    // Store the chunk data
    if r.state.chunks == nil {
        r.state.chunks = make([][]byte, r.state.totalChunks)
    }
    r.state.chunks[sequence] = make([]byte, len(chunkData))
    copy(r.state.chunks[sequence], chunkData)

    // Mark chunk as received
    r.state.receivedChunks[sequence] = true
    r.state.lastReceivedSequence = sequence

    // Update received size
    r.state.receivedSize += int64(len(chunkData))

    // Calculate and show progress
    if time.Since(r.state.lastUpdate) >= time.Second {
        progress := float64(r.state.receivedSize) / float64(r.state.fileTransfer.Size) * 100
        speed := float64(r.state.receivedSize-r.state.lastUpdateSize) / time.Since(r.state.lastUpdate).Seconds()
        
        r.progressCallback(fmt.Sprintf("⬇ %s [%.1f%%] (%.1f MB/s)", 
            r.state.fileTransfer.Name,
            progress,
            speed/1024/1024),
            "receive")
        
        r.state.lastUpdate = time.Now()
        r.state.lastUpdateSize = r.state.receivedSize
    }

    // Send chunk acknowledgment
    ack := map[string]interface{}{
        "type":     "chunk-confirm",
        "sequence": sequence,
    }

    ackJSON, err := json.Marshal(ack)
    if err != nil {
        r.logger.LogDebug(fmt.Sprintf("Failed to marshal chunk acknowledgment: %v", err))
        return err
    }

    err = r.controlChannel.SendText(string(ackJSON))
    if err != nil {
        r.logger.LogDebug(fmt.Sprintf("Failed to send chunk acknowledgment: %v", err))
        return err
    }

    r.logger.LogDebug(fmt.Sprintf("Chunk %d processed and acknowledged", sequence))
    return nil
}
