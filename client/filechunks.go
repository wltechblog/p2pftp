package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// sendFileChunks sends file data in chunks
func (c *CLI) sendFileChunks(fileData []byte) {
	c.debugLog.Printf("Starting to send file chunks, total size: %d bytes", len(fileData))

	// Use a fixed, conservative chunk size for WebRTC data channels
	// WebRTC has practical limits that vary by implementation
	chunkSize := 4096 - 8 // 4KB - 8 bytes for header = 4088 bytes
	c.debugLog.Printf("Using fixed chunk size of %d bytes (plus 8-byte header) for reliability", chunkSize)

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

	for i := 0; i < totalChunks; i++ {
		// Calculate chunk boundaries
		start := i * chunkSize
		end := start + chunkSize
		if end > len(fileData) {
			end = len(fileData)
		}

		// Create chunk with header (4 bytes sequence + 4 bytes length)
		chunkData := make([]byte, 8+end-start)

		// Write sequence number (4 bytes)
		chunkData[0] = byte(i >> 24)
		chunkData[1] = byte(i >> 16)
		chunkData[2] = byte(i >> 8)
		chunkData[3] = byte(i)

		// Write chunk size (4 bytes)
		size := end - start
		chunkData[4] = byte(size >> 24)
		chunkData[5] = byte(size >> 16)
		chunkData[6] = byte(size >> 8)
		chunkData[7] = byte(size)

		// Copy chunk data
		copy(chunkData[8:], fileData[start:end])

		c.debugLog.Printf("Sending chunk %d of %d, size: %d bytes (including 8-byte header)", i+1, totalChunks, len(chunkData))

		// Send via data channel
		c.debugLog.Printf("Sending chunk %d via data channel", i)
		err := c.peer.SendData(chunkData)

		if err != nil {
			c.debugLog.Printf("Error sending chunk %d: %v", i, err)
			failCount++

			// Try again once after a short delay
			time.Sleep(50 * time.Millisecond)
			c.debugLog.Printf("Retrying chunk %d", i)

			err = c.peer.SendData(chunkData)
			if err != nil {
				c.debugLog.Printf("Retry also failed for chunk %d: %v", i, err)
				// Continue with next chunk
			} else {
				c.debugLog.Printf("Retry succeeded for chunk %d", i)
				successCount++
			}
		} else {
			successCount++
		}

		// Print progress
		percentage := float64(i+1) / float64(totalChunks) * 100
		fmt.Printf("\rSending: %.1f%% (%d/%d chunks, %d succeeded, %d failed)",
			percentage, i+1, totalChunks, successCount, failCount)

		// Add a small delay between chunks to avoid overwhelming the data channel
		time.Sleep(10 * time.Millisecond)
	}

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
