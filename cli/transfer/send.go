package transfer

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/pion/webrtc/v3"
)

// Sender handles sending files
type Sender struct {
	state            *TransferState
	controlChannel   *webrtc.DataChannel
	dataChannel      *webrtc.DataChannel
	logger           Logger
	progressCallback ProgressCallback
	chunkSize        int
	maxMessageSize   int
}

// NewSender creates a new file sender
func NewSender(
	controlChannel *webrtc.DataChannel,
	dataChannel *webrtc.DataChannel,
	logger Logger,
	progressCallback ProgressCallback,
	chunkSize int,
	maxMessageSize int,
) *Sender {
	return &Sender{
		state:            NewTransferState(),
		controlChannel:   controlChannel,
		dataChannel:      dataChannel,
		logger:           logger,
		progressCallback: progressCallback,
		chunkSize:        chunkSize,
		maxMessageSize:   maxMessageSize,
	}
}

// SendFile sends a file to the peer
func (s *Sender) SendFile(path string) error {
	if s.state.inProgress {
		return fmt.Errorf("upload already in progress")
	}

	// Ensure both channels are initialized
	if s.controlChannel == nil || s.dataChannel == nil {
		return fmt.Errorf("connection not fully established, please wait or reconnect")
	}

	// Check that both channels are in the open state
	if s.controlChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("control channel is not ready (state: %s), please wait or reconnect",
			s.controlChannel.ReadyState().String())
	}

	if s.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("data channel is not ready (state: %s), please wait or reconnect",
			s.dataChannel.ReadyState().String())
	}

	// Log the current state of the connection
	s.logger.LogDebug(fmt.Sprintf("Starting file transfer with control channel state: %s, data channel state: %s",
		s.controlChannel.ReadyState().String(), s.dataChannel.ReadyState().String()))

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
	fileHash, err := CalculateMD5(path)
	if err != nil {
		return fmt.Errorf("failed to calculate file hash: %v", err)
	}

	// Calculate total chunks
	totalChunks := int(math.Ceil(float64(info.Size()) / float64(s.chunkSize)))

	// Initialize transfer state
	s.state = &TransferState{
		inProgress: true,
		startTime:  time.Now(),
		lastUpdate: time.Now(),
		fileTransfer: &FileTransfer{
			FileInfo: &FileInfo{
				Name: info.Name(),
				Size: info.Size(),
				MD5:  fileHash,
			},
			file:     file,
			filePath: path,
		},
		windowSize:           64, // Default window size
		nextSequenceToSend:   0,
		lastAckedSequence:    -1,
		unacknowledgedChunks: make(map[int]bool),
		retransmissionQueue:  make([]int, 0),
		chunkTimestamps:      make(map[int]time.Time),
		congestionWindow:     64, // Start with full window
		totalChunks:          totalChunks,
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
	if s.controlChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("control channel is not in open state (current state: %s)",
			s.controlChannel.ReadyState().String())
	}

	err = s.controlChannel.SendText(string(infoJSON))
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Error sending file info: %v", err))
		return fmt.Errorf("failed to send file info: %v", err)
	}

	// Show initial status
	s.progressCallback(fmt.Sprintf("⬆ %s [0%%] (0/s)", info.Name()), "send")

	// Setup confirmation channel for chunk acknowledgments
	chunkConfirms := make(chan int, totalChunks)

	// Setup confirmation handler
	s.state.confirmHandler = func(sequence int) {
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
				s.handleChunkConfirmation(sequence)
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
				s.checkForRetransmissions()
			case <-done:
				return
			}
		}
	}()

	// Start the sliding window transfer
	err = s.startSlidingWindowTransfer()
	if err != nil {
		close(done)
		return err
	}

	// Wait for all chunks to be acknowledged
	for s.state.inProgress {
		if s.state.lastAckedSequence == totalChunks-1 {
			// All chunks acknowledged, we're done
			break
		}

		// Check if channels are still valid
		if s.controlChannel.ReadyState() != webrtc.DataChannelStateOpen ||
			s.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
			s.logger.LogDebug(fmt.Sprintf("Connection issue during file transfer - control: %s, data: %s",
				s.controlChannel.ReadyState().String(), s.dataChannel.ReadyState().String()))
			close(done)

			// Show completion message with warning
			s.progressCallback(fmt.Sprintf("⬆ %s - Incomplete (connection issue)",
				info.Name()),
				"send")

			// Reset transfer state
			s.state = NewTransferState()
			return fmt.Errorf("connection issue during file transfer")
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Calculate final statistics
	avgSpeed := float64(info.Size()) / time.Since(s.state.startTime).Seconds()

	// Show completion message
	s.progressCallback(fmt.Sprintf("⬆ %s - Finishing transfer...", info.Name()), "send")

	// Try to send complete message if the control channel is still open
	if s.controlChannel.ReadyState() == webrtc.DataChannelStateOpen {
		complete := struct {
			Type string `json:"type"`
		}{
			Type: "file-complete",
		}

		completeJSON, err := json.Marshal(complete)
		if err == nil {
			err = s.controlChannel.SendText(string(completeJSON))
			if err != nil {
				s.logger.LogDebug(fmt.Sprintf("Error sending complete message: %v", err))
				// Continue anyway, as the file transfer is complete
			} else {
				s.logger.LogDebug("Sent file-complete message successfully")
				// Wait a moment for the message to be sent
				time.Sleep(100 * time.Millisecond)
			}
		}
	} else {
		s.logger.LogDebug(fmt.Sprintf("Cannot send file-complete message: control channel not in open state (state: %s)",
			s.controlChannel.ReadyState().String()))
	}

	// Show final completion message
	s.progressCallback(fmt.Sprintf("⬆ %s - Complete (avg: %.1f MB/s)",
		info.Name(),
		avgSpeed/1024/1024),
		"send")

	// Signal goroutines to exit
	close(done)

	// Reset transfer state
	s.state = NewTransferState()

	return nil
}

// HandleChunkConfirm handles a chunk confirmation
func (s *Sender) HandleChunkConfirm(sequence int) {
	if s.state.confirmHandler != nil {
		s.state.confirmHandler(sequence)
	}
}

// HandleControlMessage handles control channel messages
func (s *Sender) HandleControlMessage(msg []byte) error {
	// Parse the message
	var message map[string]interface{}
	err := json.Unmarshal(msg, &message)
	if err != nil {
		return fmt.Errorf("failed to parse control message: %v", err)
	}

	// Get the message type
	msgType, ok := message["type"].(string)
	if !ok {
		return fmt.Errorf("invalid message format: missing type")
	}

	// Handle different message types
	switch msgType {
	case "chunk-confirm":
		// Extract sequence number
		sequenceFloat, ok := message["sequence"].(float64)
		if !ok {
			return fmt.Errorf("invalid chunk-confirm message: missing sequence")
		}
		sequence := int(sequenceFloat)

		// Handle the confirmation
		s.HandleChunkConfirm(sequence)

	case "request-chunks":
		// Extract sequences
		sequencesInterface, ok := message["sequences"].([]interface{})
		if !ok {
			return fmt.Errorf("invalid request-chunks message: missing sequences")
		}

		// Convert to []int
		sequences := make([]int, len(sequencesInterface))
		for i, seq := range sequencesInterface {
			seqFloat, ok := seq.(float64)
			if !ok {
				return fmt.Errorf("invalid sequence format in request-chunks message")
			}
			sequences[i] = int(seqFloat)
		}

		// Handle the request
		return s.HandleRequestChunks(sequences)

	case "capabilities-ack":
		// Extract negotiated chunk size
		negotiatedSizeFloat, ok := message["negotiatedChunkSize"].(float64)
		if !ok {
			return fmt.Errorf("invalid capabilities-ack message: missing negotiatedChunkSize")
		}
		negotiatedSize := int(negotiatedSizeFloat)

		// Update our chunk size to the negotiated value
		s.chunkSize = negotiatedSize

	default:
		s.logger.LogDebug(fmt.Sprintf("Unknown message type: %s", msgType))
	}

	return nil
}

// HandleRequestChunks handles a request for missing chunks
func (s *Sender) HandleRequestChunks(sequences []int) error {
	if !s.state.inProgress {
		return fmt.Errorf("no file transfer in progress")
	}

	s.logger.LogDebug(fmt.Sprintf("Received request for %d missing chunks", len(sequences)))

	// Add the requested chunks to the retransmission queue
	for _, sequence := range sequences {
		// Validate the sequence number
		if sequence < 0 || sequence >= s.state.totalChunks {
			s.logger.LogDebug(fmt.Sprintf("Ignoring invalid chunk sequence: %d", sequence))
			continue
		}

		// Check if this chunk has already been acknowledged
		if sequence <= s.state.lastAckedSequence {
			s.logger.LogDebug(fmt.Sprintf("Ignoring already acknowledged chunk: %d", sequence))
			continue
		}

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
			s.logger.LogDebug(fmt.Sprintf("Queuing chunk %d for retransmission (requested by receiver)", sequence))
		}
	}

	// Try to send the requested chunks immediately
	if s.controlChannel.ReadyState() == webrtc.DataChannelStateOpen &&
		s.dataChannel.ReadyState() == webrtc.DataChannelStateOpen {
		return s.trySendNextChunks()
	}

	return nil
}
