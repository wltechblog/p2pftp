package transfer

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/pion/webrtc/v3"
)

// handleChunkConfirmation handles a chunk confirmation
func (s *Sender) handleChunkConfirmation(sequence int) {
	// Update the last acknowledged sequence if this is the next expected one
	if sequence == s.state.lastAckedSequence + 1 {
		s.state.lastAckedSequence = sequence

		// Check for any consecutive acknowledged chunks
		nextSeq := sequence + 1
		for {
			if _, ok := s.state.unacknowledgedChunks[nextSeq]; ok {
				if s.state.unacknowledgedChunks[nextSeq] {
					s.state.lastAckedSequence = nextSeq
					delete(s.state.unacknowledgedChunks, nextSeq)
					delete(s.state.chunkTimestamps, nextSeq)
					nextSeq++
				} else {
					break
				}
			} else {
				break
			}
		}
	} else if sequence > s.state.lastAckedSequence {
		// Mark this chunk as acknowledged but don't update lastAckedSequence yet
		s.state.unacknowledgedChunks[sequence] = true
	}

	// Remove from unacknowledged chunks and timestamps
	if _, ok := s.state.unacknowledgedChunks[sequence]; ok {
		delete(s.state.unacknowledgedChunks, sequence)
		delete(s.state.chunkTimestamps, sequence)
	}

	// Increase congestion window on successful ACK (TCP-like slow start/congestion avoidance)
	if s.state.congestionWindow < s.state.windowSize {
		if s.state.congestionWindow < 32 {
			// Slow start - exponential growth
			s.state.congestionWindow = int(math.Min(float64(s.state.windowSize), float64(s.state.congestionWindow + 1)))
		} else {
			// Congestion avoidance - additive increase
			s.state.congestionWindow = int(math.Min(
				float64(s.state.windowSize),
				float64(s.state.congestionWindow) + (1.0 / float64(s.state.congestionWindow)),
			))
		}
	}

	// Reset consecutive timeouts counter on successful ACK
	s.state.consecutiveTimeouts = 0

	// Continue sending if we have more chunks to send and connection is still valid
	if s.controlChannel.ReadyState() == webrtc.DataChannelStateOpen && 
	   s.dataChannel.ReadyState() == webrtc.DataChannelStateOpen {
		s.trySendNextChunks()
	} else {
		s.logger.LogDebug(fmt.Sprintf("Cannot send more chunks: channels not in open state (control: %s, data: %s)",
			s.controlChannel.ReadyState().String(), s.dataChannel.ReadyState().String()))
	}

	// Update progress using chunkSize for consistency
	totalSent := int64(s.state.lastAckedSequence + 1) * int64(s.chunkSize)
	if totalSent > s.state.fileTransfer.Size {
		totalSent = s.state.fileTransfer.Size
	}

	now := time.Now()
	if time.Since(s.state.lastUpdate) > 100*time.Millisecond {
		timeDiff := now.Sub(s.state.lastUpdate).Seconds()
		if timeDiff > 0 {
			speed := float64(totalSent-s.state.lastUpdateSize) / timeDiff
			percentage := int((float64(totalSent) / float64(s.state.fileTransfer.Size)) * 100)
			s.progressCallback(fmt.Sprintf("⬆ %s [%d%%] (%.1f MB/s)",
				s.state.fileTransfer.Name,
				percentage,
				speed/1024/1024),
				"send")
			s.state.lastUpdate = now
			s.state.lastUpdateSize = totalSent
		}
	}
}

// checkForRetransmissions checks for chunks that need retransmission
func (s *Sender) checkForRetransmissions() {
	if !s.state.inProgress {
		return
	}

	// Check for unacknowledged chunks that might need retransmission
	now := time.Now()
	var timeoutsDetected int

	// Add old unacknowledged chunks to retransmission queue
	for sequence, timestamp := range s.state.chunkTimestamps {
		if sequence <= s.state.lastAckedSequence {
			// This was already ACKed, remove it
			delete(s.state.unacknowledgedChunks, sequence)
			delete(s.state.chunkTimestamps, sequence)
		} else {
			// Check if chunk has timed out (3 seconds)
			if now.Sub(timestamp) > 3*time.Second {
				// Add to retransmission queue if not already there
				alreadyQueued := false
				for _, seq := range s.state.retransmissionQueue {
					if seq == sequence {
						alreadyQueued = true
						break
					}
				}

				if !alreadyQueued {
					s.state.retransmissionQueue = append(s.state.retransmissionQueue, sequence)
					timeoutsDetected++
					s.logger.LogDebug(fmt.Sprintf("Queuing chunk %d for retransmission (timeout)", sequence))
				}
			}
		}
	}

	// Implement congestion control
	if timeoutsDetected > 0 {
		s.state.consecutiveTimeouts++

		// Reduce window size on timeouts (TCP-like congestion avoidance)
		if s.state.consecutiveTimeouts > 1 {
			// Multiplicative decrease
			s.state.congestionWindow = int(math.Max(8, math.Floor(float64(s.state.congestionWindow) * 0.7)))
			s.logger.LogDebug(fmt.Sprintf("Reducing congestion window to %d due to timeouts", s.state.congestionWindow))
		}
	} else {
		s.state.consecutiveTimeouts = 0

		// Additive increase if no timeouts
		if s.state.congestionWindow < s.state.windowSize {
			s.state.congestionWindow = int(math.Min(
				float64(s.state.windowSize),
				float64(s.state.congestionWindow + 1),
			))
		}
	}

	// Sort retransmission queue by sequence number
	sort.Ints(s.state.retransmissionQueue)

	// Try to send next chunks including retransmissions if connection is still valid
	if s.controlChannel.ReadyState() == webrtc.DataChannelStateOpen && 
	   s.dataChannel.ReadyState() == webrtc.DataChannelStateOpen {
		s.trySendNextChunks()
	} else {
		s.logger.LogDebug(fmt.Sprintf("Cannot send retransmissions: channels not in open state (control: %s, data: %s)",
			s.controlChannel.ReadyState().String(), s.dataChannel.ReadyState().String()))
	}
}

// startSlidingWindowTransfer starts the sliding window transfer
func (s *Sender) startSlidingWindowTransfer() error {
	// Try to send initial chunks within the window
	return s.trySendNextChunks()
}

// trySendNextChunks tries to send the next chunks within the window
func (s *Sender) trySendNextChunks() error {
	if !s.state.inProgress {
		return nil
	}
	
	// Check if channels are still valid
	if s.controlChannel.ReadyState() != webrtc.DataChannelStateOpen || 
	   s.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		s.logger.LogDebug(fmt.Sprintf("Cannot send chunks: channels not in open state (control: %s, data: %s)",
			s.controlChannel.ReadyState().String(), s.dataChannel.ReadyState().String()))
		return fmt.Errorf("channels not in open state")
	}

	// Calculate effective window size (min of congestion window and configured window size)
	effectiveWindowSize := int(math.Min(float64(s.state.congestionWindow), float64(s.state.windowSize)))

	// First handle any retransmissions (prioritize them)
	for len(s.state.retransmissionQueue) > 0 {
		sequence := s.state.retransmissionQueue[0]
		s.state.retransmissionQueue = s.state.retransmissionQueue[1:]

		// Skip if this chunk has already been acknowledged
		if sequence <= s.state.lastAckedSequence {
			continue
		}

		// Send the chunk
		err := s.sendChunkBySequence(sequence)
		if err != nil {
			return err
		}
	}

	// Then send new chunks within the window
	for s.state.nextSequenceToSend < s.state.totalChunks &&
		s.state.nextSequenceToSend <= s.state.lastAckedSequence + effectiveWindowSize {

		// Send the chunk
		sequence := s.state.nextSequenceToSend
		err := s.sendChunkBySequence(sequence)
		if err != nil {
			return err
		}

		s.state.nextSequenceToSend++
	}

	return nil
}

// sendChunkBySequence sends a specific chunk by sequence number
func (s *Sender) sendChunkBySequence(sequence int) error {
	if sequence >= s.state.totalChunks {
		return nil
	}

	// Calculate offset and size for this chunk
	offset := int64(sequence) * int64(s.chunkSize)
	end := int64(math.Min(float64(offset + int64(s.chunkSize)), float64(s.state.fileTransfer.Size)))
	size := int(end - offset)

	// Seek to the correct position in the file
	_, err := s.state.fileTransfer.file.Seek(offset, 0)
	if err != nil {
		return fmt.Errorf("failed to seek in file: %v", err)
	}

	// Read the chunk
	buf := make([]byte, size)
	n, err := s.state.fileTransfer.file.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read file: %v", err)
	}

	if n != size {
		return fmt.Errorf("failed to read complete chunk: expected %d bytes, got %d", size, n)
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
		TotalChunks: s.state.totalChunks,
		Size:        n,
	}

	// Marshal the chunk info
	chunkInfoJSON, err := json.Marshal(chunkInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal chunk info: %v", err)
	}

	// Check if control channel is still valid
	if s.controlChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("control channel is not in open state (current state: %s)",
			s.controlChannel.ReadyState().String())
	}

	// Send the chunk info on the control channel
	err = s.controlChannel.SendText(string(chunkInfoJSON))
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Error sending chunk info: %v", err))
		return fmt.Errorf("failed to send chunk info: %v", err)
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

	// Copy the actual data
	if n > 0 {
		copy(framedData[8:8+n], buf[:n])
	}

	// Log the exact size of the framed data
	s.logger.LogDebug(fmt.Sprintf("Created framed data for chunk %d: %d bytes header + %d bytes data = %d bytes total",
		sequence, 8, n, len(framedData)))

	// Check if the framed data is too large
	if len(framedData) > s.maxMessageSize {
		return fmt.Errorf("framed data too large: %d bytes (limit: %d)", len(framedData), s.maxMessageSize)
	}

	// Check if data channel is still valid
	if s.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("data channel is not in open state (current state: %s)",
			s.dataChannel.ReadyState().String())
	}

	// Send the framed binary data
	err = s.dataChannel.Send(framedData)
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Error sending chunk: %v", err))
		return fmt.Errorf("failed to send chunk: %v", err)
	}

	// Log success for debugging
	s.logger.LogDebug(fmt.Sprintf("Sent framed binary chunk %d (%d bytes data, %d bytes total)",
		sequence, n, len(framedData)))

	// Mark as unacknowledged and record timestamp
	s.state.unacknowledgedChunks[sequence] = false
	s.state.chunkTimestamps[sequence] = time.Now()

	// Add a small delay to ensure the data channel has time to process the send
	time.Sleep(1 * time.Millisecond)

	return nil
}