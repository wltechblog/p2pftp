package main

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ChunkResult represents the result of sending a chunk
type ChunkResult struct {
	ChunkIndex int
	Success    bool
	Error      error
}

// sendFileChunks sends file data in chunks using a sliding window approach
func (c *CLI) sendFileChunks(fileData []byte) {
	c.debugLog.Printf("Starting to send file chunks, total size: %d bytes", len(fileData))

	// Get the negotiated chunk size from the peer if available
	var chunkSize int
	if c.peer != nil && c.peer.GetNegotiatedChunkSize() > 0 {
		// Subtract 8 bytes for our header
		chunkSize = int(c.peer.GetNegotiatedChunkSize()) - 8
		c.debugLog.Printf("Using negotiated chunk size of %d bytes (plus 8-byte header)", chunkSize)
	} else {
		// Fall back to the protocol default
		chunkSize = 16384 - 8 // 16KB - 8 bytes for header = 16376 bytes
		c.debugLog.Printf("Using protocol default chunk size of %d bytes (plus 8-byte header)", chunkSize)
	}

	totalChunks := (len(fileData) + chunkSize - 1) / chunkSize
	c.debugLog.Printf("Will send %d chunks of %d bytes each (plus 8-byte header per chunk)", totalChunks, chunkSize)

	// Try to use data channel
	useDataChannel := c.peer != nil && c.peer.IsDataChannelOpen()
	c.debugLog.Printf("Data channel available: %v", useDataChannel)

	// If data channel is not available, we can't proceed
	if !useDataChannel {
		c.debugLog.Printf("Data channel not available, cannot send file")
		fmt.Println("Error: Data channel not available, cannot send file")
		return
	}

	// Track successful and failed chunks
	successCount := 0
	failCount := 0

	// Sliding window parameters
	windowSize := 8 // Number of chunks to send in parallel
	if totalChunks < windowSize {
		windowSize = totalChunks
	}

	c.debugLog.Printf("Using sliding window with size %d for parallel transfers", windowSize)

	// Channel for tracking chunk results
	resultChan := make(chan ChunkResult, windowSize*2)

	// WaitGroup to wait for all goroutines to finish
	var wg sync.WaitGroup

	// Mutex for protecting shared counters
	var counterMutex sync.Mutex

	// Current chunk index to send
	nextChunkToSend := 0

	// Start the sliding window
	for i := 0; i < windowSize && i < totalChunks; i++ {
		wg.Add(1)
		go sendChunk(c, fileData, i, chunkSize, &wg, resultChan)
		nextChunkToSend++
	}

	// Process results and send new chunks as needed
	for i := 0; i < totalChunks; i++ {
		// Wait for a chunk to complete
		result := <-resultChan

		// Update counters
		counterMutex.Lock()
		if result.Success {
			successCount++
		} else {
			failCount++
			c.debugLog.Printf("Chunk %d failed: %v, retrying...", result.ChunkIndex, result.Error)

			// Retry failed chunk
			wg.Add(1)
			go sendChunk(c, fileData, result.ChunkIndex, chunkSize, &wg, resultChan)
			counterMutex.Unlock()
			continue
		}

		// If there are more chunks to send, start a new one
		if nextChunkToSend < totalChunks {
			wg.Add(1)
			go sendChunk(c, fileData, nextChunkToSend, chunkSize, &wg, resultChan)
			nextChunkToSend++
		}
		counterMutex.Unlock()

		// Print progress
		percentage := float64(i+1) / float64(totalChunks) * 100
		fmt.Printf("\rSending: %.1f%% (%d/%d chunks, %d succeeded, %d failed)",
			percentage, i+1, totalChunks, successCount, failCount)
	}

	// Wait for all goroutines to finish
	wg.Wait()
	close(resultChan)

	fmt.Println("\nAll chunks sent")
	c.debugLog.Printf("File transfer complete: %d/%d chunks succeeded, %d failed",
		successCount, totalChunks, failCount)

	// Send file complete message
	complete := map[string]interface{}{
		"type": "file-complete",
	}

	completeData, err := json.Marshal(complete)
	if err != nil {
		c.debugLog.Printf("Error marshaling file-complete: %v", err)
		return
	}

	if err := c.peer.SendControl(completeData); err != nil {
		c.debugLog.Printf("Error sending file-complete: %v", err)
	}
}

// sendChunk sends a single chunk of data
func sendChunk(c *CLI, fileData []byte, chunkIndex int, chunkSize int, wg *sync.WaitGroup, resultChan chan ChunkResult) {
	defer wg.Done()

	// Calculate chunk boundaries
	start := chunkIndex * chunkSize
	end := start + chunkSize
	if end > len(fileData) {
		end = len(fileData)
	}

	// Create chunk with header (4 bytes sequence + 4 bytes length)
	chunkData := make([]byte, 8+end-start)

	// Write sequence number (4 bytes)
	chunkData[0] = byte(chunkIndex >> 24)
	chunkData[1] = byte(chunkIndex >> 16)
	chunkData[2] = byte(chunkIndex >> 8)
	chunkData[3] = byte(chunkIndex)

	// Write chunk size (4 bytes)
	size := end - start
	chunkData[4] = byte(size >> 24)
	chunkData[5] = byte(size >> 16)
	chunkData[6] = byte(size >> 8)
	chunkData[7] = byte(size)

	// Copy chunk data
	copy(chunkData[8:], fileData[start:end])

	c.debugLog.Printf("Sending chunk %d, size: %d bytes (including 8-byte header)", chunkIndex, len(chunkData))

	// Send via data channel
	err := c.peer.SendData(chunkData)

	// Create result
	result := ChunkResult{
		ChunkIndex: chunkIndex,
		Success:    err == nil,
		Error:      err,
	}

	if err != nil {
		c.debugLog.Printf("Error sending chunk %d: %v", chunkIndex, err)

		// Try again once after a short delay
		time.Sleep(50 * time.Millisecond)
		c.debugLog.Printf("Retrying chunk %d", chunkIndex)

		err = c.peer.SendData(chunkData)

		// Update result based on retry
		result.Success = err == nil
		result.Error = err

		if err != nil {
			c.debugLog.Printf("Retry also failed for chunk %d: %v", chunkIndex, err)
		} else {
			c.debugLog.Printf("Retry succeeded for chunk %d", chunkIndex)
		}
	}

	// Send result back through channel
	resultChan <- result
}
